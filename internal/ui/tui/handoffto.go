package tui

import (
	"cmp"
	"context"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// The handoff dialog's other machine: the new session can start on one of the viewer's own machines, in a directory
// found there (the project's, else the checkouts of the session's remote), typed or browsed. Every call goes through
// remote.Handoff and the Handover it returns, off the main loop, as `tend handoff --host` does; the dialog only picks
// and shows. Opening there: a terminal over ssh, a task, or (no ssh) the command to run there, copied.

// handoffTo is where the dialog hands the session to.
type handoffTo struct {
	seq        int
	host       string // the machine chosen; "" is this one
	want       string // the machine being read; its answer replaces host
	reading    bool
	x          *remote.Handover // nil: this machine's own session handed here, written by capture.WriteHandoff
	dirs       []remote.RepoDir
	dir        string
	how        string // i18n key of how dir was found
	record     bool   // put dir into the project once the new session is under way
	recordable bool
	comparing  bool             // the environments for dir are being compared; nothing opens before they are
	env        *envcheck.Report // their comparison
	envWhy     string           // why they were not compared
}

type handoffReadMsg struct {
	seq     int
	host    string
	x       *remote.Handover
	path    string // this machine's own session handed here: its pack, written by capture.WriteHandoff
	dirs    []remote.RepoDir
	dirsErr string
	err     string
}

type handoffPutMsg struct {
	seq       int
	host      string
	put       remote.HandoffPut
	provider  string // "": copy the command to run there
	err       string
	recorded  string // the note of the directory recorded into a project
	recordErr error
}

type handoffEnvMsg struct {
	seq int
	dir string
	env *capture.HandoffEnv
	rep envcheck.Report
	why string
}

type handoffDirsMsg struct {
	host string
	dirs node.Dirs
	err  string
}

// peerFn reaches a machine by name off the main loop: "" is this one.
func (m *Model) peerFn() func(ctx context.Context, name string) (remote.Peer, error) {
	h, here := m.hosts, m.here
	return func(ctx context.Context, name string) (remote.Peer, error) {
		if name == "" {
			if here != nil {
				return here(), nil
			}
			return remote.Here(""), nil
		}
		if h == nil {
			return remote.Peer{}, i18n.E("cli.host.unknown", name)
		}
		return h.Reach(ctx, name)
	}
}

// handoffWhy is why the session cannot be handed to or from machine name now ("" this machine, or it can).
func (m *Model) handoffWhy(name, method string) string {
	return m.ownWhy(name, method, "cli.handoff.not_mine", "cli.handoff.server_old")
}

// ownWhy is why work only machine name's owner may do cannot go there now through method ("" this machine, or it
// can): another person's machine (notMine), a server that does not forward that work (serverOld), a machine offline or
// whose tend is too old for method.
func (m *Model) ownWhy(name, method, notMine, serverOld string) string {
	if name == "" {
		return ""
	}
	if m.far.nc != nil {
		down, features := m.serverDown(), []string(nil)
		switch {
		case down != nil:
		case m.tasks.cl == nil || m.proj.hello == nil:
			down = &wire.Error{Code: wire.CodeOffline}
		default:
			features = m.proj.hello.Features
		}
		if why := remote.ServerRefusal(name, down, !m.shared(name), features, notMine, serverOld); why != "" {
			return why
		}
	}
	hr := m.remote[name]
	switch {
	case hr == nil:
		return i18n.F("cli.host.unknown", name)
	case hr.err != nil:
		return i18n.F("remote.unreachable", name, remote.Reason(hr.err))
	case hr.lacks[method]:
		return remote.TooOld(name, method)
	}
	return ""
}

// hostLabel names machine name for the dialog: "this machine" for "".
func hostLabel(name string) string { return cmp.Or(name, i18n.T("handoff.here")) }

// askHandoff opens the dialog on r: this machine's own session shows its pack at once (written in the background);
// another machine's is read through that machine for this one.
func (m *Model) askHandoff(r *tend.Rec) {
	if r.Host == "" {
		m.doHandoff()
		return
	}
	if why := m.handoffWhy(r.Host, remote.MHandoffFacts); why != "" {
		m.flash(why)
		return
	}
	m.closeOverlay()
	m.ov = overlay{kind: ovHandoff, rec: r, focus: -1, handoff: &handoffTo{}}
	m.flash(i18n.T("handoff.writing"))
	m.pending = tea.Batch(m.pending, m.readHandoff(""))
}

// readHandoff reads the session's facts where it is for host and the directories host offers, off the main loop.
func (m *Model) readHandoff(host string) tea.Cmd {
	d := m.ov.handoff
	d.seq++
	d.want, d.reading = host, true
	seq, cp := d.seq, *m.ovRec()
	peer, pairs, remotes := m.peerFn(), m.proj.snap.DirPairs(&cp, host), []string{cp.GitRemote, m.idx.GitRemote(&cp)}
	return func() tea.Msg {
		msg := handoffReadMsg{seq: seq, host: host}
		if cp.Host == "" && host == "" {
			path, err := capture.WriteHandoff(&cp)
			if err != nil {
				msg.err = i18n.F("handoff.failed", err.Error())
			}
			msg.path = path
			return msg
		}
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		from, err := peer(ctx, cp.Host)
		if err != nil {
			msg.err = err.Error()
			return msg
		}
		to, err := peer(ctx, host)
		if err != nil {
			msg.err = err.Error()
			return msg
		}
		x, err := remote.Handoff(ctx, from, to, remote.Ref{Provider: cp.Provider, SessionID: cp.SessionID})
		if err != nil {
			msg.err = remote.HandoffRefusal(from, hostLabel(cp.Host), remote.MHandoffFacts, err)
			return msg
		}
		msg.x = x
		if !to.Has(remote.MHandoffPut) {
			msg.err = remote.TooOld(hostLabel(host), remote.MHandoffPut)
			return msg
		}
		dirs, err := x.Dirs(ctx, pairs, remotes...)
		if err != nil && !(host == "" && wire.Code(err) == wire.CodeUnknownMethod) {
			msg.dirsErr = remote.HandoffRefusal(to, hostLabel(host), remote.MRepos, err)
		}
		msg.dirs = dirs
		return msg
	}
}

func (msg handoffReadMsg) apply(m *Model) tea.Cmd {
	d := m.ov.handoff
	if m.ov.kind != ovHandoff || d == nil || msg.seq != d.seq {
		return nil
	}
	d.reading = false
	if msg.err != "" {
		m.flash(msg.err)
		if m.ov.title == "" { // nothing shown yet: the dialog has nothing to offer
			m.closeOverlay()
		}
		return nil
	}
	m.notice = ""
	if msg.dirsErr != "" {
		m.flash(msg.dirsErr)
	}
	d.host, d.x, d.dirs, d.dir = msg.host, msg.x, msg.dirs, ""
	r := m.ovRec()
	switch {
	case msg.x == nil:
		d.dir, d.how = r.Cwd, "handoff.dir.same"
	case len(msg.dirs) == 1:
		d.dir = msg.dirs[0].Path
		d.how = map[string]string{"same": "handoff.dir.same", "project": "handoff.dir.project"}[msg.dirs[0].From]
		if d.how == "" {
			d.how = "handoff.dir.found_one"
		}
	case len(msg.dirs) > 1:
		d.how = "handoff.dir.found_many"
	default:
		d.how = "handoff.dir.none"
	}
	own := m.proj.snap.Of(r)
	d.recordable = d.how == "handoff.dir.found_one" || d.how == "handoff.dir.found_many" || d.how == "handoff.dir.none"
	d.record = d.recordable && own != nil
	m.ov.providers = m.handoffProvidersTo(r)
	m.ov.focus = -1
	if msg.x == nil {
		m.ov.title, m.ov.cursor = msg.path, 0
		m.loadHandoff()
		return nil
	}
	m.setHandoffText(msg.x.Text(d.dir))
	return tea.Batch(m.compareHandoffEnv(), m.checkSSHFor(d.host))
}

// setHandoffText shows text and keeps it in the dialog's file, where e edits it and the new session reads it.
func (m *Model) setHandoffText(text string) {
	var err error
	if m.ov.title == "" {
		m.ov.title, err = capture.WriteHandoffText(m.ovRec().SessionID, text)
	} else {
		err = os.WriteFile(m.ov.title, []byte(text), 0o600)
	}
	if err != nil {
		m.flash(i18n.F("handoff.failed", err.Error()))
		return
	}
	m.ov.cursor = 0
	m.loadHandoff()
}

// setHandoffDir starts the new session in dir, the pack written for it.
func (m *Model) setHandoffDir(dir, how string) {
	d := m.ov.handoff
	d.dir, d.how = dir, how
	if d.x != nil {
		cmd := m.compareHandoffEnv()
		m.setHandoffText(d.x.Text(dir))
		m.pending = tea.Batch(m.pending, cmd)
	}
}

// compareHandoffEnv compares the session's environment with the chosen directory's on the other machine off the main
// loop, as `tend handoff --host` does before it writes the pack; nil when there is nothing to compare.
func (m *Model) compareHandoffEnv() tea.Cmd {
	d := m.ov.handoff
	if d == nil || d.x == nil {
		return nil
	}
	d.x.Env, d.env, d.envWhy, d.comparing = nil, nil, "", false
	if d.dir == "" || d.x.From.Same(d.x.To) {
		return nil
	}
	d.comparing = true
	x, seq, dir, to := *d.x, d.seq, d.dir, hostLabel(d.host)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		rep, why := x.CompareEnv(ctx, dir, to)
		return handoffEnvMsg{seq: seq, dir: dir, env: x.Env, rep: rep, why: why}
	}
}

