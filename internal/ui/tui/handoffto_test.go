package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// handoffNode answers like a machine's tend for a handoff: its hello, its sessions, the facts of each, a put, its
// checkouts of a remote and its directories.
type handoffNode struct {
	mu       sync.Mutex
	hello    remote.Hello
	sessions []remote.Session
	facts    capture.HandoffFacts
	repos    []remote.RepoDir
	dirs     map[string]node.Dirs
	env      envcheck.Print
	puts     []remote.HandoffPutParams
	mem      *memNode
}

const shopRemote = "git@example.com:team/shop.git"

func newHandoffNode(name, goos, home string) *handoffNode {
	cwd := home + "/dev/shop"
	return &handoffNode{
		hello: remote.Hello{Proto: wire.Proto, Version: "test", Endpoint: name, Hostname: name, OS: goos, Home: home, Share: node.ShareAll,
			Methods: []string{remote.MHello, remote.MList, remote.MLive, remote.MHandoffFacts, remote.MHandoffPut, remote.MRepos, node.MDirs, remote.MEnv}},
		sessions: []remote.Session{{Provider: tend.ProviderClaude, SessionID: "far-1", Title: "远端交接源", Project: "shop", Cwd: cwd,
			Turns: 5, Msgs: 10, LastAt: time.Now(), UpdatedAt: time.Now()}},
		facts: capture.HandoffFacts{Provider: tend.ProviderClaude, SessionID: "far-1", Title: "购物车结算重构", Cwd: cwd,
			Summary: "把结算拆成两步。", Requests: []string{"先跑测试"}, Git: capture.HandoffGit{Remote: shopRemote}},
	}
}

func (h *handoffNode) Handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Contains(h.hello.Methods, method) {
		return nil, &wire.Error{Code: wire.CodeUnknownMethod}
	}
	if h.mem != nil && strings.HasPrefix(method, "memory.") {
		return h.mem.handle(method, params)
	}
	switch method {
	case remote.MHello:
		return h.hello, nil
	case remote.MList:
		return remote.List{Sessions: slices.Clone(h.sessions)}, nil
	case remote.MLive:
		return remote.Live{}, nil
	case remote.MHandoffFacts:
		return h.facts, nil
	case remote.MHandoffPut:
		var p remote.HandoffPutParams
		json.Unmarshal(params, &p)
		h.puts = append(h.puts, p)
		id := fmt.Sprintf("h%d", len(h.puts))
		return remote.HandoffPut{ID: id, Path: h.hello.Home + "/.agent/tend/handoff/" + id + ".md"}, nil
	case remote.MRepos:
		return remote.Repos{Dirs: slices.Clone(h.repos)}, nil
	case remote.MEnv:
		return h.env, nil
	case node.MDirs:
		var p node.DirsParams
		json.Unmarshal(params, &p)
		return h.dirs[p.Path], nil
	}
	return nil, &wire.Error{Code: wire.CodeUnknownMethod}
}

func (h *handoffNode) put() []remote.HandoffPutParams {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.puts)
}

func (h *handoffNode) peer() remote.Peer { return remote.PeerOf("", h.hello, remote.InProcess(h)) }

// handoffRig: the TUI on this machine (here) with mba, another of the viewer's machines, reached as its transport does;
// in mode 2 (node.call) bobs is another person's machine the viewer may read.
type handoffRig struct {
	m         *Model
	here      *handoffNode
	mba       *handoffNode
	offline   bool
	noSSH     bool // mode 2: no config.hosts entry for mba
	oldServer bool
}

var handoffTransports = []string{"ssh", "node.call"}

