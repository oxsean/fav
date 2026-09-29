package coord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/task"

	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/wire"
)

type OutputPageParams struct {
	Run    string `json:"run"`
	Before int64  `json:"before"` // < 0: from the end
	N      int    `json:"n,omitempty"`
	Raw    bool   `json:"raw,omitempty"`
	File   string `json:"file,omitempty"`
}

// OutputPage is a stretch of a run's output as events: From is where its first line starts, which the page before
// ends at; To where its last whole line ends; Earliest the first position still to be read. Raw is the stretch's text
// as written, when asked for.
type OutputPage struct {
	Events   []output.Event `json:"events"`
	From     int64          `json:"from"`
	To       int64          `json:"to"`
	Earliest int64          `json:"earliest"`
	File     string         `json:"file,omitempty"`
	Prev     string         `json:"prev,omitempty"` // the page starts the current log: the log before it, still kept
	Turn     int            `json:"turn,omitempty"` // the turn the page starts in, when the node's marks know it
	Raw      string         `json:"raw,omitempty"`
}

// A page reads the node's log outputChunk first, twice as much each time after, until it holds its events or
// outputMax.
var outputChunk = 64 << 10

const (
	outputMax       = 1 << 20
	outputEvents    = 200
	maxOutputEvents = 1000
)

func (c *Coord) outputPage(ctx context.Context, p Principal, r *wire.Request) (any, error) {
	var op OutputPageParams
	if err := r.Decode(&op); err != nil {
		return nil, err
	}
	run, err := c.readableRun(p, op.Run)
	if err != nil {
		return nil, err
	}
	page, _, _, err := c.page(ctx, run, op, map[string]bool{})
	return page, err
}

// page reads a page of run's output, the messages in it recorded in seen; it answers where a follow from the page's
// end goes on and the turns there.
func (c *Coord) page(ctx context.Context, run task.Run, op OutputPageParams, seen map[string]bool) (OutputPage, node.Cursor, output.State, error) {
	n := op.N
	if n <= 0 {
		n = outputEvents
	}
	n = min(n, maxOutputEvents)
	var (
		lines   []node.TailLine
		marks   []output.Mark
		marksTo int64
		from    int64
		file    = op.File
		prev    string
		st      *output.State
		evs     []output.Event
		to      int64
		end     output.State
		size    int
	)
	for before, chunk := op.Before, outputChunk; ; chunk *= 2 {
		var t node.Tail
		if err := c.call(ctx, run.Machine, node.MRunTail, node.TailParams{Run: op.Run, Before: before, Max: chunk, File: file, Clip: !op.Raw}, &t); err != nil {
			return OutputPage{}, node.Cursor{}, output.State{}, err
		}
		got := t.Lines
		if got == nil { // raw, or a node that sends only text
			got = textLines(t.Text, t.From)
		}
		for _, l := range got {
			size += len(l.Text)
		}
		if marksTo == 0 {
			marksTo = t.MarksTo
		}
		stuck := before >= 0 && t.From >= before
		lines, marks, from, file, prev, st = append(got, lines...), append(t.Marks, marks...), t.From, t.File, t.Prev, t.Turn
		evs, to, end = eventsOf(file, lines, st)
		if whole(evs) >= n || t.Done || size >= outputMax || stuck {
			break
		}
		before = t.From
	}
	first := 1
	if st != nil {
		first = max(st.Turn, 1)
	}
	c.mu.Lock()
	said := c.said(run.ID)
	c.mu.Unlock()
	evs = output.Join(evs, marks, first, said, seen)
	if extra := whole(evs) - n; extra > 0 { // the last n, and the rest of the first one's line
		k := 0
		for ; extra > 0; k++ {
			if !evs[k].Temp {
				extra--
			}
		}
		for k > 0 && evs[k-1].Off == evs[k].Off {
			k--
		}
		evs = evs[k:]
	}
	pageFrom := from
	if len(evs) > 0 && !evs[0].Temp {
		pageFrom = evs[0].Off
	}
	page := OutputPage{Events: evs, From: pageFrom, To: max(to, pageFrom), File: file}
	if pageFrom == 0 {
		page.Prev = prev
	}
	if st == nil {
		for i := range evs {
			evs[i].Turn = 0 // ⚠️ without the node's marks a page cannot know its turns
		}
	} else if len(evs) > 0 {
		page.Turn = evs[0].Turn
	}
	if op.Raw {
		var b strings.Builder
		for _, l := range lines {
			if l.Off >= pageFrom {
				b.WriteString(l.Text)
			}
		}
		page.Raw = b.String()
	}
	return page, node.Cursor{File: file, Off: page.To, Marks: marksTo}, end, nil
}