// apply puts the comparison into the pack, unless the pack was edited meanwhile; a block stops nothing.
func (msg handoffEnvMsg) apply(m *Model) tea.Cmd {
	d := m.ov.handoff
	if m.ov.kind != ovHandoff || d == nil || d.x == nil || msg.seq != d.seq || msg.dir != d.dir {
		return nil
	}
	d.comparing = false
	if msg.why != "" {
		d.envWhy = msg.why
	} else {
		d.env = &msg.rep
	}
	plain := d.x.Text(d.dir)
	d.x.Env = msg.env
	if b, err := os.ReadFile(m.ov.title); err == nil && string(b) == plain {
		m.setHandoffText(d.x.Text(d.dir))
	}
	return nil
}

// handoffAway: the new session starts on another machine than this one, or comes here from another.
func (m *Model) handoffAway() bool {
	d := m.ov.handoff
	return d != nil && (d.host != "" || d.x != nil)
}

// handoffProvidersTo: on this machine the installed CLIs, the session's own first; another machine checks there.
func (m *Model) handoffProvidersTo(r *tend.Rec) []string {
	if d := m.ov.handoff; d == nil || d.host == "" {
		return handoffProviders(r)
	}
	out := []string{r.Provider}
	for _, p := range []string{tend.ProviderClaude, tend.ProviderCodex} {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// pickHandoffHost: this machine and the others, those that cannot take a handoff now marked with why.
func (m *Model) pickHandoffHost() {
	if m.hosts == nil {
		m.flash(i18n.T("handoff.no_hosts"))
		return
	}
	saved := m.ov
	d := saved.handoff
	items := []item{{name: "", label: i18n.T("handoff.here")}}
	for _, name := range m.hosts.Names() {
		label := name
		if why := m.handoffWhy(name, remote.MHandoffPut); why != "" {
			label = name + "  ·  " + why
		}
		items = append(items, item{name: name, label: label})
	}
	m.openPicker(i18n.T("handoff.pick_host"), "", items, false, []string{cmp.Or(d.host, "\x00")}, func(m *Model, chosen []string) {
		m.ov = saved
		if len(chosen) == 0 || chosen[0] == d.host && !d.reading {
			return
		}
		if why := m.handoffWhy(chosen[0], remote.MHandoffPut); why != "" {
			m.flash(why)
			return
		}
		m.flash(i18n.F("handoff.reading", hostLabel(chosen[0])))
		m.pending = tea.Batch(m.pending, m.readHandoff(chosen[0]))
	})
	m.ov.back = func(mm *Model) { mm.ov = saved }
	if len(items) > 0 && d.host == "" {
		m.ov.cursor = 0
	}
}

// pickHandoffDir: the candidates found there, then typing or browsing a directory of that machine.
func (m *Model) pickHandoffDir() {
	d := m.ov.handoff
	if d == nil || d.x == nil || d.reading {
		return
	}
	saved := m.ov
	const browse = "\x00browse"
	var items []item
	for _, c := range d.dirs {
		label := c.Path
		if c.Branch != "" {
			label += "  ·  " + c.Branch
		}
		items = append(items, item{name: c.Path, label: label})
	}
	items = append(items, item{name: browse, label: i18n.T("handoff.dir_browse")})
	m.openPicker(i18n.F("handoff.pick_dir", hostLabel(d.host)), i18n.T("handoff.dir_type_hint"), items, false, []string{d.dir},
		func(m *Model, chosen []string) {
			m.ov = saved
			switch {
			case len(chosen) == 0:
			case chosen[0] == browse:
				m.browseHandoffDir("")
			case slices.ContainsFunc(d.dirs, func(c remote.RepoDir) bool { return c.Path == chosen[0] }):
				how := "handoff.dir.found_one"
				if len(d.dirs) > 1 {
					how = "handoff.dir.picked"
				}
				m.setHandoffDir(chosen[0], how)
			default:
				m.typedHandoffDir(chosen[0])
			}
		})
	m.ov.parse = func(q string) (item, bool) {
		q = strings.TrimSpace(q)
		if !pathmap.Abs(q) && !strings.HasPrefix(q, "~") {
			return item{}, false
		}
		return item{name: q, label: i18n.F("handoff.dir_use", q)}, true
	}
	m.ov.back = func(mm *Model) { mm.ov = saved }
}

// typedHandoffDir takes a directory typed for the other machine, written the way that machine names it.
func (m *Model) typedHandoffDir(dir string) {
	d := m.ov.handoff
	if d.host == "" {
		dir = paths.Expand(dir)
	} else if rest, ok := strings.CutPrefix(dir, "~"); ok && d.x != nil && d.x.To.Hello.Home != "" {
		dir = d.x.To.Hello.Home + rest
	}
	if !pathmap.Abs(dir) {
		m.flash(i18n.F("cli.handoff.dir_abs", dir))
		return
	}
	d.recordable = true
	m.setHandoffDir(dir, "handoff.dir.typed")
}

// browseHandoffDir lists dir on the chosen machine ("" its roots): this one through the directory picker, another
// through its node.dirs.
func (m *Model) browseHandoffDir(dir string) {
	d := m.ov.handoff
	saved := m.ov
	if d.host == "" {
		start := dir
		if start == "" {
			if home, err := os.UserHomeDir(); err == nil {
				start = home + string(os.PathSeparator)
			}
		}
		m.openDirPicker(i18n.F("handoff.pick_dir", hostLabel("")), start, func(m *Model, chosen string) {
			m.ov = saved
			m.typedHandoffDir(chosen)
		})
		m.ov.back = func(mm *Model) { mm.ov = saved }
		return
	}
	x, host := d.x, d.host
	m.pending = tea.Batch(m.pending, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		var ds node.Dirs
		if err := x.To.Call(ctx, node.MDirs, node.DirsParams{Path: dir}, &ds); err != nil {
			return handoffDirsMsg{host: host, err: remote.HandoffRefusal(x.To, host, node.MDirs, err)}
		}
		return handoffDirsMsg{host: host, dirs: ds}
	})
}

func (msg handoffDirsMsg) apply(m *Model) tea.Cmd {
	d := m.ov.handoff
	if m.ov.kind != ovHandoff || d == nil || d.host != msg.host {
		return nil
	}
	if msg.err != "" {
		m.flash(msg.err)
		return nil
	}
	saved := m.ov
	const up, here = "\x00up", "\x00here"
	var items []item
	if msg.dirs.Path != "" {
		items = append(items, item{name: here, label: i18n.F("handoff.dir_this", msg.dirs.Path)})
	}
	for _, x := range msg.dirs.Dirs {
		label := x.Path
		if x.Git {
			label += "  ·  git"
		}
		items = append(items, item{name: x.Path, label: label})
	}
	if msg.dirs.Path != "" {
		items = append(items, item{name: up, label: i18n.T("handoff.dir_up")})
	}
	m.openPicker(i18n.F("handoff.pick_dir", msg.host), i18n.T("handoff.dir_type_hint"), items, false, nil, func(m *Model, chosen []string) {
		m.ov = saved
		switch {
		case len(chosen) == 0:
		case chosen[0] == here:
			m.typedHandoffDir(msg.dirs.Path)
		case chosen[0] == up:
			m.browseHandoffDir(msg.dirs.Parent)
		case slices.ContainsFunc(msg.dirs.Dirs, func(x node.Dir) bool { return x.Path == chosen[0] }):
			m.browseHandoffDir(chosen[0])
		default:
			m.typedHandoffDir(chosen[0])
		}
	})
	m.ov.parse = func(q string) (item, bool) {
		q = strings.TrimSpace(q)
		if !pathmap.Abs(q) {
			return item{}, false
		}
		return item{name: q, label: i18n.F("handoff.dir_use", q)}, true
	}
	m.ov.back = func(mm *Model) { mm.ov = saved }
	return nil
}

// handoffSSH: whether the chosen machine is reached over ssh from here, and why not. Mode 2 goes over ssh only to a host
// of the same name that is that machine (its node id matches), checked once.
func (m *Model) handoffSSH() (bool, string) {
	d := m.ov.handoff
	if d == nil || d.host == "" {
		return true, ""
	}
	if m.far.nc == nil {
		if _, ok := m.hosts.Host(d.host); ok {
			return true, ""
		}
		return false, i18n.F("handoff.no_ssh", d.host, keyName("enter"))
	}
	if same, known := m.far.same[d.host]; known {
		if same {
			return true, ""
		}
		return false, i18n.F("handoff.no_ssh", d.host, keyName("enter"))
	}
	if _, ok := m.far.ssh.Host(d.host); ok {
		return false, i18n.F("remote.ssh_checking", d.host)
	}
	return false, i18n.F("handoff.no_ssh", d.host, keyName("enter"))
}

// checkSSHFor asks, in mode 2, whether the host of name here is that machine, unless that is known.
func (m *Model) checkSSHFor(name string) tea.Cmd {
	if name == "" || m.far.nc == nil {
		return nil
	}
	if _, known := m.far.same[name]; known {
		return nil
	}
	if _, ok := m.far.ssh.Host(name); !ok {
		return nil
	}
	return m.checkSSH(nil, name)
}

// handoffReady is why nothing can open yet: the machine still being read, or no directory chosen there.
func (m *Model) handoffReady() string {
	d := m.ov.handoff
	switch {
	case d.reading:
		return i18n.F("handoff.reading", hostLabel(d.want))
	case d.dir == "":
		return i18n.F("handoff.need_dir", keyOf(inHandoff, actDir))
	case d.comparing:
		return i18n.F("handoff.env_reading", hostLabel(d.host))
	}
	return ""
}

// putHandoff writes the pack (as edited) on the chosen machine for provider ("" the session's own, the command copied).
func (m *Model) putHandoff(provider string) {
	if why := m.handoffReady(); why != "" {
		m.flash(why)
		return
	}
	d := m.ov.handoff
	b, err := os.ReadFile(m.ov.title)
	if err != nil {
		m.flash(i18n.F("handoff.failed", err.Error()))
		return
	}
	x, text, dir, seq, host := d.x, string(b), d.dir, d.seq, d.host
	p := cmp.Or(provider, m.ovRec().Provider)
	rec := m.handoffRecord()
	put := func(cl *coord.Client) tea.Cmd {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
			defer cancel()
			res, err := x.Put(ctx, text, dir, p)
			if err != nil {
				return handoffPutMsg{seq: seq, host: host, err: remote.HandoffRefusal(x.To, hostLabel(host), remote.MHandoffPut, err)}
			}
			msg := handoffPutMsg{seq: seq, host: host, put: res, provider: provider}
			if rec != nil { // before the new session opens: opening in this terminal ends the TUI
				msg.recorded, msg.recordErr = rec(ctx, cl)
			}
			return msg
		}
	}
	m.flash(i18n.F("handoff.putting", hostLabel(host)))
	if rec == nil {
		m.pending = tea.Batch(m.pending, put(nil))
		return
	}
	m.pending = tea.Batch(m.pending, m.withCoord(func(m *Model) tea.Cmd { return put(m.tasks.cl) }))
}