func newHandoffRig(t *testing.T, transport string, set func(*handoffRig)) *handoffRig {
	t.Helper()
	fakeCLIs(t)
	g := &handoffRig{here: newHandoffNode("here", runtime.GOOS, t.TempDir()), mba: newHandoffNode("mba", "linux", "/home/u")}
	if set != nil {
		set(g)
	}
	dial := func(tend.Host) (*remote.Client, error) {
		if g.offline {
			return nil, &wire.Error{Code: wire.CodeOffline}
		}
		return remote.Pipe(g.mba), nil
	}
	ssh := remote.NewHostsDial([]tend.Host{{Name: "mba"}}, i18n.ZH, dial)
	t.Cleanup(ssh.Close)
	offline := func(wire.Options) (*coord.Client, error) { return nil, &wire.Error{Code: wire.CodeOffline} }
	if transport == "ssh" {
		g.m = sized(t, 140, 40)
		g.m.SetCoordinator(offline)
		g.m.useOthers(Others{Here: g.here.peer, Hosts: ssh, SSH: ssh})
		g.m.setView(viewSessions)
		return g
	}
	bobs := newHandoffNode("bobs", "linux", "/home/bob")
	nc := remote.NewNodeCall(testServer, func(ctx context.Context, machine, method string, params json.RawMessage, out any) error {
		var h remote.Handler
		switch {
		case machine == "mba" && !g.offline:
			h = g.mba
		case machine == "bobs":
			h = bobs
		default:
			return &wire.Error{Code: wire.CodeOffline}
		}
		a, err := h.Handle(ctx, method, params)
		if err != nil {
			return err
		}
		b, _ := json.Marshal(a)
		return json.Unmarshal(b, out)
	})
	h := remote.NewHostsOver(nc)
	t.Cleanup(h.Close)
	cfg := tend.DefaultConfig()
	cfg.Coordinator = &tend.CoordinatorConfig{URL: testServer}
	if os.Getenv("TEND_HOME") == "" {
		t.Setenv("TEND_HOME", t.TempDir())
	}
	g.m = New(fixture(t), noIndex(t), cfg, "")
	g.m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	o := Others{Here: g.here.peer, Hosts: h, Server: nc}
	if !g.noSSH {
		o.SSH = ssh
	}
	g.m.SetCoordinator(offline)
	g.m.useOthers(o)
	g.m.setMachines([]remote.Machine{{Name: "mba", Mine: true}, {Name: "bobs"}})
	if !g.noSSH {
		g.m.far.same["mba"] = true
	}
	g.m.tasks.cl = &coord.Client{} // connected: the server's hello says what it forwards
	g.m.proj.hello = &remote.Hello{}
	if !g.oldServer {
		g.m.proj.hello.Features = []string{remote.FeatureMigrate}
	}
	g.m.setView(viewSessions)
	return g
}

// openLocal opens the handoff dialog on this machine's session under the cursor, its pack written here.
func (g *handoffRig) openLocal(t *testing.T) *tend.Rec {
	t.Helper()
	m := g.m
	r := m.current()
	r.Cwd = t.TempDir()
	m.askResume()
	m.screen()
	m.Update(press("s"))
	_, cmd := m.Update(press("s"))
	for msg := range drain(cmd) {
		if h, ok := msg.(handoffMsg); ok {
			m.Update(h)
		}
	}
	if m.ov.kind != ovHandoff {
		t.Fatalf("the handoff dialog is open: %d", m.ov.kind)
	}
	return r
}

// choose picks the machine name in the dialog's machine picker (m).
func (g *handoffRig) choose(t *testing.T, name string) {
	t.Helper()
	key(g.m, "m")
	if g.m.ov.kind != ovPicker {
		t.Fatalf("m opens the machine picker:\n%s", screenText(g.m))
	}
	typeText(g.m, name)
	key(g.m, "enter")
	waitFor(t, g.m, func() bool { return g.m.ov.kind == ovHandoff && !g.m.ov.handoff.reading })
}

// fetchNow reads name's list and hello as fetchHost does, without waiting for mode 2's server connection.
func fetchNow(m *Model, name string) {
	ctx := context.Background()
	msg := hostMsg{name: name}
	msg.recs, msg.st = m.hosts.Sessions(ctx, name)
	if msg.st.Err == nil {
		if hello, err := m.hosts.Hello(ctx, name); err == nil {
			msg.os, msg.hello, msg.lacks = hello.OS, true, lacking(hello)
		}
	}
	m.Update(msg)
}

