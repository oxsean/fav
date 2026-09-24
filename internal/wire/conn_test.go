package wire

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type text struct {
	Text string `json:"text"`
}

func echo(block chan struct{}, started chan string) Handler {
	return func(ctx context.Context, r *Request) (any, error) {
		var t text
		if err := r.Decode(&t); err != nil {
			return nil, err
		}
		switch r.Method {
		case "echo":
			return t, nil
		case "slow":
			if started != nil {
				started <- t.Text
			}
			select {
			case <-block:
				return t, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case "boom":
			panic("boom")
		}
		return nil, &Error{Code: CodeUnknownMethod, Detail: r.Method}
	}
}

func pair(t *testing.T, a, b Options) (*Conn, *Conn) {
	t.Helper()
	x, y := Pipe(a, b)
	t.Cleanup(func() { x.Close(); y.Close() })
	return x, y
}

func TestCallRoundTripsAndErrorsKeepTheConnection(t *testing.T) {
	c, _ := pair(t, Options{}, Options{Handler: echo(nil, nil)})
	var got text
	if err := c.Call(context.Background(), "echo", text{"中文 ✓ \"q\" 'x' %PATH%\n$HOME"}, &got); err != nil || got.Text != "中文 ✓ \"q\" 'x' %PATH%\n$HOME" {
		t.Fatalf("echo: %q %v", got.Text, err)
	}
	if err := c.Call(context.Background(), "nope", nil, nil); Code(err) != CodeUnknownMethod {
		t.Fatalf("unknown method: %v", err)
	}
	if err := c.Call(context.Background(), "boom", nil, nil); Code(err) != CodeInternal {
		t.Fatalf("a panic answers internal: %v", err)
	}
	if err := c.Call(context.Background(), MPing, nil, nil); err != nil || c.Err() != nil {
		t.Fatalf("errors from the other end must not close the connection: %v %v", err, c.Err())
	}
}

func TestBothEndsCallEachOther(t *testing.T) {
	a, b := pair(t, Options{Handler: echo(nil, nil)}, Options{Handler: echo(nil, nil)})
	var x, y text
	if err := a.Call(context.Background(), "echo", text{"a"}, &x); err != nil || x.Text != "a" {
		t.Fatal(x, err)
	}
	if err := b.Call(context.Background(), "echo", text{"b"}, &y); err != nil || y.Text != "b" {
		t.Fatal(y, err)
	}
}

func TestASlowCallDoesNotHoldBackOthers(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	started := make(chan string, 1)
	c, _ := pair(t, Options{}, Options{Handler: echo(block, started)})
	go c.Call(context.Background(), "slow", text{"s"}, nil)
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var got text
	if err := c.Call(ctx, "echo", text{"fast"}, &got); err != nil || got.Text != "fast" {
		t.Fatalf("answers come back out of order: %v", err)
	}
}

func TestATimedOutCallCancelsOnTheOtherEnd(t *testing.T) {
	canceled := make(chan struct{})
	h := func(ctx context.Context, r *Request) (any, error) {
		if r.Method == "slow" {
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		}
		return text{"ok"}, nil
	}
	c, _ := pair(t, Options{}, Options{Handler: h})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "slow", nil, nil); Code(err) != CodeTimeout {
		t.Fatalf("slow: %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("the other end kept working on a call nobody waits for")
	}
	var got text
	if err := c.Call(context.Background(), "echo", nil, &got); err != nil || got.Text != "ok" {
		t.Fatalf("the connection stays: %v", err)
	}
}

func TestCloseReturnsEveryWaitingCall(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	c, _ := pair(t, Options{}, Options{Handler: echo(block, nil)})
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.Call(context.Background(), "slow", nil, nil)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	closed := make(chan struct{})
	var why atomic.Value
	c.opt.OnClose = func(err error) { why.Store(err) }
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for calls")
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if Code(err) != CodeClosed {
			t.Fatalf("a waiting call ends as closed: %v", err)
		}
	}
	if err := c.Call(context.Background(), "echo", nil, nil); Code(err) != CodeClosed {
		t.Fatalf("after Close: %v", err)
	}
}