func (msg handoffPutMsg) apply(m *Model) tea.Cmd {
	d := m.ov.handoff
	if m.ov.kind != ovHandoff || d == nil || msg.seq != d.seq {
		return nil
	}
	if msg.err != "" {
		m.flash(msg.err)
		return nil
	}
	var rec tea.Cmd
	switch {
	case msg.recordErr != nil:
		m.flash(i18n.F("tasks.failed", reasonText(msg.recordErr)))
	case msg.recorded != "":
		m.flash(msg.recorded)
		rec = func() tea.Msg { return projDoneMsg{note: msg.recorded} }
	}
	title := i18n.F("handoff.title", m.ovRec().Title)
	switch {
	case msg.provider == "":
		line := remote.HandoffOpenLine(msg.put.ID)
		if err := copyText(line); err != nil {
			m.flash(i18n.F("handoff.failed", err.Error()))
			return rec
		}
		m.closeOverlay()
		m.flash(i18n.F("handoff.put_copied", msg.host, line))
	case msg.host == "":
		r := &tend.Rec{Provider: msg.provider, Cwd: d.dir, Title: title}
		p, err := capture.PlanStart(r, msg.provider, capture.HandoffPrompt(msg.put.Path), false)
		if err != nil {
			m.flash(err.Error())
			return rec
		}
		m.ov.rec = r
		m.startPlan(r, p, false)
	default:
		h := m.hosts
		if m.far.nc != nil {
			h = m.far.ssh
		}
		host, _ := h.Host(msg.host)
		r := &tend.Rec{Title: title, Host: msg.host}
		m.ov.rec = r
		m.startPlan(r, capture.Plan{Spec: remote.HandoffThere(host, msg.put.ID), Ws: herdrHere()}, false)
	}
	return rec
}

