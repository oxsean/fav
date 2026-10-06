package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/dial"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
	tuiui "github.com/oxsean/fav/internal/ui/tui"
	"github.com/oxsean/fav/internal/wire"
)

// farHosts are the other machines this command reads, reached on first use (run sets it).
var farHosts = sync.OnceValue(loadFarHosts)

func loadFarHosts() *cliHosts { return newCLIHosts(tend.LoadConfig()) }

// remoteHosts reads them (tests swap it for a fake).
var remoteHosts = func() *remote.Hosts { return farHosts().Hosts }

// newHosts: config.hosts over ssh; with config.coordinator.url set, the machines the server lets the viewer read,
// through node.call over link's connection, starting from the viewer's own kept on disk, and ssh only to resume.
func newHosts(cfg tend.Config, link *serverLink) tuiui.Others {
	var ssh *remote.Hosts
	if len(cfg.Hosts) > 0 {
		ssh = remote.NewHosts(cfg.Hosts, i18n.Resolve(cfg.Lang))
	}
	c := cfg.Coordinator
	if c == nil || c.URL == "" {
		return tuiui.Others{Hosts: ssh, SSH: ssh}
	}
	nc := remote.NewNodeCall(c.URL, link.call)
	nc.SetMachines(nc.Kept())
	return tuiui.Others{Hosts: remote.NewHostsOver(nc), Server: nc, SSH: ssh}
}

// serverLink is the server connection node.call goes through: the TUI's current one, or the one a command dialed.
type serverLink struct {
	mu sync.Mutex
	cl *coord.Client
}

func (l *serverLink) use(cl *coord.Client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cl = cl
}

func (l *serverLink) call(ctx context.Context, machine, method string, params json.RawMessage, out any) error {
	l.mu.Lock()
	cl := l.cl
	l.mu.Unlock()
	if cl == nil {
		return &wire.Error{Code: wire.CodeOffline}
	}
	return cl.Call(ctx, coord.MNodeCall, coord.NodeCall{Machine: machine, Method: method, Params: params}, out)
}

// cliHosts: a command's other machines; mode 2 dials the server once, when a machine is to be reached.
type cliHosts struct {
	tuiui.Others
	server   string
	link     serverLink
	once     sync.Once
	down     error           // the server could not be reached
	machines []coord.Machine // the server's list, once reached
	own      map[string]bool // the machines in it the viewer owns
}

func newCLIHosts(cfg tend.Config) *cliHosts {
	c := &cliHosts{}
	c.Others = newHosts(cfg, &c.link)
	if c.Server != nil {
		c.server = cfg.Coordinator.URL
	}
	return c
}

// serverWait bounds a command's dial of the server and its first answers.
const serverWait = 5 * time.Second

// dialServer reaches config.coordinator as a client (tests swap it).
var dialServer = func(ctx context.Context) (*coord.Client, error) {
	c := loadConfig().Coordinator
	token, err := dial.ReadToken(paths.Expand(c.TokenFile))
	if err != nil {
		return nil, err
	}
	conn, err := dial.Dial(ctx, c.URL, dial.RoleClient, token, wire.Options{})
	if err != nil {
		return nil, err
	}
	return &coord.Client{Conn: conn}, nil
}

// reach dials the server once per command and reads which machines the viewer may read. fzf lists in a new process
// per key: it reads only the viewer's own machines (others' lists live in memory only), and dials no more within
// cachedFor of a failed dial.
func (c *cliHosts) reach() error {
	if c.Server == nil {
		return nil
	}
	c.once.Do(func() { c.down = c.dial() })
	return c.down
}

