package tui

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// projectsState is where the lists learn which project each session belongs to (internal/projects). Mode 1 reads this
// machine's journal without becoming the coordinator; mode 2 builds it from the Tasks view's connection, dialed at
// start, and knows none while that connection is down or the server is old.
type projectsState struct {
	journal *projects.Journal // mode 1
	snap    projects.Snapshot
	hostOS  map[string]string // mode 1: what the configured machines said in hello
	hello   *remote.Hello     // the coordinator's hello on the current connection
	nodeID  string            // mode 2: this machine's node, to find it among the server's machines
	sig     string            // mode 2: what the last snapshot was built from
	after   []func(*Model) tea.Cmd
}

// served: mode 2, the coordinator is a server.
func (m *Model) served() bool { c := m.cfg.Coordinator; return c != nil && c.URL != "" }

// initProjects picks the source and has the lists ask it; New refreshes afterwards.
func (m *Model) initProjects() {
	m.lists.Belong = func(r *tend.Rec) (string, string) { return m.proj.snap.Belong(r) }
	if m.served() {
		m.proj.snap, m.proj.nodeID = projects.Down(projects.Offline), projects.NodeID(tend.Home())
		return
	}
	m.useJournal(filepath.Join(tend.Home(), "coord", "events.jsonl"))
}

func (m *Model) useJournal(path string) {
	m.proj.journal = projects.NewJournal(path)
	if _, err := m.proj.journal.Read(); err != nil {
		tracef("projects: %v", err)
	}
	m.proj.snap = m.mineSnapshot()
}

func (m *Model) mineSnapshot() projects.Snapshot {
	s := m.proj.journal.Snapshot()
	maps.Copy(s.OS, m.proj.hostOS)
	return s
}

// readProjects takes in what the journal gained (mode 1); the lists are laid out again when a project changed.
func (m *Model) readProjects() {
	if m.proj.journal == nil {
		return
	}
	changed, err := m.proj.journal.Read()
	if err != nil {
		tracef("projects: %v", err)
	}
	if changed {
		m.setProjects(m.mineSnapshot())
	}
}

// hostOS notes the system a configured machine said in hello: its sessions' directories are read by its rules.
func (m *Model) noteHostOS(name, goos string) {
	if goos == "" || m.proj.journal == nil || m.proj.hostOS[name] == goos {
		return
	}
	if m.proj.hostOS == nil {
		m.proj.hostOS = map[string]string{}
	}
	m.proj.hostOS[name] = goos
	m.setProjects(m.mineSnapshot())
}

func (m *Model) setProjects(s projects.Snapshot) {
	m.proj.snap = s
	m.recount()
	m.refresh()
}

// syncServed builds mode 2's snapshot from the connection: its hello, the state and the machines it pushed.
func (m *Model) syncServed() {
	if !m.served() {
		return
	}
	t := &m.tasks
	var s projects.Snapshot
	var sig string
	switch {
	case t.cl == nil || m.proj.hello == nil:
		s, sig = projects.Down(projects.Offline), projects.Offline
	default:
		var ps map[string]*task.Project
		if t.st != nil {
			ps = t.st.Projects
		}
		s = projects.Served(*m.proj.hello, ps, t.machines, m.proj.nodeID)
		sig = servedSig(s, t.machines)
	}
	if sig == m.proj.sig {
		return
	}
	m.proj.sig = sig
	m.setProjects(s)
}

// servedSig: what a mode 2 snapshot depends on, so pushes that change none of it lay nothing out again.
func servedSig(s projects.Snapshot, ms []coord.Machine) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%+v|", s.State, s.Here, s.Viewer)
	for _, id := range slices.Sorted(maps.Keys(s.Projects)) {
		fmt.Fprintf(&b, "%s:%d;", id, s.Projects[id].Rev)
	}
	for _, mc := range ms {
		fmt.Fprintf(&b, "%s/%s/%s/%s;", mc.Name, mc.OS, mc.Owner, mc.NodeID)
	}
	return b.String()
}

type helloMsg struct {
	cl    *coord.Client
	hello remote.Hello
	err   error
}

// readHello asks the coordinator who it takes this connection for and what it can do.
func (m *Model) readHello() tea.Cmd {
	cl := m.tasks.cl
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		var h remote.Hello
		err := cl.Call(ctx, remote.MHello, remote.HelloParams{Proto: wire.Proto, Role: "client"}, &h)
		return helloMsg{cl, h, err}
	}
}

func (msg helloMsg) apply(m *Model) tea.Cmd {
	if m.tasks.cl != msg.cl {
		return nil
	}
	if msg.err != nil {
		tracef("projects: hello: %v", msg.err)
		msg.hello = remote.Hello{}
	}
	m.proj.hello = &msg.hello
	m.syncServed()
	return nil
}

// withCoord runs do once the coordinator is reached: at once when it is, else after connecting (mode 1: this TUI
// becomes the coordinator, as opening the Tasks view does). Mode 2 offline says so.
func (m *Model) withCoord(do func(*Model) tea.Cmd) tea.Cmd {
	if m.tasks.cl != nil {
		return do(m)
	}
	if m.tasks.connect == nil {
		m.flash(i18n.T("tasks.unavailable"))
		return nil
	}
	m.proj.after = append(m.proj.after, do)
	return m.tasksOpen()
}

// connected runs what waited for the connection; failed drops it.
func (m *Model) connected(err error) tea.Cmd {
	after := m.proj.after
	m.proj.after = nil
	if err != nil {
		if len(after) > 0 {
			m.flash(i18n.F("tasks.failed", reasonText(err)))
		}
		m.syncServed()
		return nil
	}
	var cmds []tea.Cmd
	for _, do := range after {
		cmds = append(cmds, do(m))
	}
	return tea.Batch(cmds...)
}

// projectKind is how a project group's header names its kind: team or personal.
func (m *Model) projectKind(id string) string {
	if p := m.proj.snap.Projects[id]; p != nil && !projects.Personal(p) {
		return i18n.T("project.team")
	}
	return i18n.T("project.personal")
}

// groupLabel is the text of group key g.
func (m *Model) groupLabel(g string) string {
	if id := groupProject(g); id != "" {
		if p := m.proj.snap.Projects[id]; p != nil {
			return p.Name
		}
		for _, r := range m.groups[g] {
			return r.ProjectName
		}
		return id
	}
	return g
}

// projectQueryLabel shows a project: value by its project's name when it names one by id.
func (m *Model) projectQueryLabel(v string) string {
	for id, p := range m.proj.snap.Projects {
		if strings.EqualFold(id, v) {
			return p.Name
		}
	}
	return v
}
