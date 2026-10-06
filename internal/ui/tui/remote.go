package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Other machines' sessions: each host's cached list at start, fetched once in the background, then every hostsEvery
// while the host: filter shows the host (longer after failures, up to hostsMaxEvery). They are read-only here: resume
// (over ssh) and copy the command, nothing that writes or opens local things.

const (
	hostsEvery    = 30 * time.Second
	hostsMaxEvery = 5 * time.Minute
	hostTimeout   = 30 * time.Second
)

// hostRows is one host's list; rows are reused per session key across fetches (cursor, pin and probes key by pointer).
type hostRows struct {
	recs    []*tend.Rec
	byKey   map[string]*tend.Rec
	live    map[string]capture.Live
	at      time.Time // when the list shown was fetched
	err     error     // the last fetch failed: the list is the cached one
	liveErr error     // who runs there is not known now: live is the last answer
	fails   int       // fetches failed in a row
	loading bool
	idle    bool // not polled: the filter does not show the host
}

func (hr *hostRows) merge(fresh []*tend.Rec) {
	next := make(map[string]*tend.Rec, len(fresh))
	out := make([]*tend.Rec, 0, len(fresh))
	for _, f := range fresh {
		k := f.Key()
		if r := hr.byKey[k]; r != nil {
			*r = *f
			f = r
		}
		next[k] = f
		out = append(out, f)
	}
	hr.recs, hr.byKey = out, next
}

// useHosts shows h's machines next to this one's sessions; nil or no hosts leaves the TUI as it is.
func (m *Model) useHosts(h *remote.Hosts) {
	names := h.Names()
	if len(names) == 0 {
		return
	}
	m.hosts, m.remote = h, make(map[string]*hostRows, len(names))
	for _, name := range names {
		recs, st := h.Cached(name)
		hr := &hostRows{at: st.At}
		hr.merge(recs)
		m.remote[name] = hr
	}
	m.refresh()
}

type hostMsg struct {
	name    string
	recs    []*tend.Rec
	st      remote.State
	live    map[string]capture.Live
	liveErr error
	os      string // its GOOS, as its hello said
}

type hostTickMsg string

func (m *Model) fetchHosts() tea.Cmd {
	var cmds []tea.Cmd
	for _, name := range m.hosts.Names() {
		cmds = append(cmds, m.fetchHost(name))
	}
	return tea.Batch(cmds...)
}

// fetchHost reads name's list and who runs there, off the main loop; at most one fetch per host at a time.
func (m *Model) fetchHost(name string) tea.Cmd {
	hr := m.remote[name]
	if hr == nil || hr.loading || m.far.nc != nil && m.tasks.cl == nil { // mode 2 reads through the server's connection
		return nil
	}
	hr.loading = true
	h := m.hosts
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		msg := hostMsg{name: name}
		msg.recs, msg.st = h.Sessions(ctx, name)
		msg.liveErr = msg.st.Err
		if msg.st.Err == nil {
			msg.live, msg.liveErr = h.Live(ctx, name)
			if hello, err := h.Hello(ctx, name); err == nil { // reached just now: no dial
				msg.os = hello.OS
			}
		}
		return msg
	}
}

func (m *Model) applyHost(msg hostMsg) tea.Cmd {
	hr := m.remote[msg.name]
	if hr == nil {
		return nil
	}
	hr.loading, hr.err = false, msg.st.Err
	m.noteHostOS(msg.name, msg.os)
	if hr.liveErr = msg.liveErr; msg.liveErr == nil { // unknown is not "nothing runs": keep the last answer
		hr.live = msg.live
	}
	if msg.st.Err != nil {
		hr.fails = min(hr.fails+1, 4)
	} else {
		hr.fails = 0
	}
	if msg.st.Err == nil || len(msg.recs) > 0 {
		hr.merge(msg.recs)
		hr.at = msg.st.At
	}
	var retry tea.Cmd
	if msg.st.Err == nil {
		for r, p := range m.probes {
			if p.failed && r.Host == msg.name {
				delete(m.probes, r)
			}
		}
		retry = m.probeCurrent()
	}
	m.refresh()
	name, wait := msg.name, min(hostsEvery<<hr.fails, hostsMaxEvery)
	return tea.Batch(retry, tea.Tick(wait, func(time.Time) tea.Msg { return hostTickMsg(name) }))
}