// said is what the journal holds of run id's inputs, as copies; the caller holds mu.
func (c *Coord) said(id string) output.Said {
	var sends []agent.Send
	var ask task.RunInterrupt
	if r := c.st.Runs[id]; r != nil {
		sends = slices.Clone(r.Sends)
		if r.Interrupt != nil {
			ask = *r.Interrupt
		}
	}
	answers := maps.Clone(c.answered[id])
	return output.Said{
		Send: func(echo string) (agent.Send, bool) {
			for _, s := range sends {
				if s.ID == echo || node.UUIDFor(id, s.ID) == echo {
					return s, true
				}
			}
			return agent.Send{}, false
		},
		Answer: func(request string) agent.Answer { return answers[request] },
		Interrupt: func(id string) string {
			if ask.Ask == id {
				return ask.By
			}
			return ""
		},
	}
}

// textLines is text, read from off, as a page's lines.
func textLines(text string, off int64) []node.TailLine {
	var out []node.TailLine
	for text != "" {
		line, rest, whole := strings.Cut(text, "\n")
		if whole {
			line += "\n"
		}
		out = append(out, node.TailLine{Off: off, Text: line})
		off, text = off+int64(len(line)), rest
	}
	return out
}

// eventsOf reads a page's lines from st on (nil: unknown, read as the first turn); it answers the events, where the
// last whole line ends and the turns there. A line the node cut is marked truncated, and one sent as only its head is
// raw; a followed line's events are at when it was read, unless the line says.
func eventsOf(file string, lines []node.TailLine, st *output.State) ([]output.Event, int64, output.State) {
	var s output.State
	if st != nil {
		s = *st
	}
	var evs []output.Event
	var to int64
	if len(lines) > 0 {
		to = lines[0].Off
	}
	for _, l := range lines {
		if l.Head {
			evs = append(evs, output.Event{ID: file + ":" + strconv.FormatInt(l.Off, 10) + ":0", Off: l.Off, Kind: output.KindRaw,
				Text: l.Text, Turn: max(s.Turn, 1), Truncated: map[string]int{"line": int(l.Size)}, At: atOf(l.At)})
			to = l.Off + l.Size
			continue
		}
		e, next, end := output.Parse(file, l.Off, l.Text, s)
		if l.Size > 0 {
			for i := range e {
				if e[i].Truncated == nil {
					e[i].Truncated = map[string]int{}
				}
				e[i].Truncated["line"] = int(l.Size)
			}
			if end > l.Off {
				end = l.Off + l.Size
			}
		}
		if end > l.Off {
			to = end
		}
		for i := range e {
			if e[i].At == "" {
				e[i].At = atOf(l.At)
			}
		}
		s, evs = next, append(evs, e...)
	}
	return evs, to, s
}

// atOf is a followed line's time as events carry it.
func atOf(at string) string {
	if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
		return t.Format(time.RFC3339)
	}
	return ""
}

func whole(evs []output.Event) int {
	n := 0
	for _, e := range evs {
		if !e.Temp {
			n++
		}
	}
	return n
}

// OutputWatchParams opens run.output.watch: From is the cursor of the last push a copy applied (nil: the run's last
// page first).
type OutputWatchParams struct {
	Run  string       `json:"run"`
	From *node.Cursor `json:"from,omitempty"`
}

// OutputPush is one push of run.output.watch: events, and where they bring the watcher (none: temp events only).
type OutputPush struct {
	Events []output.Event `json:"events"`
	Cursor *node.Cursor   `json:"cursor,omitempty"`
}

// hub is the one follow of a run's output on its node, read once for everyone watching it: the last page first, then
// what the node pushes, parsed once, in batches every outputBatch, each encoded once for all its watchers. It keeps
// the batches of the last hubEvents events or hubBytes bytes for watchers that come later or open again.
type hub struct {
	c    *Coord
	run  string
	mu   sync.Mutex
	subs map[*outSub]bool
	// what went out: batches from start on, the last one ending at at
	batches []outBatch
	start   node.Cursor
	at      node.Cursor
	events  int
	bytes   int
	// what comes in: parsed, not yet sent
	pending []output.Event
	cursor  *node.Cursor // where pending brings a watcher
	turns   output.State
	seen    map[string]bool // the messages shown as you
	temps   map[string]bool // keys of temp events sent: the whole line that comes after takes the key
	ready   chan struct{}   // the last page is in, or err
	err     error
	ended   bool
	stop    context.CancelFunc
	linger  *time.Timer
}

