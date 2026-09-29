package wire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The frame files are the web client's (internal/server/webtest): "c" lines are what the client sends, "s" lines what
// the server sends; every other line drives the client and is skipped here.
var replayed = []string{"call", "call-queued", "call-timeout", "server-req", "watch", "watch-cancel", "watch-end", "watch-lagged", "output", "hidden"}

type scripted struct {
	client bool // a "c" line
	f      Frame
	raw    json.RawMessage
}

func script(t *testing.T, name string) []scripted {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "server", "webtest", "frames", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []scripted
	for _, line := range bytes.Split(b, []byte("\n")) {
		var l struct {
			C json.RawMessage `json:"c"`
			S json.RawMessage `json:"s"`
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := json.Unmarshal(line, &l); err != nil {
			t.Fatalf("%s: %s: %v", name, line, err)
		}
		raw, client := l.S, false
		if l.C != nil {
			raw, client = l.C, true
		}
		if raw == nil {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var f Frame
		if err := dec.Decode(&f); err != nil {
			t.Fatalf("%s: a frame Frame does not hold: %s: %v", name, raw, err)
		}
		out = append(out, scripted{client: client, f: f, raw: raw})
	}
	return out
}

func canon(t *testing.T, b []byte) any {
	t.Helper()
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return v
}

// subset: every field of want is in got with the same value; absent params are {}.
func subset(want, got any) bool {
	wm, ok := want.(map[string]any)
	if !ok {
		return reflect.DeepEqual(want, got)
	}
	gm, _ := got.(map[string]any)
	for k, v := range wm {
		if !subset(v, gm[k]) {
			return false
		}
	}
	return true
}

// peer is the scripted end: it reads frames the Conn under test writes.
type peer struct {
	t     *testing.T
	nc    net.Conn
	in    chan Frame
	extra []Frame
}

func newPeer(t *testing.T, nc net.Conn) *peer {
	p := &peer{t: t, nc: nc, in: make(chan Frame, 1024)}
	go func() {
		br := bufio.NewReader(nc)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				var f Frame
				if json.Unmarshal(line, &f) == nil {
					p.in <- f
				}
			}
			if err != nil {
				close(p.in)
				return
			}
		}
	}()
	return p
}

func (p *peer) write(f any) {
	p.t.Helper()
	b, _ := json.Marshal(f)
	if _, err := p.nc.Write(append(b, '\n')); err != nil {
		p.t.Fatal(err)
	}
}

// read is the next frame for which keep holds.
func (p *peer) read(keep func(Frame) bool) Frame {
	p.t.Helper()
	for i, f := range p.extra {
		if keep(f) {
			p.extra = append(p.extra[:i], p.extra[i+1:]...)
			return f
		}
	}
	for {
		select {
		case f, ok := <-p.in:
			if !ok {
				p.t.Fatal("the connection ended")
			}
			if keep(f) {
				return f
			}
			p.extra = append(p.extra, f)
		case <-time.After(3 * time.Second):
			p.t.Fatalf("no frame came; had %+v", p.extra)
		}
	}
}

// barrier returns once the Conn has dispatched everything written before it: it answers pings in order.
func (p *peer) barrier(id int64) {
	p.t.Helper()
	p.write(Frame{Type: TypeReq, ID: id, Method: MPing})
	p.read(func(f Frame) bool { return f.Type == TypeRes && f.ID == id })
}

type watchRec struct {
	w       chan *Watch
	mu      sync.Mutex
	got     []Push
	want    []Push
	live    bool
	wantEnd string // "" while open: done, or an error code
	ended   chan error
}

func (r *watchRec) seen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

type callRec struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
	result json.RawMessage
	want   *Frame
	gaveUp bool
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
	}
}