// hostTick fetches name again while the filter shows it; otherwise it rests until wakeHosts.
func (m *Model) hostTick(name string) tea.Cmd {
	if hr := m.remote[name]; hr != nil && !hostSelected(m.query(), name) {
		hr.idle = true
		return nil
	}
	return m.fetchHost(name)
}

// wakeHosts fetches the resting hosts the filter has come to show.
func (m *Model) wakeHosts() tea.Cmd {
	if m.hosts == nil {
		return nil
	}
	q := m.query()
	var cmds []tea.Cmd
	for name, hr := range m.remote {
		if hr.idle && hostSelected(q, name) {
			hr.idle = false
			cmds = append(cmds, m.fetchHost(name))
		}
	}
	return tea.Batch(cmds...)
}

// remoteList: the rows of the hosts q.Host selects; status:live asks each host's own live map. Trash, one-shot agent
// runs and message search are this machine's only.
func (m *Model) remoteList(q tend.Query) []*tend.Rec {
	if len(m.remote) == 0 || q.Host == "" || q.Host == tend.HostLocal || q.Status == tend.StatusTrash || q.Status == tend.StatusAgent || m.msgMode() {
		return nil
	}
	var out []*tend.Rec
	for _, name := range m.hosts.Names() {
		hr := m.remote[name]
		hq := q
		hq.Live = func(id string) bool { _, ok := hr.live[id]; return ok }
		m.lists.Place(hr.recs...)
		for _, r := range hr.recs {
			if hq.Match(r) {
				out = append(out, r)
			}
		}
	}
	return out
}

// hostSelected: the host: filter shows name's rows.
func hostSelected(q tend.Query, name string) bool {
	return q.Host == tend.HostAll || strings.EqualFold(q.Host, name)
}

// hostName is the configured spelling of the host: value v (the query is lowercased).
func (m *Model) hostName(v string) string {
	for _, name := range m.hosts.Names() {
		if strings.EqualFold(name, v) {
			return name
		}
	}
	return v
}

func (m *Model) hostChipValue(q tend.Query) string {
	switch q.Host {
	case "", tend.HostLocal:
		return i18n.T("remote.local")
	case tend.HostAll:
		return i18n.T("remote.all")
	}
	return m.hostName(q.Host)
}

// hostDown is "mba offline · 5 min ago": a host whose last fetch failed and the age of the list shown.
func (m *Model) hostDown(name string) string {
	hr := m.remote[name]
	if hr != nil && hr.err == nil && hr.liveErr != nil && m.view == viewLive {
		return i18n.F("remote.live_unknown", name, remote.Reason(hr.liveErr))
	}
	if hr == nil || hr.err == nil {
		return ""
	}
	if hr.at.IsZero() || m.now.Sub(hr.at) < time.Minute { // no list, or one too fresh to have an age
		return i18n.F("remote.down", name, remote.Reason(hr.err))
	}
	return i18n.F("remote.down_since", name, remote.Reason(hr.err), render.ShortDur(m.now.Sub(hr.at)))
}

