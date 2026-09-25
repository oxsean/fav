package coord

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Conn is a connection to a node: in this process, an ssh child, or (mode 2) a node that dialed in.
type Conn interface {
	Call(ctx context.Context, method string, params, out any) error
	Done() <-chan struct{}
	Err() *wire.Error
	Close() error
}

const (
	passEvery    = 5 * time.Second
	callWait     = 20 * time.Second
	backoffFirst = 5 * time.Second
	backoffMax   = 5 * time.Minute
	idleClose    = 5 * time.Minute
	resendAfter  = 10 * time.Second
	nodeKeepAliv = 30 * time.Second
)

type machine struct {
	name     string
	host     *tend.Host // nil: this machine, or a node that dialed in
	conn     Conn
	hello    remote.Hello
	err      error
	backoff  time.Duration
	retryAt  time.Time
	dialing  bool
	attached bool // mode 2: the node dialed in; never dialed from here
	busyAt   time.Time
}

// machines are this machine and every configured host.
func (c *Coord) machines() {
	if c.opt.Remote {
		for _, n := range c.opt.Nodes {
			c.ms[n] = &machine{name: n, attached: true}
		}
		return
	}
	c.ms[Local] = &machine{name: Local, hello: remote.LocalHello(c.opt.Version)}
	for i := range c.opt.Config.Hosts {
		h := c.opt.Config.Hosts[i]
		c.ms[h.Name] = &machine{name: h.Name, host: &h}
	}
}

func (c *Coord) slots(name string) int {
	if m, ok := c.opt.Config.Machines[name]; ok && m.Slots > 0 {
		return m.Slots
	}
	return defaultSlots
}

func (c *Coord) nodeOptions() wire.Options {
	return wire.Options{
		OnPush: func(method string, _ json.RawMessage) {
			if method == node.MChanged {
				c.poke()
			}
		},
		OnClose:   func(error) { c.poke() },
		Keepalive: nodeKeepAliv,
	}
}

// ensure starts connecting m unless it is connected, connecting or waiting out its backoff; the caller holds mu.
func (c *Coord) ensure(m *machine) {
	if m.conn != nil || m.dialing || m.attached || time.Now().Before(m.retryAt) {
		return
	}
	m.dialing = true
	go c.dial(m)
}

func (c *Coord) dial(m *machine) {
	var conn Conn
	var err error
	switch {
	case m.host == nil:
		conn = c.local()
	case c.opt.Dial != nil:
		conn, err = c.opt.Dial(*m.host, c.nodeOptions())
	default:
		conn, err = remote.DialWith(*m.host, c.nodeOptions())
	}
	var h remote.Hello
	if err == nil {
		h, err = greet(conn)
		if err != nil {
			conn.Close()
		}
	}
	c.mu.Lock()
	m.dialing = false
	if err != nil {
		c.failed(m, err)
	} else {
		m.conn, m.hello, m.err, m.backoff = conn, h, nil, 0
	}
	c.mu.Unlock()
	c.poke()
}

// greet says hello and checks the node can run things.
func greet(conn Conn) (remote.Hello, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callWait)
	defer cancel()
	var h remote.Hello
	if err := conn.Call(ctx, remote.MHello, remote.HelloParams{Proto: wire.Proto, Role: "coordinator"}, &h); err != nil {
		return h, err
	}
	if h.Proto != wire.Proto || !slices.Contains(h.Methods, node.MRunStart) {
		return h, &wire.Error{Code: wire.CodeProto, Detail: h.Version}
	}
	return h, nil
}

// NodeOptions are the options of a connection to a node: its pushes wake the coordinator.
func (c *Coord) NodeOptions() wire.Options { return c.nodeOptions() }

// Attach makes conn machine name's connection (mode 2: the node dialed in); a connection it already had is closed.
func (c *Coord) Attach(name string, conn Conn) error {
	h, err := greet(conn)
	if err != nil {
		conn.Close()
		return err
	}
	c.mu.Lock()
	m := c.ms[name]
	if m == nil {
		m = &machine{name: name}
		c.ms[name] = m
	}
	if m.conn != nil {
		m.conn.Close()
	}
	m.attached, m.conn, m.hello, m.err = true, conn, h, nil
	c.mu.Unlock()
	c.poke()
	return nil
}

