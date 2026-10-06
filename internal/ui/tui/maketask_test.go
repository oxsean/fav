package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// makeBtn is the index of the resume dialog's 「建成任务」 as drawn now, -1 when it is not there; label is what it says.
func makeBtn(m *Model) (i int, label string) {
	m.screen()
	for i, b := range m.ov.btns {
		if strings.HasPrefix(b.label, i18n.T("resume.btn_make_task")) {
			return i, b.label
		}
	}
	return -1, ""
}

// tabTo moves the resume dialog's focus with Tab until it rests on button i; false when Tab never gets there.
func tabTo(m *Model, i int) bool {
	for range len(m.ov.btns) + 1 {
		if m.ov.focus == i {
			return true
		}
		key(m, "tab")
	}
	return m.ov.focus == i
}

// clickPump clicks at x, y and runs what the click started.
func clickPump(m *Model, x, y int) {
	_, cmd := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	pump(m, cmd)
}

func taskMadeFrom(m *Model, session string) (*task.Task, *task.Run) {
	if m.tasks.st == nil {
		return nil, nil
	}
	for _, r := range m.tasks.st.Runs {
		if r.Resume == session {
			return m.tasks.st.Tasks[r.Task], r
		}
	}
	return nil, nil
}

func TestMakeATaskFromTheResumeDialogByKeyboard(t *testing.T) {
	m, _ := tasksModel(t)
	rec := m.current()
	key(m, "enter")
	i, label := makeBtn(m)
	if i < 0 || label != i18n.T("resume.btn_make_task") || m.ov.btns[i].primary {
		t.Fatalf("the 「另开会话」 row offers it: %d %q", i, label)
	}
	if !tabTo(m, i) {
		t.Fatal("Tab reaches it")
	}
	key(m, "enter")
	waitFor(t, m, func() bool { return m.ov.kind == ovMakeTask })
	s := screenText(m)
	for _, want := range []string{i18n.F("tasks.from_sub", rec.Title, i18n.T("remote.local"), tend.ProviderName(rec.Provider)),
		i18n.T("tasks.project_private"), i18n.T("tasks.from_privacy"), i18n.T("tasks.from_resumes"), i18n.T("tasks.btn_make")} {
		if !strings.Contains(s, want) {
			t.Errorf("the form shows %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, i18n.F("tasks.from_members", "")) {
		t.Error("a private task has no members line")
	}

	key(m, "ctrl+s")
	if m.ov.kind != ovMakeTask || m.notice != i18n.T("tasks.from_empty") || m.ov.field != makeText {
		t.Fatalf("nothing written: %v %q", m.ov.kind, m.notice)
	}
	typeText(m, "fix")
	key(m, "enter")
	typeText(m, "go")
	if got := m.ov.area.Value(); got != "fix\ngo" {
		t.Fatalf("Enter starts a new line: %q", got)
	}
	key(m, "tab")
	if m.ov.field != makeAgent || m.picked(0) != tend.ProviderClaude {
		t.Fatalf("Tab goes to the agent, claude first: %d %q %v", m.ov.field, m.picked(0), m.ov.opts[0])
	}
	if !slices.Contains(m.ov.opts[0], "fake") || slices.Contains(m.ov.opts[0], tend.ProviderCodex) {
		t.Fatalf("only the profiles that continue a claude session: %v", m.ov.opts[0])
	}
	key(m, "right")
	key(m, "left")
	key(m, "tab")
	if m.ov.field != makeProject || m.ov.ids[m.ov.pick[1]] != "" {
		t.Fatalf("then the project, private: %d", m.ov.field)
	}
	key(m, "ctrl+s")
	waitFor(t, m, func() bool { tk, _ := taskMadeFrom(m, rec.SessionID); return tk != nil })
	tk, run := taskMadeFrom(m, rec.SessionID)
	if tk.Project != "" || tk.Title != rec.Title || run.Brief != "fix\ngo" || run.Agent != tend.ProviderClaude || run.Dir != rec.Cwd {
		t.Fatalf("a private task continuing the session: %+v %+v", tk, run)
	}
	if want := i18n.F("tasks.made_from", tk.ID, rec.Title, keyOf(inResume, actTask)); m.notice != want {
		t.Fatalf("the flash: %q, want %q", m.notice, want)
	}
	if x := m.taskOf(rec); x == nil || x.ID != tk.ID {
		t.Fatalf("the session links to its task: %v", x)
	}
	key(m, "enter")
	if g := m.taskGroup(m.ov.rec); len(g) == 0 {
		t.Fatal("the resume dialog opens the task with g")
	}
	key(m, "g")
	if m.view != viewTasks || m.selectedTask() == nil || m.selectedTask().ID != tk.ID {
		t.Fatalf("g opens the task: %v", m.view)
	}
}

func TestMakeATaskInAProjectByClick(t *testing.T) {
	m, _ := tasksModel(t)
	rec := m.current()
	key(m, "5")
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MProjectCreate, "p", coord.ProjectCreate{ID: "p_notes", Name: "notes-api"}, nil); err != nil {
		t.Fatal(err)
	}
	p := &task.Project{ID: "p_notes", Name: "notes-api", Owner: coord.Owner.User, Members: map[string]string{"u_bob": task.RoleParticipant},
		Repos: []task.Repo{{Name: "notes-api", Dirs: map[string]string{coord.Local: rec.Cwd}}}}
	m.setProjects(projects.Mine(map[string]*task.Project{p.ID: p}))
	key(m, "1")
	for m.current() != rec {
		key(m, "j")
	}

	key(m, "enter")
	x, y := findText(m.screen(), i18n.T("resume.btn_make_task"))
	if x < 0 {
		t.Fatalf("no button:\n%s", screenText(m))
	}
	clickPump(m, x, y)
	waitFor(t, m, func() bool { return m.ov.kind == ovMakeTask })
	if want := i18n.F("tasks.project_of_dir", "notes-api"); !slices.Contains(m.ov.opts[1], want) || m.ov.opts[1][1] != want {
		t.Fatalf("the session's project comes second: %v", m.ov.opts[1])
	}

	x, y = findText(m.screen(), i18n.T("tasks.project_private"))
	click(m, x, y)
	if m.ov.field != makeProject || m.ov.ids[m.ov.pick[1]] != "p_notes" {
		t.Fatalf("a click on the project picks the next: %d %v", m.ov.field, m.ov.pick)
	}
	if s := screenText(m); !strings.Contains(s, i18n.F("tasks.from_members", "notes-api")) {
		t.Fatalf("a team project says its members see it:\n%s", s)
	}
	x, y = findText(m.screen(), i18n.T("tasks.from_text"))
	click(m, x, y+2)
	if m.ov.field != makeText || !m.ov.area.Focused() {
		t.Fatalf("a click on the text writes there: %d", m.ov.field)
	}
	typeText(m, "fix")
	x, y = findText(m.screen(), i18n.T("tasks.btn_make"))
	clickPump(m, x, y)
	waitFor(t, m, func() bool { tk, _ := taskMadeFrom(m, rec.SessionID); return tk != nil })
	tk, run := taskMadeFrom(m, rec.SessionID)
	if tk.Project != "p_notes" || run.Project != "p_notes" {
		t.Fatalf("the task is the project's: %+v %+v", tk, run)
	}
	if !strings.HasPrefix(m.notice, i18n.F("tasks.made_from", tk.ID, rec.Title, keyOf(inResume, actTask))) {
		t.Fatalf("the flash: %q", m.notice)
	}
}

func TestAnOlderCoordinatorMakesItPrivateAndSaysSo(t *testing.T) {
	m := sized(t, 120, 40)
	madeTaskMsg{run: task.Run{Task: "t_1"}, title: "x", project: "p_notes"}.apply(m)
	if m.notice != i18n.F("tasks.made_private", "t_1") {
		t.Fatalf("%q", m.notice)
	}
	madeTaskMsg{run: task.Run{Task: "t_2", Project: "p_notes"}, title: "x", project: "p_notes"}.apply(m)
	if m.notice != i18n.F("tasks.made_from", "t_2", "x", keyOf(inResume, actTask)) {
		t.Fatalf("%q", m.notice)
	}
}

// Each reason the session cannot become a task is the button's label; the button is then never focused, clicked or
// pressed, and the palette says the same.
func TestMakeTaskSaysWhyItCannot(t *testing.T) {
	reached := func(wire.Options) (*coord.Client, error) { return nil, &wire.Error{Code: wire.CodeOffline} }
	served := func(m *Model, s projects.Snapshot) {
		m.cfg.Coordinator = &tend.CoordinatorConfig{URL: "https://tend.example"}
		m.tasks.cl = &coord.Client{}
		m.proj.snap = s
	}
	ready := projects.Snapshot{State: projects.Ready, Here: "mac", Owners: map[string]string{"mac": "u_bob"}, Viewer: projects.Viewer{User: "u_bob"}}
	for _, c := range []struct {
		why string
		set func(m *Model, r *tend.Rec)
	}{
		{"resume.task_offline", func(m *Model, r *tend.Rec) { m.tasks.connect = nil }},
		{"resume.task_offline", func(m *Model, r *tend.Rec) { served(m, ready); m.tasks.cl = nil }},
		{"resume.task_old", func(m *Model, r *tend.Rec) { served(m, projects.Down(projects.Outdated)) }},
		{"resume.task_no_node", func(m *Model, r *tend.Rec) { s := ready; s.Here = ""; served(m, s) }},
		{"resume.task_not_owner", func(m *Model, r *tend.Rec) { s := ready; s.Owners = map[string]string{"mac": "u_ann"}; served(m, s) }},
		{"resume.task_busy", func(m *Model, r *tend.Rec) { m.live = map[string]capture.Live{r.SessionID: {Status: "idle"}} }},
		{"resume.task_busy", func(m *Model, r *tend.Rec) {
			m.tasks.st = task.New()
			m.tasks.st.Tasks["t"] = &task.Task{ID: "t"}
			m.tasks.st.Runs["r"] = &task.Run{ID: "r", Task: "t", Resume: r.SessionID, State: task.Queued}
		}},
		{"resume.task_busy", func(m *Model, r *tend.Rec) { r.Provider, r.LastAt, m.now = tend.ProviderCodex, time.Now(), time.Now() }},
		{"resume.task_no_agent", func(m *Model, r *tend.Rec) { m.tasks.agents = []tend.AgentProfile{{Name: "sh", Provider: "command"}} }},
		{"", func(m *Model, r *tend.Rec) { served(m, ready) }},
	} {
		m := sized(t, 140, 40)
		r := m.current()
		m.SetCoordinator(reached)
		c.set(m, r)
		if got := m.makeTaskWhy(r); got != c.why {
			t.Errorf("want %q, got %q", c.why, got)
			continue
		}
		m.askResume()
		i, label := makeBtn(m)
		if c.why == "" {
			if i < 0 || m.ov.btns[i].act == nil {
				t.Errorf("served on the viewer's own machine: the button is on")
			}
			continue
		}
		if i < 0 || label != i18n.T(c.why) || m.ov.btns[i].act != nil {
			t.Errorf("%s: the button says why and is off: %d %q", c.why, i, label)
			continue
		}
		if tabTo(m, i) {
			t.Errorf("%s: Tab focuses it", c.why)
		}
		m.ov.focus = -1
		for range len(m.ov.btns) {
			key(m, "left")
		}
		for range len(m.ov.btns) {
			if key(m, "right"); m.ov.focus == i {
				t.Errorf("%s: → focuses it", c.why)
			}
		}
		x, y := findText(m.screen(), i18n.T(c.why))
		click(m, x, y)
		if m.ov.kind != ovResume {
			t.Errorf("%s: a click on it does something: %v", c.why, m.ov.kind)
		}
		m.closeOverlay()
		m.openPalette()
		m.ov.filter.SetValue(i18n.T("resume.btn_make_task"))
		key(m, "enter")
		if m.ov.kind == ovMakeTask || m.notice != i18n.T(c.why) {
			t.Errorf("%s: the palette says why: %q", c.why, m.notice)
		}
	}
}

func TestThePaletteMakesATask(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, ":")
	m.ov.filter.SetValue(i18n.In(i18n.EN, "resume.btn_make_task"))
	key(m, "enter")
	waitFor(t, m, func() bool { return m.ov.kind == ovMakeTask })
	if m.ov.rec != m.current() {
		t.Fatal("on the selected session")
	}
	key(m, "esc")
	if m.ov.active() {
		t.Fatal("Esc discards the form")
	}
}