// offlineNote: the hosts the filter shows that cannot be reached, for the header.
func (m *Model) offlineNote() string {
	if m.hosts == nil {
		return ""
	}
	if err := m.serverDown(); err != nil { // every machine read through it is down: one sentence, not one per machine
		text := i18n.F("remote.server_down", remote.Reason(err))
		if m.view == viewProjects {
			text = i18n.T("remote.server_down_projects")
		}
		return warnSty.Render(render.GlyphWarn + " " + text)
	}
	q := tend.Parse(m.search.Value())
	var parts []string
	for _, name := range m.hosts.Names() {
		if s := m.hostDown(name); s != "" && hostSelected(q, name) {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return warnSty.Render(render.GlyphWarn + " " + strings.Join(parts, "  ·  "))
}

func (m *Model) pickHost() {
	if m.hosts == nil {
		return
	}
	q := tend.Parse(m.search.Value())
	items := []item{{name: "", label: i18n.T("remote.local")}, {name: tend.HostAll, label: i18n.T("remote.all")}}
	for _, name := range m.hosts.Names() {
		label := name
		switch s := m.hostDown(name); {
		case m.shared(name) && m.remote[name].err != nil:
			label = i18n.F("remote.shared_host_down", name, m.ownerOf(name), remote.Reason(m.remote[name].err))
		case m.shared(name):
			label = i18n.F("remote.shared_host", name, m.ownerOf(name))
		case s != "":
			label = s
		}
		items = append(items, item{name: name, label: label, count: len(m.remote[name].recs)})
	}
	cur := ""
	if q.Host != tend.HostLocal {
		cur = m.hostName(q.Host)
	}
	hint := ""
	if m.far.nc != nil {
		hint = i18n.T("remote.picker_sub")
	}
	m.openPicker(i18n.T("remote.picker_title"), hint, items, false, []string{cur},
		func(m *Model, chosen []string) {
			var toks []string
			if len(chosen) > 0 && chosen[0] != "" {
				toks = []string{"host:" + chosen[0]}
			}
			m.setQuery(toks, tend.HasPrefix("host:", "machine:"))
		})
	if m.far.nc != nil {
		m.ov.note = i18n.T("remote.manage_web")
	}
}

// remoteRow: the action would act on another machine's session (or a project group holding one).
func (m *Model) remoteRow() bool {
	if r := m.current(); r != nil {
		return r.Host != ""
	}
	if m.view == viewProjects {
		for _, r := range m.groups[m.groupUnderCursor()] {
			if r.Host != "" {
				return true
			}
		}
	}
	return false
}

// remoteBlocked: what writes tend's records or opens local things; not offered on another machine's session.
func remoteBlocked(a act) bool {
	switch a {
	case actFavorite, actDone, actArchive, actEdit, actMove, actDelete, actNew, actPeek, actHandled, actSnooze, actCloseTab, actTitle:
		return true
	}
	return false
}

// openRemoteResume: resume over ssh (a new Herdr tab when tend runs inside Herdr, else this terminal) or, when this
// machine cannot reach that one, the command to run there. Mode 2 goes over ssh only to the viewer's own machine whose
// host of the same name here has its node id; a machine shared by another person is read, never resumed.
func (m *Model) openRemoteResume(r *tend.Rec) {
	h := m.hosts
	if m.far.nc != nil {
		if m.shared(r.Host) {
			if m.chatVisible() {
				m.pane, m.projCur = paneChat, 0
			}
			return
		}
		same, known := m.far.same[r.Host]
		if _, ok := m.far.ssh.Host(r.Host); ok && !known {
			m.askSSH(r)
			return
		}
		if !same {
			m.openRunThere(r)
			return
		}
		h = m.far.ssh
	}
	cmd, ok := h.ResumeCommand(r)
	if !ok {
		m.openRunThere(r)
		return
	}
	plan := capture.Plan{Spec: agent.CommandSpec{Exec: cmd.Args[0], Args: cmd.Args[1:]}, Ws: herdrHere()}
	if p := m.probes[r]; p != nil && p.done {
		plan.Checks = p.checks
	}
	ti := newInput()
	ti.SetValue(r.Title)
	m.ov = overlay{kind: ovResume, rec: r, plan: plan, edit: ti, focus: -1}
}

// openRunThere shows the command to run on r's machine, quoted for that machine's shell, to copy.
func (m *Model) openRunThere(r *tend.Rec) {
	spec, err := agent.ResumeOf(r, "")
	if err != nil {
		m.flash(i18n.F("remote.unreachable", r.Host, remote.Reason(&wire.Error{Code: wire.CodeNotFound})))
		return
	}
	sh := shell.POSIX // the shell of that machine, not this one's
	for _, mc := range m.tasks.machines {
		if mc.Name == r.Host && mc.OS == "windows" {
			sh = shell.PowerShell
		}
	}
	m.ov = overlay{kind: ovResume, rec: r, plan: capture.Plan{Spec: spec}, there: sh.Line(spec.Cwd, spec.Argv()), focus: -1}
}

// thereNote says why the resume dialog offers the command to run there.
func (m *Model) thereNote(r *tend.Rec) string {
	if _, ok := m.far.ssh.Host(r.Host); ok {
		return i18n.F("remote.run_there_other", r.Host, r.Host)
	}
	return i18n.F("remote.run_there_note", r.Host, r.Host)
}

// herdrHere is the Herdr workspace tend runs in, nil outside Herdr.
func herdrHere() *herdr.Workspace {
	if !herdr.Active() || !herdr.Reachable() {
		return nil
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

func (m *Model) remoteGroups() []btnGroup {
	k := func(a act, text string) string { return keyed(keyOf(inResume, a), i18n.T(text)) }
	if m.ov.there != "" {
		copyThere := btn{keyed(keyOf(inResume, actEnter), i18n.T("remote.btn_copy_there")), true, (*Model).copyResume}
		return append([]btnGroup{{label: i18n.T("resume.group.resume"), bs: []btn{copyThere}, end: []btn{cancelBtn()}}}, m.taskGroup(m.ov.rec)...)
	}
	bs := []btn{{keyed(keyOf(inResume, actEnter), i18n.T("resume.btn_resume")), true, func(mm *Model) { mm.remoteResume(false) }}}
	if m.ov.plan.Ws != nil {
		bs = append(bs, btn{k(actTerminal, "resume.btn_terminal"), false, func(mm *Model) { mm.remoteResume(true) }})
	}
	bs = append(bs, btn{k(actCopy, "resume.btn_copy"), false, (*Model).copyResume})
	return append([]btnGroup{{label: i18n.T("resume.group.resume"), bs: bs, end: []btn{cancelBtn()}}}, m.taskGroup(m.ov.rec)...)
}

// remoteResume runs the ssh resume in a new tab of this Herdr workspace, or quits and runs it in this terminal.
func (m *Model) remoteResume(here bool) {
	if m.ov.there != "" {
		m.copyResume()
		return
	}
	r, p := m.ov.rec, m.ov.plan
	cp := *r // ⚠️ the row is refreshed in place on the main loop
	if p.Ws == nil || here {
		spec := p.Spec
		m.finish(Result{Start: &spec})
		return
	}
	m.ov = overlay{}
	m.flash(i18n.F("resume.opening_tab", p.Ws.Label))
	m.pending = func() tea.Msg {
		msg, warn, err := p.RunInHerdr(&cp)
		return herdrDoneMsg{rec: r, msg: msg, warn: warn, err: err}
	}
}

// remoteTarget: "mba  →  ~/dev/x  →  ssh".
func (m *Model) remoteTarget(r *tend.Rec, arrow string) string {
	dir := r.Cwd
	if dir == "" {
		dir = i18n.T("resume.where.cwd")
	}
	where := r.Host
	if ws := m.ov.plan.Ws; ws != nil && m.ov.rec == r {
		where = "Herdr " + ws.Label + arrow + i18n.T("resume.where.new_tab") + arrow + r.Host
	}
	via := "ssh"
	if m.ov.there != "" && m.ov.rec == r || !m.sshHere(r.Host) {
		via = i18n.T("remote.run_there_title")
	}
	return where + arrow + dir + arrow + via
}