// local is this machine's node, served in this process.
func (c *Coord) local() Conn {
	a, b := wire.Pipe(c.nodeOptions(), wire.Options{Handler: c.opt.Node.Handler(c.opt.Sessions)})
	go c.opt.Node.Watch(b.Done(), func(runs []string) { b.Push(node.MChanged, node.Changed{Runs: runs}) })
	return a
}

// failed records why m cannot be reached and when to try again; the caller holds mu.
func (c *Coord) failed(m *machine, err error) {
	m.err = err
	m.backoff = min(max(m.backoff*2, backoffFirst), backoffMax)
	m.retryAt = time.Now().Add(m.backoff)
}

// lost drops conn after a call on it failed at the transport; the caller does not hold mu.
func (c *Coord) lost(m *machine, conn Conn, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m.conn == conn {
		conn.Close()
		m.conn = nil
		c.failed(m, err)
	}
}

// transport: the call may not have reached the node, or its answer may not have come back; retry later.
func transport(err error) bool {
	switch wire.Code(err) {
	case "", wire.CodeClosed, wire.CodeTimeout, wire.CodeCanceled, wire.CodeBusy, wire.CodeOffline, wire.CodeAuth,
		wire.CodeHostKey, wire.CodeNoTend, wire.CodeProto:
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// Run passes until ctx ends: at once, when a node reports a change or a command asks, and every passEvery.
func (c *Coord) Run(ctx context.Context) {
	t := time.NewTicker(passEvery)
	defer t.Stop()
	for {
		c.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		case <-t.C:
		}
	}
}

// Settle passes until the machines with work are reached (or given up on) and their runs dispatched; a one-shot
// coordinator (a CLI command) calls it before it lets go of the lock.
func (c *Coord) Settle(ctx context.Context) {
	for range 3 {
		c.Pass(ctx)
		if !c.waitDials(ctx) {
			return
		}
	}
}

func (c *Coord) waitDials(ctx context.Context) (waited bool) {
	for {
		c.mu.Lock()
		dialing := false
		for _, m := range c.ms {
			dialing = dialing || m.dialing
		}
		c.mu.Unlock()
		if !dialing {
			return waited
		}
		waited = true
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Pass converges once: connects the machines that have runs to see to, reads their runs, records what changed,
// repeats stops and lost starts, and dispatches queued runs.
func (c *Coord) Pass(ctx context.Context) {
	c.passMu.Lock()
	defer c.passMu.Unlock()
	if c.log.ReadOnly() != nil {
		return
	}
	now := time.Now()
	c.mu.Lock()
	work := map[string]bool{}
	for _, r := range c.st.Runs {
		if task.Open(r.State) || r.State == task.Abandoned && !c.ackDone[r.ID] { // until the node says it ended
			work[r.Machine] = true
		}
	}
	for name := range c.acked {
		work[name] = work[name] || len(c.acked[name]) > 0
	}
	var ready []*machine
	for _, m := range c.ms {
		if m.conn != nil {
			select {
			case <-m.conn.Done():
				err := error(m.conn.Err())
				m.conn = nil
				c.failed(m, err)
			default:
			}
		}
		switch {
		case work[m.name]:
			m.busyAt = now
			c.ensure(m)
			if m.conn != nil {
				ready = append(ready, m)
			}
		case m.conn != nil && m.host != nil && now.Sub(m.busyAt) > idleClose:
			m.conn.Close()
			m.conn = nil
		}
	}
	c.mu.Unlock()
	var wg sync.WaitGroup
	for _, m := range ready {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.converge(ctx, m)
		}()
	}
	wg.Wait()
}

// missingAfter is how many lists in a row must lack an open run before it is taken for gone from its node.
const missingAfter = 2

// callNode makes one call to m's node on conn; false when the connection is lost (a call that timed out keeps it).
func (c *Coord) callNode(ctx context.Context, m *machine, conn Conn, method string, params, out any) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, callWait)
	defer cancel()
	err := conn.Call(ctx, method, params, out)
	switch {
	case err == nil:
	case transport(err) && wire.Code(err) != wire.CodeTimeout && !errors.Is(err, context.DeadlineExceeded):
		c.lost(m, conn, err)
		return false, err
	default:
		c.mu.Lock()
		m.err = err
		c.mu.Unlock()
	}
	return true, err
}