func checkWidth(t *testing.T, m *Model) {
	t.Helper()
	for i, l := range viewLines(m) {
		if w := ansi.StringWidth(l); w != m.w {
			t.Errorf("line %d is %d wide, not %d", i, w, m.w)
		}
	}
}

// TestHandoffToAnotherMachine: over either transport the dialog reads the session's facts here for mba, finds the
// one checkout of its remote there, regenerates the pack for it (no path of this machine, the directory mapping), and
// 1 opens Claude there over ssh on the pack written there.
func TestHandoffToAnotherMachine(t *testing.T) {
	for _, tr := range handoffTransports {
		t.Run(tr, func(t *testing.T) {
			g := newHandoffRig(t, tr, func(g *handoffRig) {
				g.mba.repos = []remote.RepoDir{{Path: "/home/u/dev/shop", Branch: "main", From: "index"}}
			})
			m := g.m
			g.openLocal(t)
			if s := screenText(m); !strings.Contains(s, i18n.T("handoff.here")) || !strings.Contains(s, "m "+i18n.T("handoff.btn_machine")) {
				t.Fatalf("the dialog names this machine and offers m:\n%s", s)
			}
			g.choose(t, "mba")
			d := m.ov.handoff
			if d.host != "mba" || d.dir != "/home/u/dev/shop" || d.how != "handoff.dir.found_one" {
				t.Fatalf("one checkout is chosen: %+v", d)
			}
			s := screenText(m)
			for _, want := range []string{"mba", "/home/u/dev/shop", i18n.T("handoff.dir.found_one"), i18n.T("handoff.dirs"), "d " + i18n.T("handoff.btn_dir"),
				"x " + i18n.T("handoff.btn_record"), i18n.T("resume.btn_make_task"), "y " + i18n.T("handoff.btn_copy_command")} {
				if !strings.Contains(s, want) {
					t.Errorf("the dialog lacks %q:\n%s", want, s)
				}
			}
			checkWidth(t, m)
			key(m, "1")
			if len(g.mba.put()) != 0 || m.quitting {
				t.Fatal("the first 1 only focuses Claude")
			}
			key(m, "1")
			puts := g.mba.put()
			if len(puts) != 1 || puts[0].Dir != "/home/u/dev/shop" || puts[0].Provider != tend.ProviderClaude || !strings.Contains(puts[0].Text, i18n.T("handoff.dirs")) {
				t.Fatalf("the pack is put on mba for Claude in the checkout: %+v", puts)
			}
			if !m.quitting || m.result.Start == nil {
				t.Fatalf("the new session opens in this terminal over ssh: %+v", m.result)
			}
			if argv := strings.Join(m.result.Start.Argv(), " "); !strings.Contains(argv, "handoff --open h1 --no-herdr") {
				t.Fatalf("ssh runs tend handoff --open there: %s", argv)
			}
		})
	}
}