func (c *cliHosts) dial() error {
	path := serverDownPath()
	if at := serverFailedAt(path, c.server); cachedFor > 0 && !at.IsZero() && time.Since(at) >= 0 && time.Since(at) < cachedFor {
		return &wire.Error{Code: wire.CodeOffline}
	}
	ctx, cancel := context.WithTimeout(context.Background(), serverWait)
	defer cancel()
	cl, err := dialServer(ctx)
	var hello remote.Hello
	var ms coord.Machines
	if err == nil {
		if err = cl.Call(ctx, remote.MHello, remote.HelloParams{Proto: wire.Proto, Role: "client"}, &hello); err == nil {
			err = cl.Call(ctx, coord.MMachineList, coord.MachinesParams{}, &ms)
		}
		if err != nil {
			cl.Close()
		}
	}
	if err != nil {
		noteServerDown(path, c.server, time.Now())
		return err
	}
	os.Remove(path)
	c.link.use(cl)
	c.machines = ms.Machines
	list := tuiui.ServerMachines(ms.Machines, hello.Caller, projects.NodeID(tend.Home()))
	tuiui.ForgetOthers(c.Server, ms.Machines, hello.Caller, list)
	c.own = map[string]bool{}
	for _, x := range list {
		c.own[x.Name] = x.Mine
	}
	if cachedFor > 0 {
		list = slices.DeleteFunc(list, func(x remote.Machine) bool { return !x.Mine })
	}
	c.Server.SetMachines(list)
	return nil
}

// serverDownPath records when a dial of the server last failed: its address and the time, nothing of any session.
func serverDownPath() string { return filepath.Join(tend.Home(), "hosts", "server.json") }

type serverDown struct {
	Server string    `json:"server"`
	At     time.Time `json:"failed_at"`
}

func serverFailedAt(path, server string) time.Time {
	var d serverDown
	if b, err := os.ReadFile(path); err != nil || json.Unmarshal(b, &d) != nil || d.Server != server {
		return time.Time{}
	}
	return d.At
}

func noteServerDown(path, server string, at time.Time) {
	if b, err := json.Marshal(serverDown{server, at}); err == nil && os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		fileio.WriteFile(path, b, 0o600)
	}
}

// machine is the server's entry for name.
func (c *cliHosts) machine(name string) coord.Machine {
	for _, mc := range c.machines {
		if mc.Name == name {
			return mc
		}
	}
	return coord.Machine{}
}

// mine: the viewer owns the machine called name, as the server says, else as the lists kept on disk do.
func (c *cliHosts) mine(name string) bool {
	if c.own != nil {
		return c.own[name]
	}
	for _, x := range c.Server.Kept() {
		if x.Name == name {
			return true
		}
	}
	return false
}

// sshSame: the ssh host called name here is the machine the server calls name (their node ids match).
func (c *cliHosts) sshSame(name string) bool {
	if _, ok := c.SSH.Host(name); !ok {
		return false
	}
	want := c.machine(name).NodeID
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	hello, err := c.SSH.Hello(ctx, name)
	return err == nil && want != "" && hello.NodeID == want
}

// selectsHosts: q lists other machines.
func selectsHosts(q tend.Query) bool { return q.Host != "" && q.Host != tend.HostLocal }

// hostTimeout bounds reaching a host and fetching its list.
const hostTimeout = 20 * time.Second

// cachedFor: a host list fetched this recently is used as it is (fzf lists again on every keystroke); 0 always fetches.
var cachedFor time.Duration

// hostName is the configured host called name (any case), "" when there is none.
func hostName(h *remote.Hosts, name string) string {
	for _, n := range h.Names() {
		if strings.EqualFold(n, name) {
			return n
		}
	}
	return ""
}

// hostsOf: the configured hosts q selects; none without host:, every one for host:all.
func hostsOf(h *remote.Hosts, q tend.Query) []string {
	switch q.Host {
	case "", tend.HostLocal:
		return nil
	case tend.HostAll:
		return h.Names()
	}
	if n := hostName(h, q.Host); n != "" {
		return []string{n}
	}
	fmt.Fprintln(os.Stderr, i18n.F("cli.hosts.unknown", q.Host, strings.Join(h.Names(), " ")))
	return nil
}