type outBatch struct {
	from, to node.Cursor
	raw      json.RawMessage // the OutputPush, encoded
	n        int
}

type outSub struct {
	s   *wire.Stream
	p   Principal
	gap *node.Cursor // pushes were dropped from here on: the next batch comes after a gap event
}

// watchOutput opens a run output stream on r for p.
func (c *Coord) watchOutput(p Principal, r *wire.Request) (any, error) {
	var wp OutputWatchParams
	if err := r.Decode(&wp); err != nil {
		return nil, err
	}
	if _, err := c.readableRun(p, wp.Run); err != nil {
		return nil, err
	}
	c.mu.Lock()
	h := c.outs[wp.Run]
	if h == nil && len(c.outs) >= maxHubs {
		c.mu.Unlock()
		return nil, &wire.Error{Code: wire.CodeBusy, Detail: "outputs"}
	}
	s, err := r.Stream(wire.StreamOptions{Class: wire.ClassStream, Full: wire.FullGap})
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if h == nil {
		ctx, stop := context.WithCancel(context.Background())
		h = &hub{c: c, run: wp.Run, subs: map[*outSub]bool{}, seen: map[string]bool{}, temps: map[string]bool{},
			ready: make(chan struct{}), stop: stop}
		c.outs[wp.Run] = h
		go h.follow(ctx)
	}
	h.hold()
	c.mu.Unlock()
	sb := &outSub{s: s, p: p}
	go h.serve(sb, wp.From)
	return nil, nil
}

// hold keeps h while a watcher comes; the caller holds c.mu.
func (h *hub) hold() {
	h.mu.Lock()
	if h.linger != nil {
		h.linger.Stop()
		h.linger = nil
	}
	h.mu.Unlock()
}

// serve sends sb what the hub holds from from on, then keeps it among the watchers until its stream ends.
func (h *hub) serve(sb *outSub, from *node.Cursor) {
	select {
	case <-h.ready:
	case <-sb.s.Context().Done():
		h.leave(sb)
		return
	}
	h.mu.Lock()
	if h.err != nil {
		h.mu.Unlock()
		sb.s.End(nil, h.err)
		h.leave(sb)
		return
	}
	open, i := h.openAt(from)
	sb.s.Push(wire.PushOpen, open)
	for _, b := range h.batches[i:] {
		h.send(sb, b)
	}
	if h.ended && len(h.pending) == 0 {
		h.mu.Unlock()
		sb.s.End(wire.EndDone, nil)
		return
	}
	h.subs[sb] = true
	h.mu.Unlock()
	<-sb.s.Context().Done()
	h.leave(sb)
}

// openAt is the open push for a watcher coming from from, and the first batch it is sent; the caller holds mu.
func (h *hub) openAt(from *node.Cursor) (wire.Open, int) {
	start := output.Pos{File: h.start.File, Off: h.start.Off}
	switch {
	case from == nil || *from == h.start:
		return wire.Open{Mode: wire.ModeResume, Cursor: h.start}, 0
	case from.File == h.at.File && from.Off == h.at.Off:
		return wire.Open{Mode: wire.ModeResume, Cursor: *from}, len(h.batches)
	}
	for i, b := range h.batches {
		if b.to == *from || b.to.File == from.File && b.to.Off == from.Off {
			return wire.Open{Mode: wire.ModeResume, Cursor: *from}, i + 1
		}
	}
	return wire.Open{Mode: wire.ModeGap, Cursor: h.start, From: output.Pos{File: from.File, Off: from.Off}, To: start}, 0
}

// send pushes b to sb, after a gap event when pushes to it were dropped; the caller holds mu.
func (h *hub) send(sb *outSub, b outBatch) {
	if sb.gap != nil {
		g := OutputPush{Events: []output.Event{{Kind: output.KindGap, From: &output.Pos{File: sb.gap.File, Off: sb.gap.Off},
			To: &output.Pos{File: b.from.File, Off: b.from.Off}}}}
		raw, _ := json.Marshal(g)
		if err := sb.s.PushRaw(PushOutput, raw, *sb.gap); err != nil {
			h.dropped(sb, err)
			return
		}
		sb.gap = nil
	}
	h.dropped(sb, sb.s.PushRaw(PushOutput, b.raw, b.from))
}

func (h *hub) dropped(sb *outSub, err error) {
	var gap *wire.GapError
	if errors.As(err, &gap) && sb.gap == nil {
		at := gap.Mark.(node.Cursor)
		sb.gap = &at
	}
}