// TestHandoffDirectoryCandidates: several checkouts wait for d; none offers typing (and browsing through node.dirs).
func TestHandoffDirectoryCandidates(t *testing.T) {
	for _, tr := range handoffTransports {
		t.Run(tr+"/many", func(t *testing.T) {
			g := newHandoffRig(t, tr, func(g *handoffRig) {
				g.mba.repos = []remote.RepoDir{{Path: "/home/u/dev/shop", From: "index"}, {Path: "/home/u/work/shop", From: "scan"}}
			})
			m := g.m
			g.openLocal(t)
			g.choose(t, "mba")
			if m.ov.handoff.dir != "" || !strings.Contains(screenText(m), i18n.F("handoff.dir.found_many", 2, "d")) {
				t.Fatalf("two checkouts: none chosen yet:\n%s", screenText(m))
			}
			key(m, "1")
			key(m, "1")
			if len(g.mba.put()) != 0 || m.notice != i18n.F("handoff.need_dir", "d") {
				t.Fatalf("nothing opens before a directory is chosen: %q", m.notice)
			}
			key(m, "d")
			typeText(m, "work")
			key(m, "enter")
			if m.ov.kind != ovHandoff || m.ov.handoff.dir != "/home/u/work/shop" {
				t.Fatalf("d picks one: %+v", m.ov.handoff)
			}
			if b, _ := os.ReadFile(m.ov.title); !strings.Contains(string(b), "/home/u/work/shop") {
				t.Fatalf("the pack follows the directory:\n%s", b)
			}
		})
		t.Run(tr+"/none", func(t *testing.T) {
			g := newHandoffRig(t, tr, func(g *handoffRig) {
				g.mba.dirs = map[string]node.Dirs{
					"":        {Dirs: []node.Dir{{Name: "u", Path: "/home/u"}}},
					"/home/u": {Path: "/home/u", Dirs: []node.Dir{{Name: "src", Path: "/home/u/src", Git: true}}},
				}
			})
			m := g.m
			g.openLocal(t)
			g.choose(t, "mba")
			if !strings.Contains(screenText(m), i18n.F("handoff.dir.none", "d")) {
				t.Fatalf("no checkout: typed or browsed:\n%s", screenText(m))
			}
			key(m, "d")
			typeText(m, "/srv/shop")
			key(m, "enter")
			if d := m.ov.handoff; d.dir != "/srv/shop" || d.how != "handoff.dir.typed" || !d.recordable {
				t.Fatalf("a typed directory: %+v", d)
			}
			key(m, "d")
			typeText(m, i18n.T("handoff.dir_browse"))
			key(m, "enter")
			waitFor(t, m, func() bool { return m.ov.kind == ovPicker })
			typeText(m, "/home/u")
			if vis := m.ov.visible(); len(vis) == 0 || vis[0].name != "/home/u" {
				t.Fatalf("the roots are listed: %+v", vis)
			}
			m.ov.filter.SetValue("")
			m.ov.cursor = 0
			key(m, "enter") // into /home/u
			waitFor(t, m, func() bool {
				return m.ov.kind == ovPicker && strings.Contains(m.ov.title, "mba") && len(m.ov.items) == 3
			})
			key(m, "enter") // its first row: this folder
			if m.ov.kind != ovHandoff || m.ov.handoff.dir != "/home/u" {
				t.Fatalf("browsing picks a folder there: %+v", m.ov.handoff)
			}
		})
	}
}

// TestHandoffRefusedMachines: an offline machine, one whose tend is too old, another person's machine and a server
// that does not forward handoffs are listed with why, and choosing one only says so.
func TestHandoffRefusedMachines(t *testing.T) {
	cases := []struct {
		name      string
		transport string
		set       func(*handoffRig)
		host      string
		why       func() string
		fetch     bool
	}{
		{"offline/ssh", "ssh", func(g *handoffRig) { g.offline = true }, "mba",
			func() string {
				return i18n.F("remote.unreachable", "mba", remote.Reason(&wire.Error{Code: wire.CodeOffline}))
			}, true},
		{"offline/node.call", "node.call", func(g *handoffRig) { g.offline = true }, "mba",
			func() string {
				return i18n.F("remote.unreachable", "mba", remote.Reason(&wire.Error{Code: wire.CodeOffline}))
			}, true},
		{"old/ssh", "ssh", oldMBA, "mba", func() string { return remote.TooOld("mba", remote.MHandoffPut) }, true},
		{"old unseen/ssh", "ssh", oldMBA, "mba", func() string { return remote.TooOld("mba", remote.MHandoffPut) }, false},
		{"old/node.call", "node.call", oldMBA, "mba", func() string { return remote.TooOld("mba", remote.MHandoffPut) }, true},
		{"shared", "node.call", nil, "bobs", func() string { return i18n.F("cli.handoff.not_mine", "bobs") }, false},
		{"server without migrate", "node.call", func(g *handoffRig) { g.oldServer = true }, "mba", func() string { return i18n.T("cli.handoff.server_old") }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newHandoffRig(t, c.transport, c.set)
			m := g.m
			if c.fetch {
				fetchNow(m, c.host)
			}
			g.openLocal(t)
			key(m, "m")
			if c.fetch || c.name == "shared" || c.name == "server without migrate" {
				if s := screenText(m); !strings.Contains(s, c.why()) {
					t.Fatalf("the picker says why %s cannot take it:\n%s", c.host, s)
				}
			}
			typeText(m, c.host)
			key(m, "enter")
			waitFor(t, m, func() bool { return m.ov.kind == ovHandoff && !m.ov.handoff.reading })
			if m.ov.handoff.host != "" || m.notice != c.why() || len(g.mba.put()) != 0 {
				t.Fatalf("%s is refused with %q, not %q (host %q)", c.host, c.why(), m.notice, m.ov.handoff.host)
			}
		})
	}
}