// hostRows are the rows q selects on other machines, fetched in parallel; a host that cannot answer gives its cached
// rows and one line on stderr. live: who runs on each host, by host name (status:live only).
func hostRows(q tend.Query) ([]*tend.Rec, map[string]map[string]capture.Live) {
	if q.Status == tend.StatusTrash || q.Status == tend.StatusAgent {
		return nil, nil
	}
	h, far := remoteHosts(), farHosts()
	if far.Server != nil && cachedFor == 0 && selectsHosts(q) {
		far.reach()
	}
	names := hostsOf(h, q)
	type answer struct {
		recs    []*tend.Rec
		st      remote.State
		live    map[string]capture.Live
		liveErr error
		goos    string
	}
	answers := make([]answer, len(names))
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() {
			a := &answers[i]
			a.recs, a.st = h.Cached(name)
			tried, failed := h.Failed(name)
			switch {
			case cachedFor > 0 && failed != nil && time.Since(tried) < cachedFor: // down a moment ago: not again per keystroke
				a.st.Err = failed
			case cachedFor == 0 || a.st.At.IsZero() || time.Since(a.st.At) > cachedFor:
				if err := far.reach(); err != nil {
					a.st.Err = err
					break
				}
				a.recs, a.st = h.Sessions(ctx, name)
				if a.st.Err == nil {
					if hello, err := h.Hello(ctx, name); err == nil { // reached just now: no dial
						a.goos = hello.OS
					}
				}
			}
			if q.Status == tend.StatusLive && a.st.Err == nil {
				var at time.Time
				if a.live, at = h.CachedLive(name); cachedFor == 0 || at.IsZero() || time.Since(at) > cachedFor {
					a.live, a.liveErr = h.Live(ctx, name)
				}
			}
		})
	}
	wg.Wait()
	var out []*tend.Rec
	live := make(map[string]map[string]capture.Live, len(names))
	snap, rows := sessionProjects(), belongRows()
	if far.down != nil && len(names) > 0 { // every machine read through it: one line, not one per machine
		fmt.Fprintln(os.Stderr, i18n.F("remote.server_down", remote.Reason(far.down)))
	}
	for i, name := range names {
		a := answers[i]
		if _, known := snap.OS[name]; a.goos != "" && !known && snap.OS != nil {
			snap.OS[name] = a.goos
		}
		rows.Place(a.recs...)
		switch {
		case far.down != nil:
		case a.st.Err != nil && !a.st.At.IsZero():
			fmt.Fprintln(os.Stderr, i18n.F("cli.host.cached", name, remote.Reason(a.st.Err), render.WhenFull(a.st.At)))
		case cmp.Or(a.st.Err, a.liveErr) != nil:
			fmt.Fprintln(os.Stderr, i18n.F("remote.unreachable", name, remote.Reason(cmp.Or(a.st.Err, a.liveErr))))
		}
		hq := q
		hq.Live = func(id string) bool { _, ok := a.live[id]; return ok }
		for _, r := range a.recs {
			if hq.Match(r) {
				out = append(out, r)
			}
		}
		live[name] = a.live
	}
	return out, live
}

