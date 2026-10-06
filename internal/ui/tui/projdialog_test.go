package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/coordtest"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// projectDirs are the directories of projectStore's sessions under root: webapp's main clone, a second clone of the
// same name, and notes-api.
type projectDirs struct{ webapp, scratch, notes string }

// projectStore: three webapp sessions in two directories named webapp (two in the main clone), two notes-api ones.
func projectStore(t *testing.T) (*tend.Store, projectDirs) {
	t.Helper()
	root := t.TempDir()
	d := projectDirs{filepath.Join(root, "work", "webapp"), filepath.Join(root, "scratch", "webapp"), filepath.Join(root, "work", "notes-api")}
	st, err := tend.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for i, dir := range []string{d.webapp, d.webapp, d.scratch, d.notes, d.notes} {
		os.MkdirAll(dir, 0o700)
		r := &tend.Rec{ID: tend.NewID(), Provider: tend.ProviderClaude, SessionID: tend.NewID(), Title: filepath.Base(dir) + " " + string(rune('a'+i)),
			Project: filepath.Base(dir), Cwd: dir, Status: tend.StatusDone, FavoritedAt: new(time.Now().Add(-time.Duration(i) * time.Hour))}
		if err := st.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	return st, d
}

// singleProjects is a mode 1 TUI over projectStore whose coordinator, reached when the first project changes, keeps
// its journal where the TUI reads projects.
func singleProjects(t *testing.T) (*Model, projectDirs, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("TEND_HOME", home)
	st, d := projectStore(t)
	m := newModel(t, st, 140, 40)
	n := node.New(home)
	var clients []*coord.Client
	m.SetCoordinator(func(w wire.Options) (*coord.Client, error) {
		cl, err := coord.Connect(coord.Options{Home: home, Node: n, Sessions: remote.NewLocal("test")}, w)
		if err == nil {
			clients = append(clients, cl)
		}
		return cl, err
	})
	t.Cleanup(func() {
		for _, cl := range clients {
			cl.CloseNow()
		}
	})
	m.setView(viewProjects)
	return m, d, home
}

func cursorOnGroup(t *testing.T, m *Model, key string) {
	t.Helper()
	for i, r := range m.rows {
		if r.group == key {
			m.cursor = i
			return
		}
	}
	t.Fatalf("no group %q in %v", key, groupKeys(m))
}

func groupKeys(m *Model) []string {
	var out []string
	for _, r := range m.rows {
		if r.group != "" {
			out = append(out, r.group)
		}
	}
	return out
}

func projectGroup(m *Model, name string) string {
	for _, r := range m.rows {
		if r.group != "" && groupProject(r.group) != "" && r.label == name {
			return r.group
		}
	}
	return ""
}

func clickText(t *testing.T, m *Model, text string) {
	t.Helper()
	x, y := findText(m.screen(), text)
	if x < 0 {
		t.Fatalf("no %q on screen:\n%s", text, screenText(m))
	}
	_, cmd := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	pump(m, cmd)
}

// TestFileAGroupThenRemoveItsDirectory: e on an automatic group puts its most used directory into a new project
// (only that one is ticked: the clone of the same name stays out), the sessions regroup under it, and removing the
// directory in the project's dialog sends them back. Mode 1: the first change makes the TUI the coordinator.
func TestFileAGroupThenRemoveItsDirectory(t *testing.T) {
	m, d, _ := singleProjects(t)
	cursorOnGroup(t, m, "webapp")
	key(m, "e")
	if m.ov.kind != ovProject || m.ov.proj.id != "" {
		t.Fatalf("e on an automatic group opens the dialog to file it: %v", m.ov.kind)
	}
	pd := m.ov.proj
	if len(pd.dirs) != 2 || pd.dirs[0].dir != d.webapp || !pd.dirs[0].ticked || pd.dirs[1].ticked {
		t.Fatalf("the most used directory comes first, ticked alone: %+v", pd.dirs)
	}
	if pd.pick != len(pd.choices) || !pd.name.Focused() || pd.name.Value() != "webapp" {
		t.Fatalf("no project yet: a new one, its name prefilled and focused (%q)", pd.name.Value())
	}
	if s := screenText(m); !strings.Contains(s, i18n.T("project.btn_create")) || !strings.Contains(s, i18n.T("project.new_note")) {
		t.Fatalf("the dialog:\n%s", s)
	}
	typeText(m, " shop") // letters type into the name, x included
	key(m, "enter")
	waitFor(t, m, func() bool { return projectGroup(m, "webapp shop") != "" })
	if m.tasks.cl == nil || m.tasks.cl.Coord == nil {
		t.Fatal("mode 1: the change made this TUI the coordinator")
	}
	g := projectGroup(m, "webapp shop")
	if n := len(m.groups[g]); n != 2 {
		t.Fatalf("the project's group holds the main clone's two sessions: %d", n)
	}
	if len(m.groups["webapp"]) != 1 {
		t.Fatalf("the clone of the same name stays in its automatic group: %v", groupKeys(m))
	}
	if m.rows[0].section != i18n.F("group.projects", 1) {
		t.Fatalf("project groups come first under their heading: %+v", m.rows[0])
	}
	if m.groupUnderCursor() != g || !m.open[g] {
		t.Fatal("the cursor follows to the new project's group, opened")
	}
	if m.scroll != 0 {
		t.Fatalf("a list shorter than the pane stays unscrolled after regrouping: %d", m.scroll)
	}

	key(m, "ctrl+e")
	if m.ov.kind != ovProject || m.ov.proj.id != groupProject(g) || len(m.ov.proj.dirs) != 1 {
		t.Fatalf("Ctrl+E on the project's group opens its dialog: %+v", m.ov.proj)
	}
	if s := screenText(m); !strings.Contains(s, i18n.T("project.this_machine")) || !strings.Contains(s, i18n.T("project.here")) {
		t.Fatalf("this machine's directory says it is here:\n%s", s)
	}
	clickText(t, m, i18n.T("project.btn_detach"))
	waitFor(t, m, func() bool { return projectGroup(m, "webapp shop") == "" })
	if len(m.groups["webapp"]) != 3 {
		t.Fatalf("without its directory the sessions go back to their automatic group: %v", groupKeys(m))
	}
	if m.ov.kind != ovProject || m.ov.proj.id != groupProject(g) || len(m.ov.proj.dirs) != 0 {
		t.Fatalf("the dialog stays on the project, now without directories, so one can be added back: %+v", m.ov.proj)
	}
}

// TestFileIntoAProjectByClickAndRename: the dialog lists the projects there are; a click picks one, x ticks a second
// directory, the primary button files them; Enter in the project's dialog saves a new name.
func TestFileIntoAProjectByClickAndRename(t *testing.T) {
	m, d, home := singleProjects(t)
	cl, err := coord.Connect(coord.Options{Home: home, Node: node.New(home), Sessions: remote.NewLocal("test")}, wire.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := cl.CallCommand(ctx, coord.MProjectCreate, "c1", coord.ProjectCreate{ID: "p_shop", Name: "Shop"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := cl.CallCommand(ctx, coord.MProjectAttach, "c2", coord.ProjectAttach{Project: "p_shop", Machine: coord.Local, Dir: d.notes}, nil); err != nil {
		t.Fatal(err)
	}
	cl.Close()
	m.Update(storeTickMsg{}) // another process appended to the journal: the next look reads it
	if g := projectGroup(m, "Shop"); len(m.groups[g]) != 2 {
		t.Fatalf("notes-api's sessions are in Shop: %v", groupKeys(m))
	}

	cursorOnGroup(t, m, "webapp")
	key(m, "e")
	if pd := m.ov.proj; len(pd.choices) != 1 || pd.choices[0].ID != "p_shop" {
		t.Fatalf("the project there is offered: %+v", pd.choices)
	}
	clickText(t, m, "Shop")
	if m.ov.proj.pick != 0 || m.ov.proj.name.Focused() {
		t.Fatal("a click picks the project; there is no name to type")
	}
	key(m, "tab") // to the directories
	key(m, "down")
	key(m, "x")
	if pd := m.ov.proj; !pd.dirs[0].ticked || !pd.dirs[1].ticked {
		t.Fatalf("x ticks the second directory: %+v", pd.dirs)
	}
	clickText(t, m, i18n.F("project.btn_file", "Shop"))
	waitFor(t, m, func() bool { g := projectGroup(m, "Shop"); return len(m.groups[g]) == 5 })

	cursorOnGroup(t, m, projKey+"p_shop")
	key(m, "e")
	for range len("Shop") {
		key(m, "backspace")
	}
	typeText(m, "Store")
	key(m, "enter")
	waitFor(t, m, func() bool { return projectGroup(m, "Store") != "" })
}

// servedTeam is a mode 2 TUI as viewer on their own machine, a node of a team coordinator: mac is ann's, bobs is bob's.
// Project p_shop, owned by ann with bob participating, holds the notes-api directory on both.
func servedTeam(t *testing.T, viewer coord.Principal, connect func(*coordtest.Team, wire.Options) (*coord.Client, error)) (*Model, *coordtest.Team, projectDirs) {
	t.Helper()
	ann, bob := coord.User{ID: "u_ann", Name: "Ann"}, coord.User{ID: "u_bob", Name: "Bob"}
	tm := coordtest.NewTeam(t, ann, bob)
	nodes := map[string]*node.Node{"mac": node.New(t.TempDir()), "bobs": node.New(t.TempDir())}
	tm.Attach("mac", ann.ID, nodes["mac"])
	tm.Attach("bobs", bob.ID, nodes["bobs"])
	here := nodes["mac"]
	if viewer.User == bob.ID {
		here = nodes["bobs"]
	}
	home := t.TempDir()
	t.Setenv("TEND_HOME", home)
	os.MkdirAll(filepath.Join(home, "node"), 0o700)
	os.WriteFile(filepath.Join(home, "node", "id"), []byte(here.ID()+"\n"), 0o600)
	st, d := projectStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i, c := range []struct {
		as     string
		method string
		params any
	}{
		{ann.ID, coord.MProjectCreate, coord.ProjectCreate{ID: "p_shop", Name: "Shop"}},
		{ann.ID, coord.MProjectMember, task.MemberSet{Project: "p_shop", User: bob.ID, Role: task.RoleParticipant}},
		{ann.ID, coord.MProjectAttach, coord.ProjectAttach{Project: "p_shop", Machine: "mac", Dir: d.notes}},
		{bob.ID, coord.MProjectAttach, coord.ProjectAttach{Project: "p_shop", Machine: "bobs", Dir: d.notes}},
	} {
		cl := tm.Client(coord.Principal{User: c.as}, wire.Options{})
		if err := cl.CallCommand(ctx, c.method, "c"+string(rune('0'+i)), c.params, nil); err != nil {
			t.Fatal(err)
		}
	}

	cfg := tend.DefaultConfig()
	cfg.Coordinator = &tend.CoordinatorConfig{URL: "https://tend.example"}
	m := New(st, noIndex(t), cfg, "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if connect == nil {
		connect = func(tm *coordtest.Team, w wire.Options) (*coord.Client, error) { return tm.Client(viewer, w), nil }
	}
	m.SetCoordinator(func(w wire.Options) (*coord.Client, error) { return connect(tm, w) })
	m.setView(viewProjects)
	return m, tm, d
}

// TestServedProjectsComeFromTheServer: mode 2 dials at start; the server's projects group this machine's sessions
// once its node id is found among the server's machines; a participant removes a directory on his own machine only.
func TestServedProjectsComeFromTheServer(t *testing.T) {
	m, _, _ := servedTeam(t, coord.Principal{User: "u_bob"}, nil)
	if len(groupKeys(m)) != 2 || m.proj.snap.State != projects.Offline {
		t.Fatalf("before the dial: automatic groups only: %v (%s)", groupKeys(m), m.proj.snap.State)
	}
	m.Init()
	if !m.tasks.connecting {
		t.Fatal("mode 2 dials at start")
	}
	pump(m, func() tea.Msg { cl, err := m.tasks.connect(wire.Options{}); return tasksConnMsg{cl, err} })
	waitFor(t, m, func() bool { return projectGroup(m, "Shop") != "" })
	if m.proj.snap.Here != "bobs" || m.proj.snap.Viewer.User != "u_bob" {
		t.Fatalf("this machine is bobs, the viewer bob: %+v", m.proj.snap)
	}
	if m.tasks.cl.Coord != nil {
		t.Fatal("mode 2 never becomes the coordinator")
	}
	cursorOnGroup(t, m, projKey+"p_shop")
	key(m, "e")
	if s := screenText(m); !strings.Contains(s, i18n.T("project.btn_detach")) || strings.Contains(s, i18n.T("project.btn_detach_not_yours")) {
		t.Fatalf("on his own machine's directory a participant may remove it:\n%s", s)
	}
	key(m, "down") // ann's machine
	s := screenText(m)
	for _, want := range []string{i18n.T("project.participant"), i18n.T("project.btn_detach_not_yours"), i18n.T("project.rename_owner_only")} {
		if !strings.Contains(s, want) {
			t.Fatalf("a participant's dialog lacks %q:\n%s", want, s)
		}
	}
	key(m, "esc")
	cursorOnGroup(t, m, "webapp")
	key(m, "e")
	if len(m.ov.proj.choices) != 1 {
		t.Fatalf("bob may add his own machine's directories to Shop: %+v", m.ov.proj.choices)
	}
}

// TestServedOfflineOrOldShowsAutomaticGroups: a server that cannot be reached, or one too old to say who it answers as,
// leaves every session in its automatic group; the dialog says why and offers only Esc.
func TestServedOfflineOrOldShowsAutomaticGroups(t *testing.T) {
	for name, c := range map[string]struct {
		connect func(*coordtest.Team, wire.Options) (*coord.Client, error)
		want    string
	}{
		"offline": {func(*coordtest.Team, wire.Options) (*coord.Client, error) { return nil, errors.New("no route") }, "project.offline"},
		"old": {func(tm *coordtest.Team, w wire.Options) (*coord.Client, error) {
			h := tm.C.HandlerFor(coord.Principal{User: "u_ann"})
			a, _ := wire.Pipe(w, wire.Options{Handler: func(ctx context.Context, r *wire.Request) (any, error) {
				v, err := h(ctx, r)
				if hello, ok := v.(remote.Hello); ok {
					hello.Caller = nil
					return hello, err
				}
				return v, err
			}})
			return &coord.Client{Conn: a}, nil
		}, "project.old_server"},
	} {
		t.Run(name, func(t *testing.T) {
			m, _, _ := servedTeam(t, coord.Principal{}, c.connect)
			pump(m, m.tasksOpen())
			time.Sleep(100 * time.Millisecond)
			pump(m, nil)
			if projectGroup(m, "Shop") != "" || len(groupKeys(m)) != 2 {
				t.Fatalf("automatic groups only: %v", groupKeys(m))
			}
			cursorOnGroup(t, m, "notes-api")
			key(m, "e")
			s := screenText(m)
			if !strings.Contains(s, i18n.T(c.want)) {
				t.Fatalf("the dialog does not say %s:\n%s", c.want, s)
			}
			if len(m.ov.btns) != 1 || !m.ov.btns[0].primary {
				t.Fatalf("only Esc: %+v", m.ov.btns)
			}
			key(m, "enter")
			if m.ov.active() {
				t.Fatal("Enter closes it")
			}
		})
	}
}

// TestJournalWrittenElsewhereRegroups: a project another process (the coordinator) wrote shows at the next look.
func TestJournalWrittenElsewhereRegroups(t *testing.T) {
	m, d, home := singleProjects(t)
	l, err := journal.Open(filepath.Join(home, "coord", "events.jsonl"), func(journal.Envelope) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	repos := []task.Repo{{Name: "web", Dirs: map[string]string{coord.Local: d.webapp}}}
	if _, err := l.Append(journal.System, nil, []journal.Event{
		journal.NewEvent(task.EProjectCreated, task.Project{ID: "p1", Name: "Web", Owner: coord.Owner.User, Repos: repos})}); err != nil {
		t.Fatal(err)
	}
	m.Update(storeTickMsg{})
	if g := projectGroup(m, "Web"); len(m.groups[g]) != 2 {
		t.Fatalf("the project appended elsewhere groups webapp's main clone: %v", groupKeys(m))
	}
}