func oldMBA(g *handoffRig) {
	g.mba.hello.Methods = []string{remote.MHello, remote.MList, remote.MLive, remote.MHandoffFacts}
}

// TestHandoffWithoutSSHCopiesTheCommand: in mode 2 a machine with no ssh host here gets the pack and the command to run
// there, copied by Enter; 1 and 2 are off and say why.
func TestHandoffWithoutSSHCopiesTheCommand(t *testing.T) {
	g := newHandoffRig(t, "node.call", func(g *handoffRig) {
		g.noSSH = true
		g.mba.repos = []remote.RepoDir{{Path: "/home/u/dev/shop", From: "index"}}
	})
	m := g.m
	g.openLocal(t)
	g.choose(t, "mba")
	if s := screenText(m); !strings.Contains(s, i18n.F("handoff.no_ssh", "mba", keyName("enter"))) {
		t.Fatalf("the dialog says ssh does not reach mba:\n%s", s)
	}
	for _, b := range m.ov.btns {
		if strings.HasPrefix(b.label, "1 ") && b.act != nil {
			t.Fatal("1 is off without ssh")
		}
	}
	key(m, "1")
	if len(g.mba.put()) != 0 || m.notice != i18n.F("handoff.no_ssh", "mba", keyName("enter")) {
		t.Fatalf("1 says why: %q", m.notice)
	}
	var copied string
	old := copyText
	copyText = func(s string) error { copied = s; return nil }
	defer func() { copyText = old }()
	key(m, "enter")
	if len(g.mba.put()) != 1 || copied != "tend handoff --open h1" || m.ov.active() {
		t.Fatalf("Enter puts the pack and copies the command: %q", copied)
	}
}

// TestHandoffFromAnotherMachineToHere: a session of mba is handed to this machine: its facts read there, the directory
// found here through this machine's node.repos, and the new session opened here on the pack put here.
func TestHandoffFromAnotherMachineToHere(t *testing.T) {
	for _, tr := range handoffTransports {
		t.Run(tr, func(t *testing.T) {
			dir := t.TempDir()
			g := newHandoffRig(t, tr, func(g *handoffRig) { g.here.repos = []remote.RepoDir{{Path: dir, From: "claude"}} })
			m := g.m
			fetchNow(m, "mba")
			showHosts(m, "host:mba")
			r := cursorOn(t, m, "far-1")
			key(m, "enter")
			m.screen()
			if m.ov.kind != ovResume {
				t.Fatalf("Enter opens the resume dialog:\n%s", screenText(m))
			}
			key(m, "s")
			key(m, "s")
			waitFor(t, m, func() bool { return m.ov.kind == ovHandoff && m.ov.handoff.x != nil })
			checkWidth(t, m)
			if d := m.ov.handoff; d.host != "" || d.dir != dir || m.ov.rec != r {
				t.Fatalf("this machine, its checkout: %+v", d)
			}
			if b, _ := os.ReadFile(m.ov.title); !strings.Contains(string(b), i18n.F("handoff.elsewhere", "mba")) {
				t.Fatalf("the pack says the session is on mba:\n%s", b)
			}
			key(m, "1")
			key(m, "1")
			puts := g.here.put()
			if len(puts) != 1 || puts[0].From.Name != "mba" || puts[0].Ref.SessionID != "far-1" {
				t.Fatalf("the pack is put here, from mba: %+v", puts)
			}
			s := m.result.Start
			if !m.quitting || s == nil || s.Exec != "claude" || s.Cwd != dir || !strings.Contains(strings.Join(s.Args, " "), "h1.md") {
				t.Fatalf("Claude starts here in the checkout on the pack: %+v", s)
			}
		})
	}
}

