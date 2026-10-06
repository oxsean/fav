package remote

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Transport is how Hosts reaches other machines: over ssh (NewHosts) or through a coordinator's node.call
// (NewNodeCall). Safe for concurrent use.
type Transport interface {
	Machines() []string
	// Hello is what machine said when it was last reached, reaching it first when needed.
	Hello(ctx context.Context, machine string) (Hello, error)
	Call(ctx context.Context, machine, method string, params, out any) error
	// Target is what a machine's cache was fetched through: another target never reads it.
	Target(machine string) string
	// Keep: machine's list and who runs there may be written to disk; otherwise they are held in memory only.
	Keep(machine string) bool
	Close()
}

// sshTransport reaches the configured machines: one client per host, dialed on first use and again after it fails.
type sshTransport struct {
	mu      sync.Mutex
	hosts   []tend.Host
	lang    string
	clients map[string]*Client
	hellos  map[string]Hello
	dialing map[string]chan struct{} // one dial per host at a time
	dial    func(tend.Host) (*Client, error)
	closed  bool
}

func (s *sshTransport) Machines() []string {
	out := make([]string, len(s.hosts))
	for i, x := range s.hosts {
		out[i] = x.Name
	}
	return out
}

func (s *sshTransport) Host(name string) (tend.Host, bool) {
	for _, x := range s.hosts {
		if x.Name == name {
			return x, true
		}
	}
	return tend.Host{}, false
}

// client dials name if it has no working client, and checks its protocol with hello.
func (s *sshTransport) client(ctx context.Context, name string) (*Client, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, &wire.Error{Code: wire.CodeClosed}
	}
	d := s.dialing[name]
	if d == nil {
		d = make(chan struct{}, 1)
		s.dialing[name] = d
	}
	s.mu.Unlock()
	select {
	case d <- struct{}{}:
	case <-ctx.Done():
		return nil, &wire.Error{Code: wire.CodeTimeout}
	}
	defer func() { <-d }()
	s.mu.Lock()
	c, closed := s.clients[name], s.closed
	s.mu.Unlock()
	if closed { // closed while this call waited for another one's dial
		return nil, &wire.Error{Code: wire.CodeClosed}
	}
	if c != nil && c.Err() == nil {
		return c, nil
	}
	host, ok := s.Host(name)
	if !ok {
		return nil, &wire.Error{Code: wire.CodeNotFound, Detail: name}
	}
	c, err := s.dial(host)
	if err != nil {
		return nil, err
	}
	var hello Hello
	if err := c.Call(ctx, MHello, HelloParams{Proto: wire.Proto, Role: "client", Lang: s.lang}, &hello); err != nil {
		c.Close()
		return nil, err
	}
	if hello.Proto != wire.Proto {
		c.Close()
		return nil, &wire.Error{Code: wire.CodeProto, Detail: hello.Version}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed { // Close ran while this one was dialing
		c.Close()
		return nil, &wire.Error{Code: wire.CodeClosed}
	}
	s.clients[name], s.hellos[name] = c, hello
	return c, nil
}

func (s *sshTransport) Hello(ctx context.Context, name string) (Hello, error) {
	if _, err := s.client(ctx, name); err != nil {
		return Hello{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hellos[name], nil
}

func (s *sshTransport) Call(ctx context.Context, name, method string, params, out any) error {
	c, err := s.client(ctx, name)
	if err != nil {
		return err
	}
	return c.Call(ctx, method, params, out)
}

// Target: the ssh alias and tend command.
func (s *sshTransport) Target(name string) string {
	host, _ := s.Host(name)
	return strings.Join(append([]string{host.SSH}, host.Tend...), "\x00")
}

func (s *sshTransport) Keep(string) bool { return true }

func (s *sshTransport) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, c := range s.clients {
		c.Close()
	}
	clear(s.clients)
}

// NodeCaller performs node.call{machine, method, params} on a coordinator and decodes its result into out.
type NodeCaller func(ctx context.Context, machine, method string, params json.RawMessage, out any) error

// Machine is one machine a coordinator lets the viewer read; Mine: the viewer owns it, so its list may be cached on disk.
type Machine struct {
	Name string
	Mine bool
}

// NodeCall reaches machines through the coordinator at server; the connection behind call belongs to the caller.
type NodeCall struct {
	server string
	call   NodeCaller
	mu     sync.Mutex
	ms     []Machine
	hellos map[string]Hello
	closed bool
}

func NewNodeCall(server string, call NodeCaller) *NodeCall {
	return &NodeCall{server: server, call: call, hellos: map[string]Hello{}}
}

// SetMachines replaces the machines it reaches.
func (n *NodeCall) SetMachines(ms []Machine) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ms = append([]Machine(nil), ms...)
}