func TestTheOtherEndGoingAwayEndsTheConnection(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	c, d := pair(t, Options{}, Options{Handler: echo(block, nil)})
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "slow", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	d.Close()
	select {
	case err := <-errc:
		if Code(err) != CodeClosed {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the call kept waiting on a dead connection")
	}
	<-c.Done()
}

func TestHandlersRunAtMostMaxInflightAtOnce(t *testing.T) {
	var now, peak atomic.Int32
	release := make(chan struct{})
	h := func(ctx context.Context, r *Request) (any, error) {
		n := now.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		now.Add(-1)
		return nil, nil
	}
	c, _ := pair(t, Options{}, Options{Handler: h, MaxInflight: 3})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Call(context.Background(), "x", nil, nil) }()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if p := peak.Load(); p != 3 {
		t.Fatalf("peak %d handlers at once, want 3", p)
	}
}

func TestPushesArriveInOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	x, y := pair(t, Options{OnPush: func(m string, p json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()
		if got = append(got, m); len(got) == 3 {
			close(done)
		}
	}}, Options{})
	_ = x
	for _, m := range []string{"1", "2", "3"} {
		if err := y.Push(m, text{m}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pushes lost")
	}
	if strings.Join(got, "") != "123" {
		t.Fatal(got)
	}
}

type rw struct {
	io.Reader
	io.Writer
}

func (rw) Close() error { return nil }

func TestNoiseLinesAndNULsAreSkipped(t *testing.T) {
	res := `{"kind":"res","id":1,"ok":true,"result":{"text":"x"}}`
	in := "WARNING: post-quantum\n\x00" + strings.Join(strings.Split(res, ""), "\x00") + "\n"
	c := New(rw{strings.NewReader(in), io.Discard}, Options{})
	defer c.Close()
	var got text
	if err := c.Call(context.Background(), "echo", nil, &got); err != nil || got.Text != "x" {
		t.Fatalf("%q %v", got.Text, err)
	}
}

// lastWords reads one request, answers it and hangs up: the answer and the end arrive together.
type lastWords struct {
	*io.PipeReader
	*io.PipeWriter
}

func (l lastWords) Close() error { l.PipeReader.Close(); return l.PipeWriter.Close() }

func TestAnAnswerJustBeforeTheEndIsKept(t *testing.T) {
	for i := 0; i < 200; i++ {
		reqR, reqW := io.Pipe()
		resR, resW := io.Pipe()
		go func() {
			buf := make([]byte, 4096)
			reqR.Read(buf)
			resW.Write([]byte(`{"kind":"res","id":1,"ok":true,"result":{"text":"last"}}` + "\n"))
			resW.Close()
		}()
		c := New(lastWords{resR, reqW}, Options{})
		var got text
		if err := c.Call(context.Background(), "echo", nil, &got); err != nil || got.Text != "last" {
			t.Fatalf("run %d: %q %v", i, got.Text, err)
		}
	}
}

func TestAnOverlongFrameEndsTheConnection(t *testing.T) {
	c := New(rw{strings.NewReader(strings.Repeat("x", MaxFrame+10) + "\n"), io.Discard}, Options{})
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("still open")
	}
	if e := c.Err(); e == nil || !strings.Contains(e.Detail, "longer") {
		t.Fatal(e)
	}
}

type stuck struct{ io.Reader }

func (stuck) Write(p []byte) (int, error) { select {} }
func (stuck) Close() error                { return nil }

func TestAWriteThatHangsEndsTheConnection(t *testing.T) {
	pr, _ := io.Pipe()
	c := New(stuck{pr}, Options{WriteTimeout: 50 * time.Millisecond})
	err := c.Call(context.Background(), "echo", nil, nil)
	if Code(err) != CodeTimeout || c.Err() == nil {
		t.Fatalf("%v %v", err, c.Err())
	}
}

func TestKeepaliveEndsASilentConnection(t *testing.T) {
	pr, _ := io.Pipe()
	c := New(rw{pr, io.Discard}, Options{Keepalive: 40 * time.Millisecond})
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a silent connection stayed open")
	}
	if Code(c.Err()) != CodeTimeout {
		t.Fatal(c.Err())
	}
}

func TestPastTheQueueTheAnswerIsBusyAndPingsStillWork(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := func(ctx context.Context, r *Request) (any, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}
	c, _ := pair(t, Options{}, Options{Handler: h, MaxInflight: 1, MaxQueued: 2})
	for i := 0; i < 3; i++ { // one handled, two queued
		go c.Call(context.Background(), "x", nil, nil)
	}
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Call(ctx, "x", nil, nil); Code(err) != CodeBusy {
		t.Fatalf("a full queue answers busy at once: %v", err)
	}
	if err := c.Call(ctx, MPing, nil, nil); err != nil {
		t.Fatalf("ping goes past a full queue: %v", err)
	}
}
