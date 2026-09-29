package wire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Request is one call being handled.
type Request struct {
	Method    string
	CommandID string
	Params    json.RawMessage
	Conn      *Conn
	id        int64
	ctx       context.Context
	stream    *Stream
}

// Decode reads r's params into v; bad_request when they do not fit.
func (r *Request) Decode(v any) error {
	if len(r.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.Params, v); err != nil {
		return &Error{Code: CodeBadRequest, Detail: err.Error()}
	}
	return nil
}

// Handler answers requests; an error that is not an *Error goes back as internal.
type Handler func(ctx context.Context, req *Request) (any, error)

type Options struct {
	Handler Handler
	// OnPush gets one-way messages outside any stream on the reading goroutine: it must not block.
	OnPush func(method string, params json.RawMessage)
	// Bulk marks the methods whose answers are large pieces fetched on demand: they are written after everything else.
	Bulk func(method string) bool
	// MaxStreams bounds the streams this end serves at once (default 64); WatchQueue the pushes a Watch holds before
	// its reader falls behind (default 256).
	MaxStreams int
	WatchQueue int
	// MaxInflight bounds the requests handled at once; more wait their turn (default 16), and past MaxQueued waiting
	// ones the answer is busy (default 64).
	MaxInflight int
	MaxQueued   int
	// WriteTimeout ends the connection when a frame cannot be written in time: the other end stopped reading
	// (default 30 s).
	WriteTimeout time.Duration
	// Keepalive pings the other end after this much silence and ends the connection after twice as much (0: off).
	Keepalive time.Duration
	// OnClose runs once, after the connection ended and every waiting call returned.
	OnClose func(err error)
}

// Conn is one connection. Every method is safe for concurrent use.
type Conn struct {
	opt Options
	rw  io.ReadWriteCloser
	sched
	sem     chan struct{}
	next    atomic.Int64
	seen    atomic.Int64 // unix nanos of the last frame read
	mu      sync.Mutex
	wait    map[int64]chan *Frame
	serve   map[int64]context.CancelFunc
	streams map[int64]*Stream
	watches map[int64]*Watch
	busy    sync.WaitGroup // requests being handled
	queued  atomic.Int32   // requests waiting for a handler slot
	done    chan struct{}
	err     *Error
	closed  sync.Once
}

// New starts reading rw; Close (or the other end) ends it.
func New(rw io.ReadWriteCloser, opt Options) *Conn {
	if opt.MaxInflight <= 0 {
		opt.MaxInflight = 16
	}
	if opt.MaxQueued <= 0 {
		opt.MaxQueued = 64
	}
	if opt.WriteTimeout <= 0 {
		opt.WriteTimeout = 30 * time.Second
	}
	if opt.MaxStreams <= 0 {
		opt.MaxStreams = 64
	}
	if opt.WatchQueue <= 0 {
		opt.WatchQueue = 256
	}
	c := &Conn{opt: opt, rw: rw, sem: make(chan struct{}, opt.MaxInflight), wait: map[int64]chan *Frame{},
		serve: map[int64]context.CancelFunc{}, streams: map[int64]*Stream{}, watches: map[int64]*Watch{}, done: make(chan struct{})}
	c.sched.init(max(opt.MaxQueued, 64))
	c.seen.Store(time.Now().UnixNano())
	go c.read()
	go c.writer()
	go c.watchdog()
	if opt.Keepalive > 0 {
		go c.keepalive()
	}
	return c
}

// Done is closed when the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection ended, nil while it works.
func (c *Conn) Err() *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the connection; calls in flight return closed at once.
func (c *Conn) Close() error {
	c.fail(&Error{Code: CodeClosed})
	return nil
}

func (c *Conn) fail(why *Error) {
	c.closed.Do(func() {
		c.mu.Lock()
		c.err = why
		serve, streams, watches := c.serve, c.streams, c.watches
		c.wait, c.serve = map[int64]chan *Frame{}, map[int64]context.CancelFunc{} // each waiting call sees done
		c.streams, c.watches = map[int64]*Stream{}, map[int64]*Watch{}
		c.mu.Unlock()
		c.rw.Close()
		for _, cancel := range serve {
			cancel()
		}
		for _, s := range streams {
			s.drop(why)
		}
		close(c.done)
		for _, w := range watches {
			w.end(why, nil, false)
		}
		if c.opt.OnClose != nil {
			go c.opt.OnClose(why)
		}
	})
}

// Call sends method with params and decodes the result into out (nil: ignore it). When ctx ends first, the other end
// is told to cancel and the call returns timeout; the connection stays.
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	return c.CallCommand(ctx, method, "", params, out)
}