// leave drops sb; the last one to go lets the hub linger for hubLinger.
func (h *hub) leave(sb *outSub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, sb)
	if len(h.subs) == 0 && h.linger == nil && !h.ended {
		h.linger = time.AfterFunc(hubLinger, h.close)
	}
}

// close ends the follow, unless a watcher came back meanwhile.
func (h *hub) close() {
	c := h.c
	c.mu.Lock()
	h.mu.Lock()
	idle := len(h.subs) == 0 && h.linger != nil
	if idle && c.outs[h.run] == h {
		delete(c.outs, h.run)
	}
	h.mu.Unlock()
	c.mu.Unlock()
	if idle {
		h.stop()
	}
}

// follow reads the run's last page, then follows its output on the node until the run ended or the node went.
func (h *hub) follow(ctx context.Context) {
	c := h.c
	defer h.stop()
	w, err := h.begin(ctx)
	if err != nil {
		h.finish(err)
		return
	}
	defer w.Cancel()
	t := time.NewTicker(outputBatch)
	defer t.Stop()
	pushes := make(chan wire.Push)
	ended := make(chan error, 1)
	go func() {
		for {
			p, err := w.Next(ctx)
			if err != nil {
				ended <- err
				return
			}
			select {
			case pushes <- p:
			case <-ctx.Done():
				ended <- ctx.Err()
				return
			}
		}
	}()
	for {
		select {
		case p := <-pushes:
			if p.Method != node.PushFollow {
				continue
			}
			var fl node.Follow
			if p.Decode(&fl) != nil {
				continue
			}
			c.mu.Lock()
			said := c.said(h.run)
			if m := c.ms[c.machineOf(h.run)]; m != nil {
				m.busyAt = time.Now()
			}
			c.mu.Unlock()
			h.mu.Lock()
			h.take(fl, said)
			h.mu.Unlock()
		case <-t.C:
			h.mu.Lock()
			h.flush()
			h.mu.Unlock()
		case err := <-ended:
			if errors.Is(err, io.EOF) {
				err = nil
			}
			h.finish(err)
			return
		}
	}
}

func (c *Coord) machineOf(run string) string {
	if r := c.st.Runs[run]; r != nil {
		return r.Machine
	}
	return ""
}

// begin reads the last page into the first batch and opens the follow on the node from its end.
func (h *hub) begin(ctx context.Context) (*wire.Watch, error) {
	c := h.c
	c.mu.Lock()
	r := c.st.Runs[h.run]
	var run task.Run
	if r != nil {
		run = *r
	}
	c.mu.Unlock()
	pctx, cancel := context.WithTimeout(ctx, callWait)
	defer cancel()
	page, end, turns, err := c.page(pctx, run, OutputPageParams{Run: h.run, Before: -1}, h.seen)
	if err != nil {
		return nil, gone(err)
	}
	conn, err := c.nodeConn(ctx, run.Machine)
	if err != nil {
		return nil, gone(err)
	}
	w := conn.Watch(ctx, node.MRunFollow, node.FollowParams{Run: h.run, From: end})
	h.mu.Lock()
	h.start = node.Cursor{File: page.File, Off: page.From, Marks: end.Marks}
	h.at, h.turns, h.pending, h.cursor = h.start, turns, page.Events, &end
	for _, e := range page.Events {
		if e.Temp {
			h.temps[e.Key] = true
		}
	}
	h.flush()
	h.mu.Unlock()
	close(h.ready)
	return w, nil
}

// watcher is a node connection that streams.
type watcher interface {
	Watch(ctx context.Context, method string, params any) *wire.Watch
}