// handoffTerminal opens the new session with provider: here in this machine's way, there over ssh.
func (m *Model) handoffTerminal(provider string) {
	if !m.handoffAway() {
		m.startHandoff(provider)
		return
	}
	if ok, why := m.handoffSSH(); !ok {
		m.flash(why)
		return
	}
	m.putHandoff(provider)
}

// handoffCopy copies the pack here; for another machine it writes the pack there and copies the command that opens it.
func (m *Model) handoffCopy() {
	if m.ov.handoff == nil || m.ov.handoff.host == "" {
		m.copyHandoff()
		return
	}
	m.putHandoff("")
}

// handoffTaskWhy is the i18n key of why the pack cannot become a task now, "" when it can.
func (m *Model) handoffTaskWhy() string {
	switch s := &m.proj.snap; {
	case m.tasks.connect == nil, m.served() && (m.tasks.cl == nil || !s.Ready()):
		return "resume.task_offline"
	}
	return ""
}

// handoffTask opens the task form filled from the pack: the machine and directory chosen, the session's CLI, its project.
func (m *Model) handoffTask() tea.Cmd {
	if why := m.handoffReady(); why != "" {
		m.flash(why)
		return nil
	}
	b, err := os.ReadFile(m.ov.title)
	if err != nil {
		m.flash(i18n.F("handoff.failed", err.Error()))
		return nil
	}
	d, r := m.ov.handoff, m.ovRec()
	machine := d.host
	if machine == "" {
		machine = m.makeMachine()
	}
	project := ""
	if p := m.proj.snap.Of(r); p != nil {
		project = p.ID
	}
	record := m.recordHandoff()
	title, brief, dir := i18n.F("handoff.title", r.Title), capture.HandoffBrief(string(b)), d.dir
	return tea.Batch(record, m.withCoord(func(m *Model) tea.Cmd {
		cmd := m.openTaskForm(nil)
		if m.ov.kind != ovTaskForm {
			return cmd
		}
		m.ov.edit.SetValue(title)
		m.ov.edit2.SetValue(dir)
		m.ov.area.SetValue(brief)
		if !slices.Contains(m.ov.opts[0], machine) { // the coordinator has not listed it: never another machine in its place
			m.ov.opts[0] = append(m.ov.opts[0], machine)
		}
		m.ov.pick[0] = choice(m.ov.opts[0], machine)
		m.ov.pick[1] = choice(m.ov.opts[1], r.Provider)
		m.ov.project, m.ov.dispatch = project, true
		return cmd
	}))
}