// CallCommand is Call for a write with an idempotency key.
func (c *Conn) CallCommand(ctx context.Context, method, commandID string, params, out any) error {
	f := &Frame{Type: TypeReq, ID: c.next.Add(1), Method: method, CommandID: commandID}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		f.Params = b
	}
	ch := make(chan *Frame, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.wait[f.ID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.wait, f.ID)
		c.mu.Unlock()
	}()
	if err := c.send(ctx, f); err != nil {
		select { // the other end may have answered and hung up while the write was being confirmed
		case res := <-ch:
			return decodeResult(res, out)
		default:
		}
		if Code(err) == CodeTimeout && c.Err() == nil { // the request is queued or written: the cancel goes after it
			c.post(c.ctl, &pending{b: cancelFrame(f.ID)})
		}
		return err
	}
	select {
	case res := <-ch:
		return decodeResult(res, out)
	case <-c.done:
		select { // the answer may have come just before the end
		case res := <-ch:
			return decodeResult(res, out)
		default:
		}
		return c.Err()
	case <-ctx.Done():
		c.sendQuiet(&Frame{Type: TypeCancel, ID: f.ID})
		return &Error{Code: CodeTimeout, Detail: method}
	}
}

func decodeResult(res *Frame, out any) error {
	if res.Error != nil {
		return res.Error
	}
	if out == nil || len(res.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(res.Result, out); err != nil {
		return &Error{Code: CodeBadRequest, Detail: err.Error()}
	}
	return nil
}

// Push sends a one-way message outside any stream.
func (c *Conn) Push(method string, params any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.send(context.Background(), &Frame{Type: TypePush, Method: method, Params: b})
}

// reply sends an answer that takes no handler slot and is not waited for; with enough of them already waiting to be
// written (the other end reads nothing) it is dropped and the caller's call times out.
func (c *Conn) reply(f *Frame) {
	if b, err := encode(f); err == nil {
		c.post(c.ctl, &pending{b: b, oob: true})
	}
}

func (c *Conn) sendQuiet(f *Frame) {
	ctx, cancel := context.WithTimeout(context.Background(), c.opt.WriteTimeout)
	defer cancel()
	c.send(ctx, f)
}

// send queues one frame on the control lane and waits until it is written.
func (c *Conn) send(ctx context.Context, f *Frame) error { return c.sendOn(ctx, c.ctl, f) }

func (c *Conn) sendOn(ctx context.Context, l *lane, f *Frame) error {
	b, err := encode(f)
	if err != nil {
		return err
	}
	o := &pending{b: b, done: make(chan error, 1)}
	if !c.post(l, o) {
		return c.Err()
	}
	select {
	case err := <-o.done:
		return err
	case <-c.done:
		return c.Err()
	case <-ctx.Done():
		return &Error{Code: CodeTimeout, Detail: f.Method}
	}
}

// encode is f as one line. A frame the other end would drop the connection for is refused, and an answer that large
// becomes an internal error.
func encode(f *Frame) ([]byte, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	if len(b) >= MaxFrame {
		if f.Type != TypeRes {
			return nil, &Error{Code: CodeBadRequest, Detail: fmt.Sprintf("frame of %d bytes", len(b))}
		}
		b, _ = json.Marshal(&Frame{Type: TypeRes, ID: f.ID, Error: &Error{Code: CodeInternal, Detail: fmt.Sprintf("answer of %d bytes", len(b))}})
	}
	return append(b, '\n'), nil
}

func (c *Conn) read() {
	br := bufio.NewReaderSize(c.rw, 64<<10)
	for {
		line, err := readLine(br)
		if len(line) > 0 {
			c.seen.Store(time.Now().UnixNano())
			c.dispatch(bytes.ReplaceAll(line, []byte{0}, nil)) // wsl.exe may interleave UTF-16 NULs
		}
		if err != nil {
			why := &Error{Code: CodeClosed}
			if errors.Is(err, errTooLong) {
				why.Detail = err.Error()
			} else {
				c.drain()
			}
			c.fail(why)
			return
		}
	}
}

var errTooLong = fmt.Errorf("frame longer than %d bytes", MaxFrame)

// readLine returns one line without its newline, errTooLong past MaxFrame.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		part, err := br.ReadSlice('\n')
		if len(buf)+len(part) > MaxFrame {
			return nil, errTooLong
		}
		buf = append(buf, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(buf, "\r\n"), err
	}
}

