package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type num struct {
	N int `json:"n"`
}

// streamer serves "count": n pushes, then done; "hold": a stream that stays open; "fail": an error after opening.
func streamer(opened chan *Stream) Handler {
	return func(ctx context.Context, r *Request) (any, error) {
		var p num
		r.Decode(&p)
		switch r.Method {
		case "count":
			s, err := r.Stream(StreamOptions{Class: ClassStream})
			if err != nil {
				return nil, err
			}
			go func() {
				s.Push(PushOpen, Open{Mode: ModeSnapshot})
				for i := range p.N {
					s.Push("n", num{i})
				}
				s.End(EndDone, nil)
			}()
			return nil, nil
		case "hold":
			s, err := r.Stream(StreamOptions{Class: ClassStream})
			if err != nil {
				return nil, err
			}
			if opened != nil {
				opened <- s
			}
			return nil, nil
		case "fail":
			if _, err := r.Stream(StreamOptions{}); err != nil {
				return nil, err
			}
			return nil, &Error{Code: CodeGone, Detail: "machine"}
		}
		return text{"ok"}, nil
	}
}

func within(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	recv(t, ch, what)
}

// recv is the next value from ch; a test never waits without a deadline.
func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal(what)
	}
	var zero T
	return zero
}

// bounded is the context for a test's calls and Next: it ends with the test and after 5 s.
func bounded(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func streamsOf(c *Conn) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.streams)
}

func TestAStreamHandsOutItsPushesInOrderUntilDone(t *testing.T) {
	c, _ := pair(t, Options{}, Options{Handler: streamer(nil)})
	w := c.Watch(context.Background(), "count", num{50})
	p, err := w.Next(bounded(t))
	if err != nil || p.Method != PushOpen {
		t.Fatalf("the first push: %v %v", p, err)
	}
	for i := range 50 {
		p, err := w.Next(bounded(t))
		var n num
		if err != nil || p.Method != "n" || p.Decode(&n) != nil || n.N != i {
			t.Fatalf("push %d: %s %s %v", i, p.Method, p.Params, err)
		}
	}
	if _, err := w.Next(bounded(t)); !errors.Is(err, io.EOF) || w.Err() != nil {
		t.Fatalf("the end: %v %v", err, w.Err())
	}
	var e Ended
	if err := w.Result(&e); err != nil || e != EndDone {
		t.Fatal(e, err)
	}
}

func TestAStreamItsProviderEndsWithAnErrorStaysEnded(t *testing.T) {
	c, _ := pair(t, Options{}, Options{Handler: streamer(nil)})
	w := c.Watch(context.Background(), "fail", nil)
	if _, err := w.Next(bounded(t)); Code(err) != CodeGone {
		t.Fatal(err)
	}
	if Code(w.Err()) != CodeGone {
		t.Fatal(w.Err())
	}
}

func TestCancellingAStreamEndsItOnBothEnds(t *testing.T) {
	opened := make(chan *Stream, 1)
	c, _ := pair(t, Options{}, Options{Handler: streamer(opened)})
	ctx, cancel := context.WithCancel(context.Background())
	w := c.Watch(ctx, "hold", nil)
	s := recv(t, opened, "the stream did not open")
	cancel()
	within(t, s.Context().Done(), "the provider's stream stayed open")
	if _, err := w.Next(bounded(t)); Code(err) != CodeCanceled {
		t.Fatal(err)
	}
	if err := s.Push("n", num{1}); Code(err) != CodeCanceled {
		t.Fatalf("a push after the cancel: %v", err)
	}
	w.Cancel() // again: nothing happens
}

func TestOpeningAndCancellingAtOnceLeavesNoStreamOpen(t *testing.T) {
	c, d := pair(t, Options{}, Options{Handler: streamer(nil)})
	for range 200 {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Watch(ctx, "hold", nil)
		c.Watch(context.Background(), "hold", nil).Cancel()
	}
	if err := c.Call(bounded(t), MPing, nil, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "streams left open", func() bool { return streamsOf(d) == 0 })
}