// TestReplayAsOpener: the Go end sends what the web client sends, and hands out what the server sends as the web
// client does.
func TestReplayAsOpener(t *testing.T) {
	for _, name := range replayed {
		t.Run(name, func(t *testing.T) {
			x, y := net.Pipe()
			c := New(x, Options{})
			defer c.Close()
			p := newPeer(t, y)
			watches, calls := map[int64]*watchRec{}, map[int64]*callRec{}
			barriers := int64(1 << 40)
			for _, l := range script(t, name) {
				f := l.f
				if !l.client {
					switch {
					case f.Type == TypePush && watches[f.ID] != nil && watches[f.ID].live:
						r := watches[f.ID]
						r.want = append(r.want, Push{Method: f.Method, Params: f.Params})
					case f.Type == TypeRes && calls[f.ID] != nil && !calls[f.ID].gaveUp:
						calls[f.ID].want = &f
					case f.Type == TypeRes && watches[f.ID] != nil && watches[f.ID].live:
						r := watches[f.ID]
						r.live, r.wantEnd = false, "done"
						if f.Error != nil {
							r.wantEnd = f.Error.Code
						}
					}
					p.write(json.RawMessage(l.raw))
					barriers++
					p.barrier(barriers)
					continue
				}
				switch {
				case f.Type == TypeReq && strings.HasSuffix(f.Method, ".watch"):
					r := &watchRec{w: make(chan *Watch, 1), live: true, ended: make(chan error, 1)}
					watches[f.ID] = r
					go func() {
						w := c.Watch(context.Background(), f.Method, json.RawMessage(orEmpty(f.Params)))
						r.w <- w
						for {
							push, err := w.Next(context.Background())
							if err != nil {
								r.ended <- err
								return
							}
							r.mu.Lock()
							r.got = append(r.got, push)
							r.mu.Unlock()
						}
					}()
				case f.Type == TypeReq:
					ctx, cancel := context.WithCancel(context.Background())
					r := &callRec{cancel: cancel, done: make(chan struct{})}
					calls[f.ID] = r
					go func() {
						defer close(r.done)
						r.err = c.CallCommand(ctx, f.Method, f.CommandID, json.RawMessage(orEmpty(f.Params)), &r.result)
					}()
				case f.Type == TypeCancel && calls[f.ID] != nil:
					calls[f.ID].gaveUp = true
					calls[f.ID].cancel()
				case f.Type == TypeCancel && watches[f.ID] != nil:
					r := watches[f.ID]
					waitFor(t, "pushes before the cancel", func() bool { return r.seen() == len(r.want) })
					r.live, r.wantEnd = false, CodeCanceled
					w := <-r.w
					r.w <- w
					w.Cancel()
				}
				got := p.read(func(g Frame) bool { return g.Type == f.Type && g.ID == f.ID })
				if got.Method != f.Method || got.CommandID != f.CommandID || !subset(canon(t, orEmpty(f.Params)), canon(t, orEmpty(got.Params))) ||
					!reflect.DeepEqual(canon(t, f.Result), canon(t, got.Result)) || !reflect.DeepEqual(f.Error, got.Error) {
					b, _ := json.Marshal(got)
					t.Fatalf("sent %s, the web client sends %s", b, l.raw)
				}
			}
			barriers++
			p.barrier(barriers)
			for id, r := range watches {
				waitFor(t, "the pushes", func() bool { return r.seen() >= len(r.want) })
				if r.wantEnd != "" {
					var err error
					select {
					case err = <-r.ended:
					case <-time.After(3 * time.Second):
						t.Fatalf("watch %d did not end", id)
					}
					code := Code(err)
					if errors.Is(err, io.EOF) {
						code = "done"
					}
					if code != r.wantEnd {
						t.Fatalf("watch %d ended %v, want %s", id, err, r.wantEnd)
					}
				}
				r.mu.Lock()
				if len(r.got) != len(r.want) {
					t.Fatalf("watch %d: %d pushes, want %d", id, len(r.got), len(r.want))
				}
				for i := range r.got {
					if r.got[i].Method != r.want[i].Method || !reflect.DeepEqual(canon(t, r.got[i].Params), canon(t, r.want[i].Params)) {
						t.Fatalf("watch %d push %d: %s %s, want %s %s", id, i, r.got[i].Method, r.got[i].Params, r.want[i].Method, r.want[i].Params)
					}
				}
				r.mu.Unlock()
			}
			for id, r := range calls {
				<-r.done
				switch {
				case r.gaveUp:
					if Code(r.err) != CodeTimeout {
						t.Fatalf("call %d: %v, want timeout", id, r.err)
					}
				case r.want == nil:
					t.Fatalf("call %d: nothing answers it", id)
				case r.want.Error != nil:
					if !reflect.DeepEqual(r.err, r.want.Error) {
						t.Fatalf("call %d: %v, want %v", id, r.err, r.want.Error)
					}
				case r.err != nil || !reflect.DeepEqual(canon(t, r.result), canon(t, r.want.Result)):
					t.Fatalf("call %d: %s %v, want %s", id, r.result, r.err, r.want.Result)
				}
			}
		})
	}
}

