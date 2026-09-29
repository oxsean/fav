package wire

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// StreamQueue is how many bytes of pushes a stream holds before Full applies.
const StreamQueue = 1 << 20

// Full is what a stream does with a push that does not fit its queue.
type Full int

const (
	FullLag    Full = iota // drop what is queued and end the stream as lagged: the opener starts again from its cursor
	FullGap                // drop the queued pushes of the same method and this one; Push returns a *GapError
	FullLatest             // drop the queued pushes of the same method and queue this one
)

type StreamOptions struct {
	Class Class
	Full  Full
	Queue int // bytes; default StreamQueue
}

// GapError is Push's answer when FullGap dropped pushes: Mark is the mark of the first one dropped (this push's when
// none was queued). The stream stays open; its provider tells the opener what it lost.
type GapError struct {
	Mark any
}

func (e *GapError) Error() string { return "gap" }

// Stream is the providing end of a stream: pushes carrying the id of the request that opened it, until End.
type Stream struct {
	c      *Conn
	id     int64
	opt    StreamOptions
	ctx    context.Context
	cancel context.CancelFunc
	lane   *lane
	ended  bool   // under c.wmx
	why    *Error // why Push fails once ended
}

// Stream turns r into a stream: the handler returns (its result is ignored, an error ends the stream) and the stream
// stays open until End, the opener's cancel or the connection's end. A stream takes no handler slot; the handler
// holds one until it returns, so the pushing is done on another goroutine. Past Options.MaxStreams open streams the
// answer is busy.
func (r *Request) Stream(opt StreamOptions) (*Stream, error) {
	if r.stream != nil {
		return r.stream, nil
	}
	c := r.Conn
	if c == nil || r.id == 0 {
		return nil, &Error{Code: CodeUnsupported, Detail: r.Method}
	}
	if opt.Queue <= 0 {
		opt.Queue = StreamQueue
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Stream{c: c, id: r.id, opt: opt, ctx: ctx, cancel: cancel, lane: &lane{class: opt.Class}}
	c.mu.Lock()
	switch {
	case c.err != nil:
		err := c.err
		c.mu.Unlock()
		cancel()
		return nil, err
	case r.ctx != nil && r.ctx.Err() != nil: // the opener cancelled before the stream was there to hear it
		c.mu.Unlock()
		cancel()
		return nil, &Error{Code: CodeCanceled}
	case len(c.streams) >= c.opt.MaxStreams:
		c.mu.Unlock()
		cancel()
		return nil, &Error{Code: CodeBusy, Detail: "streams"}
	}
	c.streams[r.id] = s
	c.mu.Unlock()
	r.stream = s
	return s, nil
}

// Context ends when the stream does.
func (s *Stream) Context() context.Context { return s.ctx }

// Push queues one push; it does not wait for the write. It fails once the stream has ended.
func (s *Stream) Push(method string, params any) error { return s.PushMark(method, params, nil) }

func (s *Stream) frame(method string, params any) ([]byte, error) {
	p, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return encode(&Frame{Type: TypePush, ID: s.id, Method: method, Params: p})
}

// PushWait is Push for a provider that can wait: while the queue has no room it waits (the other end reads slowly)
// instead of applying Full.
func (s *Stream) PushWait(ctx context.Context, method string, params any) error {
	b, err := s.frame(method, params)
	if err != nil {
		return err
	}
	c, l := s.c, s.lane
	for {
		c.wmx.Lock()
		if s.ended {
			c.wmx.Unlock()
			return s.why
		}
		if l.bytes == 0 || l.bytes+len(b) <= s.opt.Queue {
			ok := c.putLocked(l, &pending{b: b, method: method})
			c.wmx.Unlock()
			if !ok {
				return c.Err()
			}
			c.wake()
			return nil
		}
		if l.room == nil {
			l.room = make(chan struct{}, 1)
		}
		room := l.room
		c.wmx.Unlock()
		select {
		case <-room:
		case <-s.ctx.Done():
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// PushMark is Push with a mark that a GapError hands back when this push is dropped.
func (s *Stream) PushMark(method string, params any, mark any) error {
	b, err := s.frame(method, params)
	if err != nil {
		return err
	}
	c, l := s.c, s.lane
	c.wmx.Lock()
	if s.ended {
		c.wmx.Unlock()
		return s.why
	}
	if l.bytes > 0 && l.bytes+len(b) > s.opt.Queue {
		switch s.opt.Full {
		case FullLag:
			l.dropAll()
			c.wmx.Unlock()
			lag := &Error{Code: CodeLagged}
			s.End(nil, lag)
			return lag
		case FullGap:
			m, found := l.dropMethod(method)
			c.wmx.Unlock()
			if !found {
				m = mark
			}
			return &GapError{Mark: m}
		case FullLatest:
			l.dropMethod(method)
		}
	}
	ok := c.putLocked(l, &pending{b: b, method: method, mark: mark})
	c.wmx.Unlock()
	if !ok {
		return c.Err()
	}
	c.wake()
	return nil
}

// End sends the stream's last frame: result (EndDone when the provider is finished), or err. Later calls do nothing.
func (s *Stream) End(result any, err error) {
	f := &Frame{Type: TypeRes, ID: s.id}
	if err != nil {
		f.Error = asError(context.Background(), err)
	} else if result != nil {
		b, merr := json.Marshal(result)
		if merr != nil {
			f.Error = &Error{Code: CodeInternal, Detail: merr.Error()}
		} else {
			f.Result = b
		}
	}
	b, eerr := encode(f)
	if eerr != nil {
		b, _ = encode(&Frame{Type: TypeRes, ID: s.id, Error: &Error{Code: CodeInternal, Detail: eerr.Error()}})
	}
	c := s.c
	c.wmx.Lock()
	if s.ended {
		c.wmx.Unlock()
		return
	}
	s.ended = true
	if s.why = f.Error; s.why == nil {
		s.why = &Error{Code: CodeCanceled, Detail: "ended"}
	}
	ok := c.putLocked(s.lane, &pending{b: b})
	c.wmx.Unlock()
	if ok {
		c.wake()
	}
	c.mu.Lock()
	if c.streams[s.id] == s {
		delete(c.streams, s.id)
	}
	c.mu.Unlock()
	s.cancel()
}

// abort drops what is queued and ends the stream with why: the opener cancelled it.
func (s *Stream) abort(why *Error) {
	s.c.wmx.Lock()
	if !s.ended {
		s.lane.dropAll()
	}
	s.c.wmx.Unlock()
	s.End(nil, why)
}

// drop ends the stream without a last frame: the connection is gone.
func (s *Stream) drop(why *Error) {
	s.c.wmx.Lock()
	if !s.ended {
		s.ended, s.why = true, why
		s.lane.dropAll()
	}
	s.c.wmx.Unlock()
	s.cancel()
}

// Push is one push a Watch received.
type Push struct {
	Method string
	Params json.RawMessage
}

// Decode reads p's params into v.
func (p Push) Decode(v any) error {
	if err := json.Unmarshal(p.Params, v); err != nil {
		return &Error{Code: CodeBadRequest, Detail: fmt.Sprintf("%s: %v", p.Method, err)}
	}
	return nil
}

// Watch is the opening end of a stream.
type Watch struct {
	c      *Conn
	id     int64
	q      chan Push
	done   chan struct{}
	once   sync.Once
	err    error
	result json.RawMessage
	local  bool // ended on this side: what is queued is not handed out
	stop   func() bool
}

// Watch opens a stream: the receiver is registered before the request goes out, so no push finds no one. When ctx
// ends the stream is cancelled. A reader that lets Options.WatchQueue pushes wait ends it as lagged.
func (c *Conn) Watch(ctx context.Context, method string, params any) *Watch {
	w := &Watch{c: c, id: c.next.Add(1), q: make(chan Push, c.opt.WatchQueue), done: make(chan struct{})}
	f := &Frame{Type: TypeReq, ID: w.id, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			w.end(&Error{Code: CodeBadRequest, Detail: err.Error()}, nil, true)
			return w
		}
		f.Params = b
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		w.end(err, nil, true)
		return w
	}
	c.watches[w.id] = w
	c.mu.Unlock()
	if err := c.send(ctx, f); err != nil {
		if w.end(err, nil, true) && Code(err) == CodeTimeout {
			c.post(c.ctl, &pending{b: cancelFrame(w.id)})
		}
		return w
	}
	stop := context.AfterFunc(ctx, w.Cancel)
	c.mu.Lock()
	w.stop = stop
	c.mu.Unlock()
	select {
	case <-w.done:
		stop()
	default:
	}
	return w
}

func cancelFrame(id int64) []byte {
	b, _ := encode(&Frame{Type: TypeCancel, ID: id})
	return b
}

// Next is the next push, a queued one even when ctx has ended; once the stream has ended, io.EOF when its provider
// finished it, else why it ended.
func (w *Watch) Next(ctx context.Context) (Push, error) {
	select {
	case p := <-w.q:
		if w.dropped() {
			return Push{}, w.Err()
		}
		return p, nil
	default:
	}
	select {
	case p := <-w.q:
		if w.dropped() {
			return Push{}, w.Err()
		}
		return p, nil
	case <-w.done:
		if !w.local {
			select {
			case p := <-w.q:
				return p, nil
			default:
			}
		}
		if w.err == nil {
			return Push{}, io.EOF
		}
		return Push{}, w.err
	case <-ctx.Done():
		return Push{}, ctx.Err()
	}
}

func (w *Watch) dropped() bool {
	select {
	case <-w.done:
		return w.local
	default:
		return false
	}
}

// Done is closed when the stream has ended.
func (w *Watch) Done() <-chan struct{} { return w.done }

// Err is why the stream ended: nil while it is open and when its provider finished it.
func (w *Watch) Err() error {
	select {
	case <-w.done:
		return w.err
	default:
		return nil
	}
}

// Result decodes the result the provider finished the stream with.
func (w *Watch) Result(out any) error {
	<-w.done
	if w.err != nil {
		return w.err
	}
	if len(w.result) == 0 || out == nil {
		return nil
	}
	return json.Unmarshal(w.result, out)
}

// Cancel ends the stream on both ends; later pushes are dropped.
func (w *Watch) Cancel() {
	if w.end(&Error{Code: CodeCanceled}, nil, true) {
		w.c.post(w.c.ctl, &pending{b: cancelFrame(w.id)})
	}
}

// take queues a push on the reading goroutine; a full queue ends the stream as lagged.
func (w *Watch) take(p Push) {
	select {
	case w.q <- p:
	default:
		if w.end(&Error{Code: CodeLagged}, nil, true) {
			w.c.post(w.c.ctl, &pending{b: cancelFrame(w.id)})
		}
	}
}

// end ends w once: err nil means the provider finished it with result. local: ended on this side.
func (w *Watch) end(err error, result json.RawMessage, local bool) bool {
	ended := false
	w.once.Do(func() {
		ended = true
		if e, ok := err.(*Error); ok && e == nil {
			err = nil
		}
		w.err, w.result, w.local = err, result, local
		w.c.mu.Lock()
		if w.c.watches[w.id] == w {
			delete(w.c.watches, w.id)
		}
		stop := w.stop
		w.c.mu.Unlock()
		close(w.done)
		if stop != nil {
			stop()
		}
	})
	return ended
}