// nodeConn is machine name's connection when its node follows output; it connects first when needed.
func (c *Coord) nodeConn(ctx context.Context, name string) (watcher, error) {
	for {
		c.mu.Lock()
		m := c.ms[name]
		if m == nil {
			c.mu.Unlock()
			return nil, notFound("machine " + name)
		}
		m.busyAt = time.Now()
		c.ensure(m)
		conn, dialing, err, local := m.conn, m.dialing, m.err, m.host == nil && !m.attached
		follows := local || slices.Contains(m.hello.Methods, node.MRunFollow)
		c.mu.Unlock()
		if conn != nil {
			w, ok := conn.(watcher)
			if !ok || !follows {
				return nil, &wire.Error{Code: wire.CodeUnsupported, Detail: node.MRunFollow}
			}
			return w, nil
		}
		if !dialing {
			if err == nil {
				err = &wire.Error{Code: wire.CodeOffline}
			}
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, &wire.Error{Code: wire.CodeTimeout, Detail: node.MRunFollow}
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// gone is err as a watcher hears it: a node out of reach, or a run it no longer has, is gone.
func gone(err error) error {
	switch wire.Code(err) {
	case wire.CodeClosed, wire.CodeOffline, wire.CodeTimeout, wire.CodeNotFound, wire.CodeAuth, wire.CodeHostKey, wire.CodeNoTend, wire.CodeProto:
		return &wire.Error{Code: wire.CodeGone, Detail: wire.Code(err)}
	}
	return err
}

// take parses one push of the node; the caller holds mu.
func (h *hub) take(fl node.Follow, said output.Said) {
	var evs []output.Event
	if g := fl.Gap; g != nil {
		evs = append(evs, output.Event{Kind: output.KindGap, From: &output.Pos{File: g.From.File, Off: g.From.Off},
			To: &output.Pos{File: g.To.File, Off: g.To.Off}})
	}
	if len(fl.Lines) > 0 || len(fl.Marks) > 0 {
		st := h.turns
		lines, _, next := eventsOf(fl.File, fl.Lines, &st)
		if len(fl.Lines) > 0 {
			h.turns = next
		}
		for i := range lines {
			if k := fl.File + ":" + strconv.FormatInt(lines[i].Off, 10); h.temps[k] && (i == 0 || lines[i-1].Off != lines[i].Off) {
				lines[i].Key = k
				delete(h.temps, k)
			}
		}
		evs = append(evs, output.Join(lines, fl.Marks, max(st.Turn, 1), said, h.seen)...)
	}
	if p := fl.Part; p != nil {
		part, _, _ := output.Parse(fl.File, p.Off, p.Text, h.turns)
		for _, e := range part {
			if e.Temp {
				h.temps[e.Key] = true
				evs = append(evs, e)
			}
		}
	}
	h.pending = append(h.pending, evs...)
	cursor := fl.Cursor
	h.cursor = &cursor
}

// flush sends what came since the last batch, in pushes of at most maxOutputPush bytes; the caller holds mu.
func (h *hub) flush() {
	if h.cursor == nil {
		return
	}
	for len(h.pending) > 0 || h.cursor != nil {
		push, rest := OutputPush{}, h.pending
		size := 0
		for len(rest) > 0 {
			b, _ := json.Marshal(rest[0])
			if size > 0 && size+len(b) > maxOutputPush {
				break
			}
			push.Events, size, rest = append(push.Events, rest[0]), size+len(b), rest[1:]
		}
		if push.Events == nil {
			push.Events = []output.Event{}
		}
		h.pending = rest
		b := outBatch{from: h.at, n: len(push.Events)}
		if len(rest) == 0 {
			push.Cursor, h.at, h.cursor = h.cursor, *h.cursor, nil
		}
		b.to = h.at
		b.raw, _ = json.Marshal(push)
		h.keep(b)
		for sb := range h.subs {
			h.send(sb, b)
		}
	}
}

// keep adds b to the batches kept, dropping the oldest past hubEvents or hubBytes; the caller holds mu.
func (h *hub) keep(b outBatch) {
	h.batches = append(h.batches, b)
	h.events, h.bytes = h.events+b.n, h.bytes+len(b.raw)
	for len(h.batches) > 1 && (h.events > hubEvents || h.bytes > hubBytes) {
		old := h.batches[0]
		h.batches = h.batches[1:]
		h.events, h.bytes, h.start = h.events-old.n, h.bytes-len(old.raw), old.to
	}
}

// finish ends every watcher's stream: done once the run ended and all of it went out, else why the follow ended.
func (h *hub) finish(err error) {
	c := h.c
	c.mu.Lock()
	if c.outs[h.run] == h {
		delete(c.outs, h.run)
	}
	c.mu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.flush()
	} else {
		err = gone(err)
		if wire.Code(err) == wire.CodeCanceled {
			err = &wire.Error{Code: wire.CodeGone, Detail: "follow"}
		}
	}
	h.ended, h.err = true, err
	select {
	case <-h.ready:
	default:
		close(h.ready)
	}
	for sb := range h.subs {
		sb.s.End(wire.EndDone, err)
		delete(h.subs, sb)
	}
}

// recheckOutputs ends the output streams of watchers who may no longer read their run; the caller holds mu.
func (c *Coord) recheckOutputs() {
	for id, h := range c.outs {
		run := c.st.Runs[id]
		h.mu.Lock()
		for sb := range h.subs {
			if !canRead(c.st, sb.p, runTask(c.st, run)) {
				sb.s.End(nil, &wire.Error{Code: wire.CodeUnauthorized})
				delete(h.subs, sb)
			}
		}
		h.mu.Unlock()
	}
}