func (n *NodeCall) machine(name string) (Machine, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, m := range n.ms {
		if m.Name == name {
			return m, true
		}
	}
	return Machine{}, false
}

func (n *NodeCall) Machines() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, len(n.ms))
	for i, m := range n.ms {
		out[i] = m.Name
	}
	return out
}

// Hello asks the machine once and again after it was offline. Lang stays out: a node sets its whole process's language
// from it.
func (n *NodeCall) Hello(ctx context.Context, name string) (Hello, error) {
	n.mu.Lock()
	h, ok := n.hellos[name]
	closed := n.closed
	n.mu.Unlock()
	switch {
	case closed:
		return Hello{}, &wire.Error{Code: wire.CodeClosed}
	case ok:
		return h, nil
	}
	if _, listed := n.machine(name); !listed {
		return Hello{}, &wire.Error{Code: wire.CodeNotFound, Detail: name}
	}
	if err := n.forward(ctx, name, MHello, HelloParams{Proto: wire.Proto, Role: "client"}, &h); err != nil {
		return Hello{}, err
	}
	if h.Proto != wire.Proto {
		return Hello{}, &wire.Error{Code: wire.CodeProto, Detail: h.Version}
	}
	n.mu.Lock()
	n.hellos[name] = h
	n.mu.Unlock()
	return h, nil
}

func (n *NodeCall) Call(ctx context.Context, name, method string, params, out any) error {
	if _, err := n.Hello(ctx, name); err != nil {
		return err
	}
	return n.forward(ctx, name, method, params, out)
}

func (n *NodeCall) forward(ctx context.Context, name, method string, params, out any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
		}
		raw = b
	}
	err := n.call(ctx, name, method, raw, out)
	if c := wire.Code(err); c == wire.CodeOffline || c == wire.CodeClosed {
		n.mu.Lock()
		delete(n.hellos, name)
		n.mu.Unlock()
	}
	return err
}

// Target: the server and the machine's name there.
func (n *NodeCall) Target(name string) string { return n.server + "\x00" + name }

func (n *NodeCall) Keep(name string) bool {
	m, _ := n.machine(name)
	return m.Mine
}

// Kept are the machines whose lists were kept on disk from this server: the viewer's own, shown from there before
// the server's machine list arrives or while it cannot be reached.
func (n *NodeCall) Kept() []Machine {
	files, _ := filepath.Glob(filepath.Join(tend.Home(), "hosts", "*", "sessions.json"))
	var out []Machine
	for _, f := range files {
		var c struct {
			Target string `json:"target"`
		}
		b, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(b, &c) != nil {
			continue
		}
		name, ok := strings.CutPrefix(c.Target, n.server+"\x00")
		if ok && name != "" && cachePath(name) == f {
			out = append(out, Machine{Name: name, Mine: true})
		}
	}
	slices.SortFunc(out, func(a, b Machine) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Forget removes what was kept on disk from this server of each machine that ms, the server's whole list, no longer
// gives as the viewer's own: one now shared by another person, or gone.
func (n *NodeCall) Forget(ms []Machine) {
	for _, x := range n.Kept() {
		if !slices.Contains(ms, Machine{Name: x.Name, Mine: true}) {
			ForgetCache(x.Name)
		}
	}
}

func (n *NodeCall) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closed = true
}
