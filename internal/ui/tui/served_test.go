package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/coordtest"
	fx "github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

const testServer = "https://tend.example"

// servedRig: Ann's TUI in mode 2 on her machine ann-mac; she owns mba too, Bob owns bobs and lets her read it. Every
// node answers with the same fixture machine's sessions.
type servedRig struct {
	m     *Model
	tm    *coordtest.Team
	nodes map[string]*node.Node
	d     *fx.Dataset
	mu    sync.Mutex
	cl    *coord.Client // the connection node.call goes through, as cmd/tend's link holds it
	down  bool          // connecting fails
}

func (s *servedRig) call(ctx context.Context, machine, method string, params json.RawMessage, out any) error {
	s.mu.Lock()
	cl := s.cl
	s.mu.Unlock()
	if cl == nil {
		return &wire.Error{Code: wire.CodeOffline}
	}
	return cl.Call(ctx, coord.MNodeCall, coord.NodeCall{Machine: machine, Method: method, Params: params}, out)
}

// sshAnswer is a config.hosts entry answering hello with a node id.
type sshAnswer struct{ nodeID string }

func (a sshAnswer) Handle(_ context.Context, method string, _ json.RawMessage) (any, error) {
	if method == remote.MHello {
		return remote.Hello{Proto: wire.Proto, Version: "test", NodeID: a.nodeID}, nil
	}
	return nil, &wire.Error{Code: wire.CodeUnknownMethod}
}