// converge reconciles and dispatches one connected machine.
func (c *Coord) converge(ctx context.Context, m *machine) {
	c.mu.Lock()
	conn, ack := m.conn, c.acked[m.name]
	c.mu.Unlock()
	if conn == nil {
		return
	}
	var list node.Runs
	if _, err := c.callNode(ctx, m, conn, node.MRunList, node.ListParams{Coordinator: c.id, Ack: ack}, &list); err != nil {
		return
	}

	c.mu.Lock()
	c.acked[m.name] = slices.DeleteFunc(c.acked[m.name], func(id string) bool { return slices.Contains(ack, id) })
	for _, id := range ack {
		c.ackDone[id] = true
	}
	seen := map[string]node.Snapshot{}
	var events []journal.Event
	for _, s := range list.Runs {
		seen[s.Run] = s
		r := c.st.Runs[s.Run]
		if r == nil || r.Machine != m.name {
			continue
		}
		if o := observation(s); r.Would(o) {
			events = append(events, journal.NewEvent(task.ERunObserved, o))
		}
	}
	for _, r := range c.st.Runs {
		if r.Machine != m.name {
			continue
		}
		if _, onNode := seen[r.ID]; onNode || r.State != task.Running && r.State != task.Unknown {
			delete(c.missing, r.ID)
			continue
		}
		if c.missing[r.ID]++; c.missing[r.ID] >= missingAfter {
			if o := (task.Observation{ID: r.ID, State: task.Unknown, Reason: "missing", NodeRev: r.NodeRev}); r.Would(o) {
				events = append(events, journal.NewEvent(task.ERunObserved, o))
			}
		}
	}
	err := c.commit(nil, events...)
	var stops []string
	for _, s := range list.Runs {
		r := c.st.Runs[s.Run]
		if r == nil || r.Machine != m.name || task.Open(r.State) {
			continue
		}
		switch {
		case node.Terminal(s.State.State) || s.State.State == node.StateUnknown:
			if !c.ackDone[s.Run] && !slices.Contains(c.acked[m.name], s.Run) {
				c.acked[m.name] = append(c.acked[m.name], s.Run)
			}
		case r.State == task.Abandoned && !s.StopAsked: // given up on, but it runs on
			stops = append(stops, s.Run)
		}
	}
	for _, r := range c.st.Runs {
		if _, onNode := seen[r.ID]; r.Machine == m.name && r.State == task.Abandoned && !onNode && !c.ackDone[r.ID] {
			stops = append(stops, r.ID) // not there (yet): the stop leaves a tombstone a late start finds
		}
	}

	var starts []*task.Run
	var mine []*task.Run
	for _, r := range c.st.Runs {
		if r.Machine == m.name && task.Open(r.State) {
			mine = append(mine, r)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].QueuedAt.Before(mine[j].QueuedAt) })
	used, dirs := 0, map[string]bool{}
	for _, r := range c.st.Runs { // given up on but still running there: it keeps its slot and directory until it ends
		if s, onNode := seen[r.ID]; r.Machine == m.name && r.State == task.Abandoned && onNode && !node.Terminal(s.State.State) &&
			s.State.State != node.StateUnknown {
			used++
			dirs[dirKey(r.Dir, m.hello.OS)] = true
		}
	}
	for _, r := range mine {
		if r.State == task.Queued {
			continue
		}
		used++
		dirs[dirKey(r.Dir, m.hello.OS)] = true
		s, onNode := seen[r.ID]
		switch {
		case r.Want == "stop" && onNode && !node.Terminal(s.State.State) && !s.StopAsked:
			stops = append(stops, r.ID)
		case r.Want == "stop" && !onNode && r.State == task.Starting: // the start never arrived: the node records it stopped
			stops = append(stops, r.ID)
		case r.State == task.Starting && !onNode && r.Want == "run" && time.Since(c.sent[r.ID]) > resendAfter:
			starts = append(starts, r)
		}
	}
	var starting []journal.Event
	for _, r := range mine {
		if r.State != task.Queued || r.Want != "run" {
			continue
		}
		if used >= c.slots(m.name) {
			break
		}
		dir, ok := c.mapDir(r, m)
		if !ok || dirs[dirKey(dir, m.hello.OS)] {
			continue
		}
		used++
		dirs[dirKey(dir, m.hello.OS)] = true
		starting = append(starting, journal.NewEvent(task.ERunStarting, task.RunStarting{ID: r.ID, Dir: dir}))
	}
	if err == nil {
		err = c.commit(nil, starting...)
	}
	if err == nil {
		for _, e := range starting {
			var d task.RunStarting
			json.Unmarshal(e.Data, &d)
			starts = append(starts, c.st.Runs[d.ID])
		}
	}
	if err != nil {
		m.err = err
	}
	params := make([]node.StartParams, len(starts))
	for i, r := range starts {
		c.sent[r.ID] = time.Now()
		params[i] = node.StartParams{Run: r.ID, Task: r.Task, Coordinator: c.id, Profile: r.Profile, Dir: r.Dir,
			Brief: r.Brief, Title: r.Title, Runner: r.Runner}
	}
	c.mu.Unlock()

	for _, id := range stops {
		var s node.Snapshot
		if ok, _ := c.callNode(ctx, m, conn, node.MRunStop, node.RunRef{Run: id, Coordinator: c.id}, &s); !ok {
			return
		}
	}
	for _, p := range params {
		var s node.Snapshot
		ok, err := c.callNode(ctx, m, conn, node.MRunStart, p, &s)
		if !ok {
			return
		}
		if err != nil && transport(err) {
			continue // timed out: the next list tells whether it arrived
		}
		o := observation(s)
		if err != nil {
			o = task.Observation{ID: p.Run, State: task.Failed, Reason: err.Error()}
		}
		c.mu.Lock()
		if r := c.st.Runs[p.Run]; r != nil && r.Would(o) {
			c.commit(nil, journal.NewEvent(task.ERunObserved, o))
		}
		c.mu.Unlock()
	}
}