func (c *Conn) dispatch(line []byte) {
	var f Frame
	if json.Unmarshal(line, &f) != nil { // noise before or between frames: a login banner, a shell warning
		return
	}
	switch f.Type {
	case TypeRes:
		c.mu.Lock()
		ch, w := c.wait[f.ID], c.watches[f.ID]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- &f:
			default:
			}
		} else if w != nil {
			w.end(f.Error, f.Result, false)
		}
	case TypeReq:
		c.handle(&f)
	case TypePush:
		if f.ID != 0 {
			c.mu.Lock()
			w := c.watches[f.ID]
			c.mu.Unlock()
			if w != nil {
				w.take(Push{Method: f.Method, Params: f.Params})
			}
		} else if c.opt.OnPush != nil {
			c.opt.OnPush(f.Method, f.Params)
		}
	case TypeCancel:
		c.mu.Lock()
		if cancel := c.serve[f.ID]; cancel != nil {
			cancel() // under mu: a handler opening its stream right now sees it (Request.Stream)
		}
		s := c.streams[f.ID]
		c.mu.Unlock()
		if s != nil {
			s.abort(&Error{Code: CodeCanceled})
		}
	}
}

// drainWait bounds how long requests already read may take to be answered after the other end stopped sending.
const drainWait = time.Minute

// drain lets the requests already read finish and answer: a caller may send its requests and close its side.
func (c *Conn) drain() {
	idle := make(chan struct{})
	go func() { c.busy.Wait(); close(idle) }()
	select {
	case <-idle:
	case <-c.done:
	case <-time.After(drainWait):
	}
}

func (c *Conn) handle(f *Frame) {
	if f.Method == MPing { // answered even when every handler slot is taken: it proves the connection, not the handlers
		c.reply(&Frame{Type: TypeRes, ID: f.ID})
		return
	}
	if int(c.queued.Add(1)) > c.opt.MaxQueued {
		c.queued.Add(-1)
		c.reply(&Frame{Type: TypeRes, ID: f.ID, Error: &Error{Code: CodeBusy}})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := c.ctl
	if c.opt.Bulk != nil && c.opt.Bulk(f.Method) {
		l = &lane{class: ClassBulk}
	}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		c.queued.Add(-1)
		cancel()
		return
	}
	c.serve[f.ID] = cancel
	c.busy.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.busy.Done()
		defer func() {
			cancel()
			c.mu.Lock()
			delete(c.serve, f.ID)
			c.mu.Unlock()
		}()
		select {
		case c.sem <- struct{}{}:
			c.queued.Add(-1)
		case <-ctx.Done():
			c.queued.Add(-1)
			return
		}
		defer func() { <-c.sem }() // held until the answer is written: a peer that does not read holds the slots
		res := c.answer(ctx, f)
		if res == nil || ctx.Err() != nil && c.Err() != nil {
			return
		}
		wctx, wcancel := context.WithTimeout(context.Background(), c.opt.WriteTimeout)
		defer wcancel()
		c.sendOn(wctx, l, res)
	}()
}

// answer is the res for f, nil when the handler opened a stream: the stream sends its own last res.
func (c *Conn) answer(ctx context.Context, f *Frame) (res *Frame) {
	res = &Frame{Type: TypeRes, ID: f.ID}
	r := &Request{Method: f.Method, CommandID: f.CommandID, Params: f.Params, Conn: c, id: f.ID, ctx: ctx}
	defer func() {
		if p := recover(); p != nil {
			res.Result, res.Error = nil, &Error{Code: CodeInternal, Detail: fmt.Sprint(p)}
		}
		if r.stream != nil {
			if res.Error != nil {
				r.stream.End(nil, res.Error)
			}
			res = nil
		}
	}()
	if c.opt.Handler == nil {
		res.Error = &Error{Code: CodeUnknownMethod, Detail: f.Method}
		return res
	}
	out, err := c.opt.Handler(ctx, r)
	if err != nil {
		res.Error = asError(ctx, err)
		return res
	}
	if r.stream != nil {
		return res
	}
	b, err := json.Marshal(out)
	if err != nil {
		res.Error = &Error{Code: CodeInternal, Detail: err.Error()}
		return res
	}
	res.Result = b
	return res
}

func asError(ctx context.Context, err error) *Error {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: CodeInternal, Detail: err.Error()}
	}
	if ctx.Err() != nil && e.Code == CodeInternal {
		e = &Error{Code: CodeCanceled}
	}
	return e
}

func (c *Conn) keepalive() {
	every := c.opt.Keepalive
	t := time.NewTicker(every / 2)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
		}
		quiet := time.Since(time.Unix(0, c.seen.Load()))
		switch {
		case quiet >= 2*every:
			c.fail(&Error{Code: CodeTimeout, Detail: "no frames"})
			return
		case quiet >= every:
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), every)
				defer cancel()
				c.Call(ctx, MPing, nil, nil)
			}()
		}
	}
}

// Pipe is two connected Conns in this process: tests, and a coordinator talking to its own machine's node.
func Pipe(a, b Options) (*Conn, *Conn) {
	x, y := net.Pipe()
	return New(x, a), New(y, b)
}