// newServedRig: ssh maps config.hosts names to the node id each answers (nil: no config.hosts).
func newServedRig(t *testing.T, ssh map[string]string) *servedRig {
	t.Helper()
	d, err := fx.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	ann, bob := coord.User{ID: "u_ann", Name: "Ann"}, coord.User{ID: "u_bob", Name: "Bob"}
	s := &servedRig{tm: coordtest.NewTeam(t, ann, bob), d: d, nodes: map[string]*node.Node{}}
	for name, owner := range map[string]string{"ann-mac": ann.ID, "mba": ann.ID, "bobs": bob.ID} {
		s.nodes[name] = node.New(t.TempDir())
		s.tm.Attach(name, owner, s.nodes[name])
	}
	os.MkdirAll(filepath.Join(d.Home, "node"), 0o700)
	os.WriteFile(filepath.Join(d.Home, "node", "id"), []byte(s.nodes["ann-mac"].ID()+"\n"), 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.tm.Client(coord.Principal{User: bob.ID}, wire.Options{}).CallCommand(ctx, coord.MMachineSessions, "share-bobs",
		task.SessionsSet{Machine: "bobs", Users: []string{ann.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	s.m = s.open(t, ssh)
	return s
}

// open is a new TUI on the rig, as cmd/tend's runTUI builds it.
func (s *servedRig) open(t *testing.T, ssh map[string]string) *Model {
	t.Helper()
	var hosts []tend.Host
	for name := range ssh {
		hosts = append(hosts, tend.Host{Name: name})
	}
	var sshHosts *remote.Hosts
	if len(hosts) > 0 {
		sshHosts = remote.NewHostsDial(hosts, i18n.ZH, func(h tend.Host) (*remote.Client, error) { return remote.Pipe(sshAnswer{ssh[h.Name]}), nil })
		t.Cleanup(sshHosts.Close)
	}
	nc := remote.NewNodeCall(testServer, s.call)
	nc.SetMachines(nc.Kept())
	h := remote.NewHostsOver(nc)
	t.Cleanup(h.Close)
	cfg := tend.DefaultConfig()
	cfg.Coordinator = &tend.CoordinatorConfig{URL: testServer}
	m := New(fixture(t), noIndex(t), cfg, "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m.useOthers(Others{Hosts: h, Server: nc, SSH: sshHosts})
	m.SetCoordinator(func(o wire.Options) (*coord.Client, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.down {
			return nil, &wire.Error{Code: wire.CodeOffline, Detail: "dial tcp: connection refused"}
		}
		s.cl = s.tm.Client(coord.Principal{User: "u_ann"}, o)
		return s.cl, nil
	})
	m.setView(viewSessions)
	return m
}

// start runs Init and the dial it makes.
func (s *servedRig) start(t *testing.T) {
	t.Helper()
	s.m.Init()
	if !s.m.tasks.connecting {
		t.Fatal("mode 2 dials at start")
	}
	pump(s.m, func() tea.Msg { cl, err := s.m.tasks.connect(wire.Options{}); return tasksConnMsg{cl, err} })
}

func names(m *Model) []string {
	out := slices.Clone(m.hosts.Names())
	slices.Sort(out)
	return out
}

func rowsOn(m *Model, host string) []*tend.Rec {
	var out []*tend.Rec
	for _, row := range m.rows {
		if row.rec != nil && row.rec.Host == host {
			out = append(out, row.rec)
		}
	}
	return out
}

// TestServedReadsTheMachinesTheViewerMayRead: the server's machines but this one, own ones cached on disk, the shared
// one in memory only; host:all lists both and a conversation pages through the server.
func TestServedReadsTheMachinesTheViewerMayRead(t *testing.T) {
	s := newServedRig(t, nil)
	m := s.m
	if len(m.hosts.Names()) != 0 {
		t.Fatalf("nothing kept from this server yet: %v", m.hosts.Names())
	}
	s.start(t)
	waitFor(t, m, func() bool { return slices.Equal(names(m), []string{"bobs", "mba"}) })
	if !m.far.mine["mba"] || m.far.mine["bobs"] {
		t.Fatalf("mine: %v", m.far.mine)
	}
	m.search.SetValue("host:all")
	m.refresh()
	waitFor(t, m, func() bool { m.refresh(); return len(rowsOn(m, "mba")) > 0 && len(rowsOn(m, "bobs")) > 0 })
	if _, err := os.Stat(filepath.Join(remote.CacheDir("mba"), "sessions.json")); err != nil {
		t.Fatalf("the own machine's list is kept on disk: %v", err)
	}
	if _, err := os.Stat(remote.CacheDir("bobs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing of the shared machine on disk: %v", err)
	}
	r := rowsOn(m, "bobs")[0]
	if page := m.hosts.Source(r).Messages(-1, 2); page.Err != nil || len(page.Msgs) == 0 {
		t.Fatalf("a shared machine's conversation reads through the server: %v", page.Err)
	}
	m.pickHost()
	var items []string
	for _, it := range m.ov.items {
		items = append(items, it.name)
	}
	if !slices.Equal(items, []string{"", tend.HostAll, "mba", "bobs"}) && !slices.Equal(items, []string{"", tend.HostAll, "bobs", "mba"}) {
		t.Fatalf("the picker: this machine, all, then the server's machines but this one: %v", items)
	}
	if m.ov.hint != i18n.T("remote.picker_sub") {
		t.Fatalf("picker hint: %q", m.ov.hint)
	}
	m.closeOverlay()

	again := s.open(t, nil) // the next start shows the own machine from disk before the server answers
	if got := names(again); !slices.Equal(got, []string{"mba"}) {
		t.Fatalf("kept: %v", got)
	}
}

// cursorOnHost puts the cursor on host's first row.
func cursorOnHost(t *testing.T, m *Model, host string) *tend.Rec {
	t.Helper()
	m.search.SetValue("host:" + host)
	m.refresh()
	waitFor(t, m, func() bool { m.refresh(); return len(rowsOn(m, host)) > 0 })
	r := rowsOn(m, host)[0]
	m.cursor = slices.IndexFunc(m.rows, func(row row) bool { return row.rec == r })
	return r
}

// TestServedResumeGoesOverSSHOnlyToTheSameMachine: the own machine resumes over ssh when the host of its name here
// answers with its node id; another node id, or no such host, gives the command to run there; a shared one is read.
func TestServedResumeGoesOverSSHOnlyToTheSameMachine(t *testing.T) {
	for _, c := range []struct {
		name  string
		ssh   func(s *servedRig) map[string]string
		there bool
	}{
		{"same node", func(s *servedRig) map[string]string { return map[string]string{"mba": s.nodes["mba"].ID()} }, false},
		{"another node", func(*servedRig) map[string]string { return map[string]string{"mba": "someone-else"} }, true},
		{"no ssh host", func(*servedRig) map[string]string { return nil }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newServedRig(t, nil)
			s.m = s.open(t, c.ssh(s))
			m := s.m
			s.start(t)
			waitFor(t, m, func() bool { return len(m.hosts.Names()) == 2 })
			r := cursorOnHost(t, m, "mba")
			key(m, "enter")
			waitFor(t, m, func() bool { return m.ov.kind == ovResume })
			if m.ov.rec != r || (m.ov.there != "") != c.there {
				t.Fatalf("there %q (want %v)", m.ov.there, c.there)
			}
			screen := screenText(m)
			if !c.there {
				if !strings.Contains(strings.Join(m.ov.plan.Spec.Argv(), " "), "resume --terminal --no-herdr "+r.SessionID) {
					t.Fatalf("ssh resume: %v", m.ov.plan.Spec.Argv())
				}
				return
			}
			if !strings.Contains(screen, i18n.T("remote.run_there_title")) || !strings.Contains(screen, i18n.T("remote.btn_copy_there")) {
				t.Fatalf("the dialog offers the command to copy:\n%s", screen)
			}
			var copied string
			old := copyText
			copyText = func(s string) error { copied = s; return nil }
			defer func() { copyText = old }()
			key(m, "enter")
			if copied != m.resumeCommandFor(r) || !strings.Contains(copied, r.SessionID) || m.ov.active() {
				t.Fatalf("Enter copies the command: %q", copied)
			}
		})
	}
}

// resumeCommandFor is the command the dialog would copy for r.
func (m *Model) resumeCommandFor(r *tend.Rec) string {
	m.openRunThere(r)
	defer m.closeOverlay()
	return m.ov.there
}

func TestServedRunThereIsQuotedForThatMachine(t *testing.T) {
	s := newServedRig(t, nil)
	m := s.m
	s.start(t)
	waitFor(t, m, func() bool { return len(m.hosts.Names()) == 2 })
	r := cursorOnHost(t, m, "mba")
	setOS := func(goos string) {
		for i := range m.tasks.machines {
			if m.tasks.machines[i].Name == "mba" {
				m.tasks.machines[i].OS = goos
			}
		}
	}
	setOS("linux")
	posix := m.resumeCommandFor(r)
	setOS("windows")
	if win := m.resumeCommandFor(r); win == posix || !strings.HasPrefix(win, "Set-Location") && !strings.Contains(win, "; ") {
		t.Fatalf("PowerShell on a Windows machine: %q (POSIX %q)", win, posix)
	}
}

// TestServedSharedRowIsReadNotResumed: Enter on a shared machine's row opens its conversation.
func TestServedSharedRowIsReadNotResumed(t *testing.T) {
	s := newServedRig(t, map[string]string{"bobs": "x"})
	m := s.m
	s.start(t)
	waitFor(t, m, func() bool { return len(m.hosts.Names()) == 2 })
	cursorOnHost(t, m, "bobs")
	key(m, "enter")
	if m.ov.active() || m.pane != paneChat {
		t.Fatalf("Enter reads it: overlay %d pane %d", m.ov.kind, m.pane)
	}
}

// TestServedServerDown: unreachable from the start, own machines show their cache marked offline in one sentence and
// the shared one is gone; a connection lost later does the same, and the TUI dials again.
func TestServedServerDown(t *testing.T) {
	s := newServedRig(t, nil)
	s.start(t)
	waitFor(t, s.m, func() bool {
		_, err := os.Stat(filepath.Join(remote.CacheDir("mba"), "sessions.json"))
		return err == nil
	})

	s.down = true
	m := s.open(t, nil)
	s.m = m
	s.start(t)
	if m.serverDown() == nil || !slices.Equal(names(m), []string{"mba"}) {
		t.Fatalf("down %v, machines %v", m.serverDown(), names(m))
	}
	m.search.SetValue("host:all")
	m.refresh()
	screen := screenText(m)
	if !strings.Contains(screen, i18n.F("remote.server_down", remote.Reason(m.serverDown()))) || len(rowsOn(m, "mba")) == 0 {
		t.Fatalf("one sentence and the cache:\n%s", screen)
	}
	if strings.Contains(screen, "mba：") || strings.Contains(screen, "mba:") {
		t.Fatalf("no sentence per machine:\n%s", screen)
	}
	m.pickHost()
	if label := m.ov.items[2].label; !strings.Contains(label, remote.Reason(&wire.Error{Code: wire.CodeOffline})) {
		t.Fatalf("the picker marks it offline: %q", label)
	}
	m.closeOverlay()
	m.setView(viewProjects)
	if !strings.Contains(screenText(m), i18n.T("remote.server_down_projects")) {
		t.Fatalf("the projects view says why its groups are by directory:\n%s", screenText(m))
	}
	m.setView(viewSessions)

	s.down = false
	pump(m, serverRetryMsg{}.apply(m))
	if m.tasks.connecting {
		pump(m, func() tea.Msg { cl, err := m.tasks.connect(wire.Options{}); return tasksConnMsg{cl, err} })
	}
	waitFor(t, m, func() bool { return slices.Equal(names(m), []string{"bobs", "mba"}) && m.serverDown() == nil })

	s.down = true
	m.tasks.cl.Close()
	waitFor(t, m, func() bool { return m.serverDown() != nil })
	if !slices.Equal(names(m), []string{"mba"}) {
		t.Fatalf("a lost connection drops the shared machine: %v", names(m))
	}
}

// TestServedOldServerSaysSoOnce: a machine list without node ids cannot place this machine.
func TestServedOldServerSaysSoOnce(t *testing.T) {
	s := newServedRig(t, nil)
	m := s.m
	s.start(t)
	waitFor(t, m, func() bool { return m.tasks.machinesIn && m.proj.hello != nil })
	old := []coord.Machine{{Name: "ann-mac", Owner: "u_ann"}, {Name: "mba", Owner: "u_ann"}}
	pump(m, tasksMachinesMsg{machines: old}.apply(m))
	if m.notice != i18n.T("remote.old_server") || m.proj.snap.Here != "" {
		t.Fatalf("notice %q, here %q", m.notice, m.proj.snap.Here)
	}
	m.notice = ""
	pump(m, tasksMachinesMsg{machines: old}.apply(m))
	if m.notice != "" {
		t.Fatalf("once: %q", m.notice)
	}
}

// TestServedSharedRowsSayWhoseTheyAre: a shared machine's rows, its detail and the machine picker say 「只读 · <owner>」
// and that it is read here; the viewer's own rows carry no such mark, and the picker says sharing is set on the web.
func TestServedSharedRowsSayWhoseTheyAre(t *testing.T) {
	s := newServedRig(t, nil)
	m := s.m
	s.start(t)
	waitFor(t, m, func() bool { return len(m.hosts.Names()) == 2 && m.ownerName("bobs") == "Bob" })
	own := cursorOnHost(t, m, "mba")
	if strings.Contains(m.hostMark(own), i18n.F("remote.shared", "Bob")) || strings.Contains(screenText(m), i18n.T("remote.read_title")) {
		t.Fatalf("the viewer's own machine is not marked: %q", m.hostMark(own))
	}
	cursorOnHost(t, m, "bobs")
	screen := screenText(m)
	for _, want := range []string{"@bobs  ·  " + i18n.F("remote.shared", "Bob"), i18n.T("remote.read_title"), i18n.F("remote.shared_note", "Bob")} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, i18n.T("card.resume_target")) || strings.Contains(screen, i18n.T("detail.checking")) {
		t.Fatalf("no resume target and no checks for one on a shared row:\n%s", screen)
	}
	if g := m.mainKeys(m.current()); len(g) != 1 || g[0].text != i18n.T("footer.enter_read") {
		t.Fatalf("Enter reads it: %+v", g)
	}
	m.pickHost()
	i := slices.IndexFunc(m.ov.items, func(it item) bool { return it.name == "bobs" })
	if i < 0 || m.ov.items[i].label != i18n.F("remote.shared_host", "bobs", "Bob") {
		t.Fatalf("the picker marks the shared machine: %+v", m.ov.items)
	}
	if !strings.Contains(screenText(m), i18n.T("remote.manage_web")) {
		t.Fatalf("the picker says sharing is set on the web:\n%s", screenText(m))
	}
}

// TestServedSharedRowOffersNothingThatChangesIt walks every list key on a shared row: Enter and Space read it, r and
// 「建成任务」 say why not, what would write is refused naming the owner, and no dialog that resumes, starts, edits or
// deletes opens.
func TestServedSharedRowOffersNothingThatChangesIt(t *testing.T) {
	s := newServedRig(t, nil)
	m := s.m
	s.start(t)
	waitFor(t, m, func() bool { return len(m.hosts.Names()) == 2 && m.ownerName("bobs") == "Bob" })
	r := cursorOnHost(t, m, "bobs")
	was := *r
	readOnly, why := i18n.F("remote.shared_read_only", "Bob"), i18n.F("remote.shared_note", "Bob")
	for _, b := range bindings {
		if b.in&inList == 0 || b.act == actQuit {
			continue
		}
		m.closeOverlay()
		m.setView(viewSessions)
		m.pane, m.notice, m.chipFocus = paneList, "", -1
		cursorOnHost(t, m, "bobs")
		key(m, b.keys[0])
		switch m.ov.kind {
		case ovResume, ovMakeTask, ovEdit, ovConfirm, ovStart, ovPeek, ovHandoff:
			t.Fatalf("%q opened dialog %d on a shared row", b.keys[0], m.ov.kind)
		}
		switch {
		case b.act == actEnter || b.act == actSpace:
			if m.pane != paneChat {
				t.Fatalf("%q reads the conversation: pane %d", b.keys[0], m.pane)
			}
		case b.act == actResume:
			if m.notice != why {
				t.Fatalf("r says why not: %q", m.notice)
			}
		case remoteBlocked(b.act):
			if m.notice != readOnly {
				t.Fatalf("%q is refused: %q", b.keys[0], m.notice)
			}
		}
	}
	if r.Title != was.Title || r.Favorite() != was.Favorite() || r.Status != was.Status || !slices.Equal(r.Tags, was.Tags) {
		t.Fatalf("the row changed: %+v", r)
	}
	m.closeOverlay()
	cursorOnHost(t, m, "bobs")
	m.notice = ""
	pump(m, m.makeTask(m.current()))
	if m.ov.active() || m.notice != why {
		t.Fatalf("「建成任务」 says why not: overlay %d, %q", m.ov.kind, m.notice)
	}
}

// TestServedOwnMachineWritesThroughTheServer: the viewer's own machine takes favorite, done and edit through the
// server's node.call put and writes its own records; a shared one is refused before anything is sent.
func TestServedOwnMachineWritesThroughTheServer(t *testing.T) {
	s := newServedRig(t, nil)
	m := s.m
	s.start(t)
	m.search.SetValue("host:all status:all")
	m.refresh()
	waitFor(t, m, func() bool { m.refresh(); return len(rowsOn(m, "mba")) > 0 && len(rowsOn(m, "bobs")) > 0 })
	r := cursorOnHost(t, m, "mba")
	key(m, "x")
	waitFor(t, m, func() bool { return r.Status == tend.StatusDone })
	key(m, "e")
	if m.ov.kind != ovEdit {
		t.Fatal("e opens the edit dialog on the viewer's own machine")
	}
	m.ov.edit.SetValue("改在 mba 上")
	key(m, "ctrl+s")
	waitFor(t, m, func() bool { return r.Title == "改在 mba 上" })
	if m.notice != i18n.F("edit.saved", "改在 mba 上") {
		t.Fatalf("%q", m.notice)
	}
	st, err := tend.OpenAt(filepath.Join(s.d.Home, "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if rec := st.BySession(r.Provider, r.SessionID); rec == nil || rec.Title != "改在 mba 上" || rec.Status != tend.StatusDone {
		t.Fatalf("mba's node wrote its records: %+v", rec)
	}
	if len(m.store.All()) != len(fixture(t).All()) {
		t.Fatal("nothing is written to this machine's records")
	}

	bob := cursorOnHost(t, m, "bobs")
	was := bob.Favorite()
	key(m, "f")
	if m.notice != i18n.F("remote.shared_read_only", m.ownerName("bobs")) || bob.Favorite() != was {
		t.Fatalf("a shared machine stays read only: %q", m.notice)
	}

	s.down = true
	m = s.open(t, nil)
	s.m = m
	s.start(t)
	m.search.SetValue("host:all status:all")
	m.refresh()
	cursorOnHost(t, m, "mba")
	key(m, "f")
	if want := i18n.F("remote.put_server_down", remote.Reason(m.serverDown())); m.notice != want {
		t.Fatalf("the server out of reach says why: %q, want %q", m.notice, want)
	}
}

// TestServedForgetsAMachineNoLongerTheViewers: when the server's list gives a machine kept on disk to another person,
// its kept list goes and its rows are marked shared; an old server's list, which cannot say, leaves it.
func TestServedForgetsAMachineNoLongerTheViewers(t *testing.T) {
	s := newServedRig(t, nil)
	m := s.m
	s.start(t)
	kept := func() bool {
		_, err := os.Stat(filepath.Join(remote.CacheDir("mba"), "sessions.json"))
		return err == nil
	}
	waitFor(t, m, kept)
	waitFor(t, m, func() bool { return m.tasks.machinesIn && m.proj.hello != nil })
	ms := slices.Clone(m.tasks.machines)
	pump(m, tasksMachinesMsg{machines: []coord.Machine{{Name: "ann-mac", Owner: "u_ann"}, {Name: "mba", Owner: "u_bob"}}}.apply(m))
	if !kept() {
		t.Fatal("an old server's list says nothing of who owns what")
	}
	for i := range ms {
		if ms[i].Name == "mba" {
			ms[i].Owner = "u_bob"
		}
	}
	pump(m, tasksMachinesMsg{machines: ms}.apply(m))
	if kept() || m.far.mine["mba"] {
		t.Fatalf("mba is Bob's now: kept %v, mine %v", kept(), m.far.mine)
	}
	if again := s.open(t, nil); len(again.hosts.Names()) != 0 {
		t.Fatalf("nothing of it starts the next TUI: %v", again.hosts.Names())
	}
}

// TestNamesAreAskedOncePerConnection: the owners' names come in one batch and are kept; an id not known yet is asked
// alone; a reset of the state stream forgets them, and a server without people.names leaves ids.
func TestNamesAreAskedOncePerConnection(t *testing.T) {
	s := newServedRig(t, nil)
	m := s.m
	s.start(t)
	waitFor(t, m, func() bool { return m.ownerName("bobs") == "Bob" && m.nameOf("u_ann") == "Ann" })
	if m.askNames() != nil {
		t.Fatal("the names known are not asked again")
	}
	m.tasks.machines = append(m.tasks.machines, coord.Machine{Name: "carols", Owner: "u_carol"})
	cmd := m.askNames()
	if cmd == nil || len(m.people.asked) != 3 {
		t.Fatalf("only the new id is asked: %v", m.people.asked)
	}
	if msg := cmd().(namesMsg); !slices.Equal(msg.ids, []string{"u_carol"}) || len(msg.got.Names) != 0 {
		t.Fatalf("one id, which the server does not answer for: %+v", msg)
	} else {
		msg.apply(m)
	}
	if m.ownerName("carols") != "u_carol" || m.askNames() != nil {
		t.Fatal("an id the server does not name stays an id, and is not asked again on this connection")
	}

	tasksPushMsg{cl: m.tasks.cl, gen: m.tasks.gen, pushes: []wire.Push{{Method: coord.PushReset}}}.apply(m)
	if m.nameOf("u_ann") != "u_ann" {
		t.Fatal("a reset forgets the names")
	}
	pump(m, m.askNames())
	if m.ownerName("bobs") != "Bob" {
		t.Fatal("and asks them again")
	}

	m.forgetNames()
	m.proj.hello.Methods = slices.DeleteFunc(slices.Clone(m.proj.hello.Methods), func(x string) bool { return x == coord.MPeopleNames })
	if m.askNames() != nil || m.ownerName("bobs") != "u_bob" {
		t.Fatal("an old server's ids stay ids")
	}
}