// TestHandoffSharedSessionStaysShut: a session of another person's machine is never handed off.
func TestHandoffSharedSessionStaysShut(t *testing.T) {
	g := newHandoffRig(t, "node.call", nil)
	m := g.m
	m.askHandoff(&tend.Rec{Provider: tend.ProviderClaude, SessionID: "far-1", Host: "bobs"})
	if m.ov.active() || m.notice != i18n.F("cli.handoff.not_mine", "bobs") {
		t.Fatalf("only a flash: %q", m.notice)
	}
}

// TestHandoffRecordsTheDirectory: x ticks recording the directory into the project; the dialog shows the choice.
func TestHandoffRecordsTheDirectory(t *testing.T) {
	g := newHandoffRig(t, "ssh", func(g *handoffRig) { g.mba.repos = []remote.RepoDir{{Path: "/home/u/dev/shop", From: "index"}} })
	m := g.m
	g.openLocal(t)
	g.choose(t, "mba")
	if m.ov.handoff.record || !strings.Contains(screenText(m), "[ ] "+i18n.T("handoff.record_new")) {
		t.Fatalf("a session in no project: a new one is offered, not ticked:\n%s", screenText(m))
	}
	key(m, "x")
	if !m.ov.handoff.record || !strings.Contains(screenText(m), "[x] "+i18n.T("handoff.record_new")) {
		t.Fatal("x ticks it")
	}
	clickText(t, m, "[x] "+i18n.T("handoff.record_new"))
	if m.ov.handoff.record {
		t.Fatal("a click unticks it")
	}
}

// TestHandoffTaskForm: 「建成任务」 fills the task form from the pack for mba, its directory there, run at once.
func TestHandoffTaskForm(t *testing.T) {
	g := newHandoffRig(t, "ssh", func(g *handoffRig) { g.mba.repos = []remote.RepoDir{{Path: "/home/u/dev/shop", From: "index"}} })
	m := g.m
	m.tasks.machines = []coord.Machine{{Name: coord.Local}, {Name: "mba"}}
	r := g.openLocal(t)
	g.choose(t, "mba")
	m.screen()
	i := slices.IndexFunc(m.ov.btns, func(b btn) bool { return b.label == i18n.T("resume.btn_make_task") })
	if i < 0 || m.ov.btns[i].act == nil {
		t.Fatalf("「建成任务」 is on: %q", screenText(m))
	}
	for range i + 1 {
		m.Update(press("tab"))
		m.screen()
		if m.ov.focus == i {
			break
		}
	}
	if m.ov.focus != i {
		t.Fatalf("Tab reaches 「建成任务」: focus %d", m.ov.focus)
	}
	m.tasks.cl = &coord.Client{}
	m.Update(press("enter"))
	if m.ov.kind != ovTaskForm {
		t.Fatalf("the task form opens:\n%s", screenText(m))
	}
	if m.ov.edit.Value() != i18n.F("handoff.title", r.Title) || m.ov.edit2.Value() != "/home/u/dev/shop" || m.picked(0) != "mba" ||
		!m.ov.dispatch || !strings.Contains(m.ov.area.Value(), i18n.T("handoff.task_ask")) {
		t.Fatalf("filled for mba: %q %q %q", m.ov.edit.Value(), m.ov.edit2.Value(), m.picked(0))
	}
}