func TestStreamsTakeNoHandlerSlotButOpeningOneDoes(t *testing.T) {
	release := make(chan struct{})
	h := func(ctx context.Context, r *Request) (any, error) {
		if r.Method == "late" {
			<-release
		}
		if strings.HasSuffix(r.Method, "hold") || r.Method == "late" {
			_, err := r.Stream(StreamOptions{})
			return nil, err
		}
		return text{"ok"}, nil
	}
	c, _ := pair(t, Options{}, Options{Handler: h, MaxInflight: 1})
	for range 5 {
		c.Watch(context.Background(), "hold", nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Call(ctx, "echo", nil, nil); err != nil {
		t.Fatalf("open streams held the slot: %v", err)
	}
	c.Watch(context.Background(), "late", nil)
	short, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if err := c.Call(short, "echo", nil, nil); Code(err) != CodeTimeout {
		t.Fatalf("a handler that has not opened its stream yet holds its slot: %v", err)
	}
	close(release)
	if err := c.Call(ctx, "echo", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPastMaxStreamsTheAnswerIsBusy(t *testing.T) {
	c, d := pair(t, Options{}, Options{Handler: streamer(nil), MaxStreams: 2})
	a := c.Watch(context.Background(), "hold", nil)
	c.Watch(context.Background(), "hold", nil)
	waitFor(t, "two streams open", func() bool { return streamsOf(d) == 2 })
	if _, err := c.Watch(context.Background(), "hold", nil).Next(bounded(t)); Code(err) != CodeBusy {
		t.Fatal(err)
	}
	a.Cancel()
	waitFor(t, "a cancelled stream still counts", func() bool { return streamsOf(d) < 2 })
	w := c.Watch(context.Background(), "hold", nil)
	select {
	case <-w.Done():
		t.Fatal(w.Err())
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTheStreamThatFindsNoRoomIsBusyWhicheverWasSentFirst(t *testing.T) {
	late := make(chan struct{})
	h := func(ctx context.Context, r *Request) (any, error) {
		if r.Method == "late" {
			select {
			case <-late:
			case <-ctx.Done():
			}
		}
		_, err := r.Stream(StreamOptions{})
		return nil, err
	}
	c, d := pair(t, Options{}, Options{Handler: h, MaxStreams: 2})
	first := c.Watch(context.Background(), "late", nil)
	c.Watch(context.Background(), "hold", nil)
	c.Watch(context.Background(), "hold", nil)
	waitFor(t, "two streams open", func() bool { return streamsOf(d) == 2 })
	close(late)
	if _, err := first.Next(bounded(t)); Code(err) != CodeBusy {
		t.Fatal(err)
	}
}

func TestTheEndOfTheConnectionEndsEveryStream(t *testing.T) {
	opened := make(chan *Stream, 2)
	c, d := pair(t, Options{}, Options{Handler: streamer(opened)})
	w1, w2 := c.Watch(context.Background(), "hold", nil), c.Watch(context.Background(), "hold", nil)
	s1, s2 := recv(t, opened, "the stream did not open"), recv(t, opened, "the stream did not open")
	d.Close()
	for _, w := range []*Watch{w1, w2} {
		if _, err := w.Next(bounded(t)); Code(err) != CodeClosed {
			t.Fatal(err)
		}
	}
	within(t, s1.Context().Done(), "a stream outlived its connection")
	within(t, s2.Context().Done(), "a stream outlived its connection")
}

func TestAWatchWhoseReaderFallsBehindEndsAsLagged(t *testing.T) {
	opened := make(chan *Stream, 1)
	c, _ := pair(t, Options{WatchQueue: 4}, Options{Handler: streamer(opened)})
	w := c.Watch(context.Background(), "hold", nil)
	s := recv(t, opened, "the stream did not open")
	for i := range 10 {
		s.Push("n", num{i})
	}
	within(t, s.Context().Done(), "the provider was not told")
	within(t, w.Done(), "the watch stayed open")
	if Code(w.Err()) != CodeLagged {
		t.Fatal(w.Err())
	}
}

// stalled is a provider whose peer reads nothing until told: pushes pile up in the stream's queue.
func stalled(t *testing.T, opt StreamOptions) (*Stream, *peer) {
	t.Helper()
	opened := make(chan *Stream, 1)
	x, y := net.Pipe()
	c := New(x, Options{Handler: func(ctx context.Context, r *Request) (any, error) {
		s, err := r.Stream(opt)
		opened <- s
		return nil, err
	}})
	t.Cleanup(func() { c.Close() })
	b, _ := json.Marshal(Frame{Type: TypeReq, ID: 1, Method: "w"})
	y.Write(append(b, '\n'))
	s := recv(t, opened, "the stream did not open")
	if err := s.Push(PushOpen, Open{Mode: ModeResume}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // the writer is now stuck writing open
	return s, &peer{t: t, nc: y}
}

func (p *peer) start() { *p = *newPeer(p.t, p.nc) }

func (p *peer) methods(n int) []string {
	var got []string
	for range n {
		f := p.read(func(Frame) bool { return true })
		if f.Type == TypeRes && f.Error == nil {
			got = append(got, "res:done")
		} else if f.Type == TypeRes {
			got = append(got, "res:"+f.Error.Code)
		} else {
			got = append(got, f.Method+string(f.Params))
		}
	}
	return got
}

func TestAFullLaggingStreamEndsAsLagged(t *testing.T) {
	s, p := stalled(t, StreamOptions{Full: FullLag, Queue: 100})
	var err error
	for i := 0; err == nil; i++ {
		err = s.Push("n", num{i})
	}
	if Code(err) != CodeLagged {
		t.Fatal(err)
	}
	p.start()
	if got := p.methods(2); got[0] != `open{"mode":"resume"}` || got[1] != "res:lagged" {
		t.Fatal(got)
	}
}

func TestAFullGapStreamDropsOnlyItsMethodAndSaysWhere(t *testing.T) {
	s, p := stalled(t, StreamOptions{Full: FullGap, Queue: 100})
	var err error
	i := 0
	for ; err == nil; i++ {
		err = s.PushMark("run.output", num{i}, i)
	}
	var gap *GapError
	if !errors.As(err, &gap) || gap.Mark != 0 {
		t.Fatalf("%v %+v", err, gap)
	}
	if err := s.Push("gap", num{i}); err != nil {
		t.Fatal(err)
	}
	p.start()
	want := []string{`open{"mode":"resume"}`, fmt.Sprintf(`gap{"n":%d}`, i)}
	if got := p.methods(2); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatal(got)
	}
}

func TestAFullLatestStreamKeepsTheNewestOfAMethod(t *testing.T) {
	s, p := stalled(t, StreamOptions{Full: FullLatest, Queue: 100})
	for i := range 50 {
		if err := s.Push("machines", num{i}); err != nil {
			t.Fatal(err)
		}
	}
	s.End(EndDone, nil)
	p.start()
	got := p.methods(3)
	if got[0] != `open{"mode":"resume"}` || got[1] != `machines{"n":49}` {
		t.Fatal(got)
	}
}

func TestASlowStreamDoesNotHoldBackAnswersPingsOrState(t *testing.T) {
	opened := make(chan *Stream, 2)
	x, y := net.Pipe()
	c := New(x, Options{Handler: func(ctx context.Context, r *Request) (any, error) {
		cl := ClassStream
		if r.Method == "state.watch" {
			cl = ClassState
		}
		s, err := r.Stream(StreamOptions{Class: cl, Queue: 64 << 20})
		opened <- s
		return nil, err
	}})
	defer c.Close()
	p := &peer{t: t, nc: y}
	p.write(Frame{Type: TypeReq, ID: 1, Method: "run.output.watch"})
	out := recv(t, opened, "the stream did not open")
	p.write(Frame{Type: TypeReq, ID: 2, Method: "state.watch"})
	state := recv(t, opened, "the stream did not open")
	big := strings.Repeat("x", 8<<10)
	for range 200 {
		out.Push("run.output", text{big})
	}
	state.Push("journal", num{1})
	p.write(Frame{Type: TypeReq, ID: 3, Method: MPing})
	time.Sleep(20 * time.Millisecond)
	p.start()
	var ping, journal int
	for i := 1; ping == 0 || journal == 0; i++ {
		f := p.read(func(Frame) bool { return true })
		switch {
		case f.Type == TypeRes && f.ID == 3:
			ping = i
		case f.Method == "journal":
			journal = i
		}
	}
	if ping > 3 || journal > 3 {
		t.Fatalf("behind the output: the ping answer came %dth, the state push %dth", ping, journal)
	}
}

func TestPushWaitWaitsForRoomInsteadOfLagging(t *testing.T) {
	s, p := stalled(t, StreamOptions{Full: FullLag, Queue: 100})
	done := make(chan error, 1)
	ctx := bounded(t)
	go func() {
		for i := range 20 {
			if err := s.PushWait(ctx, "n", num{i}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		t.Fatalf("PushWait did not wait for a reader: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	p.start()
	got := p.methods(21)
	if err := recv(t, done, "PushWait did not return"); err != nil || got[20] != `n{"n":19}` {
		t.Fatal(err, got)
	}
}