// pickHost resolves host:ref (a configured host, then a record id or session id prefix there); ok is false when ref
// names no configured host. A full key found in the host's cached list is taken from there without reaching it.
func pickHost(ref string) (r *tend.Rec, ok bool, err error) {
	name, sub, found := strings.Cut(ref, ":")
	if !found {
		return nil, false, nil
	}
	h, far := remoteHosts(), farHosts()
	if hostName(h, name) == "" {
		far.reach()
	}
	if name = hostName(h, name); name == "" {
		return nil, false, nil
	}
	if sub == "" {
		return nil, true, errors.New(i18n.T("cli.missing_id"))
	}
	cached, _ := h.Cached(name)
	if r, n := matchRef(cached, sub, recKeys); n == 1 && (r.SessionID == sub || r.ID == sub) {
		return r, true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	far.reach()
	recs, st := h.Sessions(ctx, name)
	if r, n := matchRef(recs, sub, recKeys); n > 0 {
		return r, true, refErr(ref, n)
	}
	if st.Err != nil {
		return nil, true, errors.New(i18n.F("remote.unreachable", name, remote.Reason(st.Err)))
	}
	return nil, true, refErr(ref, 0)
}

// pickLocal is pick for commands that write: another machine's records are read-only here.
func pickLocal(s *tend.Store, ref string) (*tend.Rec, error) {
	r, err := pick(s, ref)
	if err == nil && r.Host != "" {
		return nil, readOnly(r)
	}
	return r, err
}

func readOnly(r *tend.Rec) error { return i18n.E("cli.remote.read_only", r.Host) }

// remoteResume is `ssh -t <host> tend resume …` for r, as a command spec. Mode 2 resumes over ssh only the viewer's
// own machine whose host of the same name here is that machine; otherwise there is the command to run on it.
func remoteResume(r *tend.Rec) (spec agent.CommandSpec, there string, err error) {
	h, far := remoteHosts(), farHosts()
	if far.Server != nil {
		far.reach()
		if !far.mine(r.Host) {
			return spec, "", i18n.E("cli.remote.shared", r.Host)
		}
		if !far.sshSame(r.Host) {
			return spec, far.thereLine(r), nil
		}
		h = far.SSH
	}
	cmd, ok := h.ResumeCommand(r)
	if !ok {
		return spec, "", i18n.E("cli.host.unknown", r.Host)
	}
	return agent.CommandSpec{Exec: cmd.Args[0], Args: cmd.Args[1:]}, "", nil
}

// thereLine is the command resuming r on its machine, quoted for that machine's shell.
func (c *cliHosts) thereLine(r *tend.Rec) string {
	spec, err := agent.ResumeOf(r, "")
	if err != nil {
		return ""
	}
	sh := shell.POSIX
	if c.machine(r.Host).OS == "windows" {
		sh = shell.PowerShell
	}
	return sh.Line(spec.Cwd, spec.Argv())
}

// resumeRemote resumes r on its host over ssh: in a new tab of this Herdr workspace when tend runs inside Herdr
// (workspace: another one, by label), else in this terminal.
func resumeRemote(r *tend.Rec, dryRun, noHerdr bool, workspace string) error {
	spec, there, err := remoteResume(r)
	if err != nil {
		return err
	}
	if there != "" {
		return i18n.E("remote.run_there", r.Host, there)
	}
	plan := capture.Plan{Spec: spec}
	if !noHerdr && herdr.Active() {
		plan.Ws = herdrWorkspace(workspace)
	}
	if dryRun {
		plan.Checks = remoteHosts().Source(r).Checks()
		printPlan(r, plan, true)
		return nil
	}
	if plan.Ws != nil {
		msg, warn, err := plan.RunInHerdr(r)
		if err == nil {
			if warn != nil {
				fmt.Fprint(os.Stderr, i18n.F("cli.resume.warn", warn))
			}
			fmt.Println(msg)
			return nil
		}
		fmt.Fprint(os.Stderr, i18n.F("cli.resume.herdr_failed", err))
	}
	return resumeHere(spec)
}

// herdrWorkspace is the Herdr workspace labelled label, or the one this pane is in.
func herdrWorkspace(label string) *herdr.Workspace {
	if label != "" {
		ws, _ := herdr.FindWorkspace(label)
		return ws
	}
	pane, err := herdr.CurrentPane()
	if err != nil {
		return nil
	}
	ws, _ := herdr.Workspaces()
	for i := range ws {
		if ws[i].WorkspaceID == pane.WorkspaceID {
			return &ws[i]
		}
	}
	return nil
}