// TestHandoffComparesEnvironments: once a directory is chosen on mba the dialog compares the session's environment here
// with that directory's there in the background, as `tend handoff --host` does: the pack gains the comparison, the
// dialog one line of counts, and a block stops nothing. A machine without env leaves the pack and the line saying why.
func TestHandoffComparesEnvironments(t *testing.T) {
	for _, tr := range handoffTransports {
		t.Run(tr, func(t *testing.T) {
			g := newHandoffRig(t, tr, func(g *handoffRig) {
				g.mba.repos = []remote.RepoDir{{Path: "/home/u/dev/shop", From: "index"}}
				g.here.env = envcheck.Print{Dir: g.here.facts.Cwd, CLIs: []envcheck.CLI{{Name: "claude", Found: true, Version: "2.1.292"}},
					Skills: []string{"review"}, Seen: &envcheck.Seen{CLI: tend.ProviderClaude, Version: "2.1.292", Used: []string{"review"}, Known: []string{"skills"}}}
				g.mba.env = envcheck.Print{Dir: "/home/u/dev/shop", CLIs: []envcheck.CLI{{Name: "claude"}}}
			})
			m := g.m
			g.openLocal(t)
			g.choose(t, "mba")
			waitFor(t, m, func() bool { return !m.ov.handoff.comparing })
			want := envcheck.Compare(g.here.env, g.mba.env, g.here.peer().End(), g.mba.peer().End())
			if want.Block != 1 || want.Unequal != 1 {
				t.Fatalf("the fixture blocks once and differs once: %+v", want)
			}
			if s := screenText(m); !strings.Contains(s, i18n.F("handoff.env_counts", 1, 1)) {
				t.Fatalf("the dialog counts blocks and differences:\n%s", s)
			}
			block := "- " + envcheck.LevelText(envcheck.LevelBlock) + ": " + i18n.F("envcheck.cli_missing", "claude")
			if b, _ := os.ReadFile(m.ov.title); !strings.Contains(string(b), want.Summary()) || !strings.Contains(string(b), block) {
				t.Fatalf("the pack carries the comparison:\n%s", b)
			}
			checkWidth(t, m)
			key(m, "1")
			key(m, "1")
			if puts := g.mba.put(); len(puts) != 1 || !strings.Contains(puts[0].Text, block) {
				t.Fatalf("a block stops nothing and goes with the pack: %+v", puts)
			}
		})
		t.Run(tr+"/unread", func(t *testing.T) {
			g := newHandoffRig(t, tr, func(g *handoffRig) {
				g.mba.repos = []remote.RepoDir{{Path: "/home/u/dev/shop", From: "index"}}
				g.mba.hello.Methods = slices.DeleteFunc(slices.Clone(g.mba.hello.Methods), func(s string) bool { return s == remote.MEnv })
			})
			m := g.m
			g.openLocal(t)
			g.choose(t, "mba")
			waitFor(t, m, func() bool { return !m.ov.handoff.comparing })
			why := remote.EnvRefusal("mba", &wire.Error{Code: wire.CodeUnknownMethod})
			line := i18n.F("handoff.env.unread", why)
			if s := screenText(m); m.ov.handoff.envWhy != why || !strings.Contains(s, string([]rune(line)[:12])) {
				t.Fatalf("the dialog says why nothing was compared:\n%s", s)
			}
			if b, _ := os.ReadFile(m.ov.title); !strings.Contains(string(b), i18n.F("handoff.env.unread", remote.Reason(&wire.Error{Code: wire.CodeUnknownMethod}))) {
				t.Fatalf("the pack says so too:\n%s", b)
			}
			key(m, "1")
			key(m, "1")
			if len(g.mba.put()) != 1 {
				t.Fatal("the handoff goes on")
			}
		})
	}
}
