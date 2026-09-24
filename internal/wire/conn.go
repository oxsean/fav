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
	// OnPush gets one-way messages on the reading goroutine: it must not block.
	OnPush func(method string, params json.RawMessage)
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
	opt    Options
	rw     io.ReadWriteCloser
	wmu    chan struct{}
	sem    chan struct{}
	next   atomic.Int64
	seen   atomic.Int64 // unix nanos of the last frame read
	mu     sync.Mutex
	wait   map[int64]chan *Frame
	serve  map[int64]context.CancelFunc
	busy   sync.WaitGroup // requests being handled
	queued atomic.Int32   // requests waiting for a handler slot
	oob    chan struct{}  // answers sent outside the handler slots (pings, busy) being written
	done   chan struct{}
	err    *Error
	closed sync.Once
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
	c := &Conn{opt: opt, rw: rw, wmu: make(chan struct{}, 1), sem: make(chan struct{}, opt.MaxInflight),
		oob: make(chan struct{}, max(opt.MaxQueued, 64)), wait: map[int64]chan *Frame{}, serve: map[int64]context.CancelFunc{}, done: make(chan struct{})}
	c.seen.Store(time.Now().UnixNano())
	go c.read()
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
		serve := c.serve
		c.wait, c.serve = map[int64]chan *Frame{}, map[int64]context.CancelFunc{} // each waiting call sees done
		c.mu.Unlock()
		c.rw.Close()
		for _, cancel := range serve {
			cancel()
		}
		close(c.done)
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
	f := &Frame{Kind: KindReq, ID: c.next.Add(1), Method: method, CommandID: commandID}
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
			return err
		}
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
		c.sendQuiet(&Frame{Kind: KindCancel, ID: f.ID})
		return &Error{Code: CodeTimeout, Detail: method}
	}
}

func decodeResult(res *Frame, out any) error {
	if !res.OK {
		if res.Error == nil {
			return &Error{Code: CodeInternal}
		}
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

// Push sends a one-way message.
func (c *Conn) Push(method string, params any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.send(context.Background(), &Frame{Kind: KindPush, Method: method, Params: b})
}

// reply sends an answer that takes no handler slot; with MaxQueued of them already being written (the other end reads
// nothing) it is dropped and the caller's call times out.
func (c *Conn) reply(f *Frame) {
	select {
	case c.oob <- struct{}{}:
		go func() {
			defer func() { <-c.oob }()
			c.sendQuiet(f)
		}()
	default:
	}
}

func (c *Conn) sendQuiet(f *Frame) {
	ctx, cancel := context.WithTimeout(context.Background(), c.opt.WriteTimeout)
	defer cancel()
	c.send(ctx, f)
}

// send writes one frame. A write that does not finish in WriteTimeout ends the connection: half a line would break
// the stream.
func (c *Conn) send(ctx context.Context, f *Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(b) >= MaxFrame { // the other end would drop the connection
		if f.Kind != KindRes {
			return &Error{Code: CodeBadRequest, Detail: fmt.Sprintf("frame of %d bytes", len(b))}
		}
		b, _ = json.Marshal(&Frame{Kind: KindRes, ID: f.ID, Error: &Error{Code: CodeInternal, Detail: fmt.Sprintf("answer of %d bytes", len(b))}})
	}
	b = append(b, '\n')
	select {
	case c.wmu <- struct{}{}:
	case <-c.done:
		return c.Err()
	case <-ctx.Done():
		return &Error{Code: CodeTimeout, Detail: f.Method}
	}
	wrote := make(chan error, 1)
	go func() {
		_, err := c.rw.Write(b)
		wrote <- err
	}()
	t := time.NewTimer(c.opt.WriteTimeout)
	defer t.Stop()
	select {
	case err := <-wrote:
		<-c.wmu
		if err != nil {
			c.fail(&Error{Code: CodeClosed, Detail: err.Error()})
			return c.Err()
		}
		return nil
	case <-t.C:
		c.fail(&Error{Code: CodeTimeout, Detail: "write"})
		go func() { <-wrote; <-c.wmu }()
		return c.Err()
	case <-c.done:
		go func() { <-wrote; <-c.wmu }()
		return c.Err()
	}
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
	if f.Kind == "" { // proto 1 had no kind: its hello still gets an answer, so the older end can tell it is outdated
		switch {
		case f.Method != "":
			f.Kind = KindReq
		case f.ID != 0:
			f.Kind = KindRes
		}
	}
	switch f.Kind {
	case KindRes:
		c.mu.Lock()
		ch := c.wait[f.ID]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- &f:
			default:
			}
		}
	case KindReq:
		c.handle(&f)
	case KindPush:
		if c.opt.OnPush != nil {
			c.opt.OnPush(f.Method, f.Params)
		}
	case KindCancel:
		c.mu.Lock()
		cancel := c.serve[f.ID]
		c.mu.Unlock()
		if cancel != nil {
			cancel()
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
		c.reply(&Frame{Kind: KindRes, ID: f.ID, OK: true})
		return
	}
	if int(c.queued.Add(1)) > c.opt.MaxQueued {
		c.queued.Add(-1)
		c.reply(&Frame{Kind: KindRes, ID: f.ID, Error: &Error{Code: CodeBusy}})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
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
		if ctx.Err() != nil && c.Err() != nil {
			return
		}
		c.sendQuiet(res)
	}()
}

func (c *Conn) answer(ctx context.Context, f *Frame) (res *Frame) {
	res = &Frame{Kind: KindRes, ID: f.ID}
	defer func() {
		if p := recover(); p != nil {
			res.OK, res.Result, res.Error = false, nil, &Error{Code: CodeInternal, Detail: fmt.Sprint(p)}
		}
	}()
	if c.opt.Handler == nil {
		res.Error = &Error{Code: CodeUnknownMethod, Detail: f.Method}
		return res
	}
	out, err := c.opt.Handler(ctx, &Request{Method: f.Method, CommandID: f.CommandID, Params: f.Params, Conn: c})
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			e = &Error{Code: CodeInternal, Detail: err.Error()}
		}
		if ctx.Err() != nil && e.Code == CodeInternal {
			e = &Error{Code: CodeCanceled}
		}
		res.Error = e
		return res
	}
	b, err := json.Marshal(out)
	if err != nil {
		res.Error = &Error{Code: CodeInternal, Detail: err.Error()}
		return res
	}
	res.OK, res.Result = true, b
	return res
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