func observation(s node.Snapshot) task.Observation {
	return task.Observation{ID: s.Run, State: s.State.State, ExitCode: s.ExitCode, Reason: s.Reason, Provider: s.Provider,
		Session: s.Session, Pane: s.Pane, NodeRev: s.Rev, StartedAt: s.StartedAt, EndedAt: s.EndedAt}
}

// mapDir is r's directory as machine m names it; false while the machine it was written for has not been reached (it
// is dialed meanwhile). The caller holds mu.
func (c *Coord) mapDir(r *task.Run, m *machine) (string, bool) {
	from := r.From
	if from == "" || from == m.name {
		return r.Dir, true
	}
	src := c.ms[from]
	if src == nil {
		return r.Dir, true
	}
	if src.hello.Home == "" {
		src.busyAt = time.Now()
		c.ensure(src)
		return "", false
	}
	if p, ok := pathmap.Map(r.Dir, end(src.hello), end(m.hello)); ok {
		return p, true
	}
	return r.Dir, true
}

// dirKey is dir as a key for the one-run-per-directory rule on a machine running os.
func dirKey(dir, os string) string {
	if os == "windows" {
		return path.Clean(strings.ToLower(strings.ReplaceAll(dir, `\`, "/")))
	}
	return paths.Clean(dir)
}

func end(h remote.Hello) pathmap.End {
	return pathmap.End{OS: h.OS, Home: h.Home, Host: h.Hostname, WSL: h.WSL != ""}
}

// call forwards a request to machine name's node, connecting it first when needed.
func (c *Coord) call(ctx context.Context, name, method string, params, out any) error {
	c.mu.Lock()
	m := c.ms[name]
	if m == nil {
		c.mu.Unlock()
		return notFound("machine " + name)
	}
	m.busyAt = time.Now()
	c.ensure(m)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		conn, dialing, err := m.conn, m.dialing, m.err
		c.mu.Unlock()
		if conn != nil {
			err := conn.Call(ctx, method, params, out)
			if err != nil && transport(err) && wire.Code(err) != wire.CodeTimeout {
				c.lost(m, conn, err)
			}
			return err
		}
		if !dialing {
			if err == nil {
				err = &wire.Error{Code: wire.CodeOffline}
			}
			return err
		}
		select {
		case <-ctx.Done():
			return &wire.Error{Code: wire.CodeTimeout, Detail: method}
		case <-time.After(50 * time.Millisecond):
		}
	}
}