func orEmpty(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(`{}`)
	}
	return b
}

// TestReplayAsProvider: the Go end, told what to send, sends the server's frames; frames about a request the client
// cancelled are a race and not asked for, except that a stream answers its cancel once.
func TestReplayAsProvider(t *testing.T) {
	for _, name := range replayed {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			streams := map[int64]*Stream{}
			answers := map[int64]chan *Frame{}
			answer := func(id int64) chan *Frame {
				mu.Lock()
				defer mu.Unlock()
				if answers[id] == nil {
					answers[id] = make(chan *Frame, 1)
				}
				return answers[id]
			}
			opened := make(chan int64, 64)
			h := func(ctx context.Context, r *Request) (any, error) {
				if strings.HasSuffix(r.Method, ".watch") {
					s, err := r.Stream(StreamOptions{Class: ClassStream})
					if err != nil {
						return nil, err
					}
					mu.Lock()
					streams[r.id] = s
					mu.Unlock()
					opened <- r.id
					return nil, nil
				}
				select {
				case f := <-answer(r.id):
					if f.Error != nil {
						return nil, f.Error
					}
					return f.Result, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			x, y := net.Pipe()
			c := New(x, Options{Handler: h})
			defer c.Close()
			p := newPeer(t, y)
			cancelled := map[int64]bool{}
			isWatch, finished := map[int64]bool{}, map[int64]bool{}
			served := map[int64]int64{} // the script's id of a request this end sends → its own
			var calls sync.WaitGroup
			for _, l := range script(t, name) {
				f := l.f
				if l.client {
					if f.Type == TypeRes {
						f.ID = served[f.ID]
					}
					p.write(f)
					switch f.Type {
					case TypeReq:
						if strings.HasSuffix(f.Method, ".watch") {
							isWatch[f.ID] = true
							select {
							case <-opened:
							case <-time.After(3 * time.Second):
								t.Fatal("the stream did not open")
							}
						}
					case TypeCancel:
						cancelled[f.ID] = true
					}
					continue
				}
				if (cancelled[f.ID] || finished[f.ID]) && f.Type != TypeReq {
					continue
				}
				if f.Type == TypeRes {
					finished[f.ID] = true
				}
				switch {
				case f.Type == TypeReq:
					want := f
					calls.Add(1)
					sent := make(chan int64, 1)
					go func() {
						defer calls.Done()
						sent <- c.next.Load() + 1
						var params any
						if len(want.Params) > 0 {
							params = want.Params
						}
						c.Call(context.Background(), want.Method, params, nil)
					}()
					own := <-sent
					served[f.ID] = own
					f.ID = own
				case isWatch[f.ID]:
					mu.Lock()
					s := streams[f.ID]
					mu.Unlock()
					if f.Type == TypePush {
						if err := s.Push(f.Method, f.Params); err != nil {
							t.Fatal(err)
						}
					} else if f.Error != nil {
						s.End(nil, f.Error)
					} else {
						s.End(f.Result, nil)
					}
				default:
					ff := f
					answer(f.ID) <- &ff
				}
				want := f
				got := p.read(func(g Frame) bool { return !cancelled[g.ID] || g.Type == TypeReq })
				wb, _ := json.Marshal(want)
				gb, _ := json.Marshal(got)
				if !reflect.DeepEqual(canon(t, wb), canon(t, gb)) {
					t.Fatalf("sent %s, the server sends %s", gb, wb)
				}
			}
			p.barrier(1 << 40)
			for id := range cancelled {
				g := p.read(func(g Frame) bool { return g.Type == TypeRes && g.ID == id })
				if g.Error == nil || g.Error.Code != CodeCanceled {
					t.Fatalf("a cancelled request answered %+v", g)
				}
			}
			p.barrier(1<<40 + 1)
			for _, g := range p.extra {
				if !cancelled[g.ID] || g.Type != TypePush {
					b, _ := json.Marshal(g)
					t.Fatalf("an extra frame %s", b)
				}
			}
			c.Close()
			calls.Wait()
		})
	}
}