// handoffRecord is the recording ticked in the dialog: the chosen directory into the session's project, or a new
// personal project holding both directories when the session is in none; nil when nothing is to be recorded.
func (m *Model) handoffRecord() func(ctx context.Context, cl *coord.Client) (string, error) {
	d := m.ov.handoff
	if d == nil || !d.record || !m.canRecord() || d.x == nil {
		return nil
	}
	r, s := m.ovRec(), &m.proj.snap
	url := cmp.Or(d.x.Facts.Git.Remote, r.GitRemote)
	dirs := []coord.ProjectAttach{{Machine: cmp.Or(d.host, s.Here), Dir: d.dir, Remote: url}}
	id, name := "", cmp.Or(r.Project, r.Title)
	if p := s.Of(r); p != nil {
		id, name = p.ID, p.Name
	} else {
		dirs = append([]coord.ProjectAttach{{Machine: s.Machine(r), Dir: projects.Dir(r), Remote: url}}, dirs...)
	}
	note := i18n.F("handoff.recorded", d.dir, name)
	return func(ctx context.Context, cl *coord.Client) (string, error) {
		if id == "" {
			var p task.Project
			if err := cl.CallCommand(ctx, coord.MProjectCreate, commandID(), coord.ProjectCreate{Name: name}, &p); err != nil {
				return "", err
			}
			id = p.ID
		}
		for _, x := range dirs {
			x.Project = id
			if err := cl.CallCommand(ctx, coord.MProjectAttach, commandID(), x, nil); err != nil {
				return "", err
			}
		}
		return note, nil
	}
}

// canRecord: the dialog's directory can go into a project: it was found or typed (not the project's own), and a
// coordinator can be reached.
func (m *Model) canRecord() bool {
	d := m.ov.handoff
	return d != nil && d.recordable && m.tasks.connect != nil
}

// recordHandoff runs the ticked recording on its own.
func (m *Model) recordHandoff() tea.Cmd {
	rec := m.handoffRecord()
	if rec == nil {
		return nil
	}
	return m.withCoord(func(m *Model) tea.Cmd {
		cl := m.tasks.cl
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
			defer cancel()
			note, err := rec(ctx, cl)
			return projDoneMsg{note: note, err: err}
		}
	})
}

// projectName is project id's name, the id itself while the projects are not read.
func (m *Model) projectName(id string) string {
	for _, p := range m.proj.snap.Projects {
		if p.ID == id {
			return p.Name
		}
	}
	return id
}
