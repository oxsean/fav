package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// tasksModel is a Model whose Tasks view talks to a coordinator in a temporary home; its node records every run as
// already running with some output instead of starting a process.
func tasksModel(t *testing.T) (*Model, string) {
	return tasksModelWith(t, func(dir string, spec node.Spec) (string, error) {
		now := time.Now()
		os.WriteFile(filepath.Join(dir, "output.log"), []byte("step one\nstep two\n"), 0o600)
		return "", writeState(dir, node.State{Rev: 1, State: node.StateExited, ExitCode: new(0), Provider: spec.Provider,
			Session: spec.Session, StartedAt: &now, EndedAt: &now})
	})
}

func tasksModelWith(t *testing.T, launch func(dir string, spec node.Spec) (string, error)) (*Model, string) {
	t.Helper()
	home := t.TempDir()
	n := node.New(home)
	n.Launch = launch
	m := sized(t, 140, 40)
	var clients []*coord.Client
	m.SetCoordinator(func(w wire.Options) (*coord.Client, error) {
		cl, err := coord.Connect(coord.Options{Home: home, Config: tend.Config{Agents: []tend.AgentProfile{{Name: "fake", Provider: "fake"}}},
			Node: n, Sessions: remote.NewLocal("test")}, w)
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
	return m, home
}

func writeState(dir string, st node.State) error {
	b := `{"rev":1,"state":"` + st.State + `","exit_code":0,"provider":"` + st.Provider + `","session":"` + st.Session + `"}`
	return os.WriteFile(filepath.Join(dir, "state.json"), []byte(b), 0o600)
}

// pump runs cmd and feeds the Tasks view's messages back until none come; commands that take longer than a moment
// (ticks, blinks, waiting for a push) are left running, and what they return later is fed in by the next pump.
func pump(m *Model, cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for _, ch := range late[m] {
		ch := ch
		queue = append(queue, func() tea.Msg {
			select {
			case msg := <-ch:
				return msg
			default:
				late[m] = append(late[m], ch)
				return nil
			}
		})
	}
	delete(late, m)
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		got := make(chan tea.Msg, 1)
		go func() { got <- c() }()
		var msg tea.Msg
		select {
		case msg = <-got:
		case <-time.After(300 * time.Millisecond):
			late[m] = append(late[m], got)
			continue
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case tasksTickMsg:
		case interface{ apply(*Model) tea.Cmd }:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}

// late are the commands pump stopped waiting for, per model.
var late = map[*Model][]chan tea.Msg{}

func key(m *Model, k string) {
	_, cmd := m.Update(press(k))
	pump(m, cmd)
}

func typeText(m *Model, s string) {
	for _, r := range s {
		key(m, string(r))
	}
}

func screenText(m *Model) string { return ansi.Strip(m.screen()) }

func waitFor(t *testing.T, m *Model, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; screen:\n%s", screenText(m))
		}
		time.Sleep(50 * time.Millisecond)
		pump(m, m.pollTasks())
	}
}

func TestTasksViewCreatesRunsAndShowsATask(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	if m.view != viewTasks || m.tasks.cl == nil || !m.tasks.loaded {
		t.Fatalf("5 opens the Tasks view connected: view %v err %v", m.view, m.tasks.err)
	}
	if s := screenText(m); !strings.Contains(s, "还没有任务") && !strings.Contains(s, "No tasks yet") {
		t.Fatalf("empty view:\n%s", s)
	}
	key(m, "w")
	if m.ov.kind != ovTaskForm {
		t.Fatalf("w opens the task form: %v", m.ov.kind)
	}
	typeText(m, "fix the build")
	key(m, "tab")
	m.ov.edit2.SetValue(t.TempDir())
	key(m, "tab") // machine
	key(m, "tab") // agent
	for m.picked(1) != "fake" {
		key(m, "right")
	}
	key(m, "ctrl+s")
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	x := m.tasks.list[0]
	if x.Title != "fix the build" || x.Agent != "fake" || x.Machine != "" {
		t.Fatalf("%+v", x)
	}

	key(m, "enter")
	if m.ov.kind != ovTask {
		t.Fatalf("Enter opens the task dialog: %v", m.ov.kind)
	}
	s := screenText(m)
	if !strings.Contains(s, "跑起来") && !strings.Contains(s, "Run") {
		t.Fatalf("the dialog offers to run it:\n%s", s)
	}
	key(m, "enter")
	if m.ov.kind != ovTaskRun || m.picked(1) != "fake" {
		t.Fatalf("Enter on Run opens the run dialog with the task's agent: %v %q", m.ov.kind, m.picked(1))
	}
	key(m, "enter")
	waitFor(t, m, func() bool { r := m.selectedRun(); return r != nil && r.State == task.Exited })
	waitFor(t, m, func() bool { return m.tasks.out[m.selectedRun().ID].end })
	s = screenText(m)
	for _, want := range []string{"fix the build", "step two", "local"} {
		if !strings.Contains(s, want) {
			t.Fatalf("screen lacks %q:\n%s", want, s)
		}
	}
	for i, l := range strings.Split(m.screen(), "\n") {
		if w := ansi.StringWidth(l); w != m.w {
			t.Fatalf("line %d is %d wide, not %d", i, w, m.w)
		}
	}

	key(m, "x")
	waitFor(t, m, func() bool { return m.tasks.st.Tasks[x.ID].Status == task.StatusDone })
	key(m, "x")
	waitFor(t, m, func() bool { return m.tasks.st.Tasks[x.ID].Status == task.StatusTodo })

	key(m, "space")
	if m.view != viewSessions || m.pane != paneChat {
		t.Fatalf("Space shows the run's session: view %v pane %v", m.view, m.pane)
	}
}

func TestTasksViewWithoutACoordinatorSaysSo(t *testing.T) {
	m := sized(t, 140, 40)
	key(m, "5")
	if s := screenText(m); !strings.Contains(s, "协调器") && !strings.Contains(s, "coordinator") {
		t.Fatalf("%s", s)
	}
	key(m, "f")
	if m.notice == "" {
		t.Fatal("a session action in the Tasks view says it is not there")
	}
}

func TestEditingATaskKeepsTheBriefTheListLacks(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	brief := strings.Repeat("a long brief line\n", 2000)
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "one", Brief: brief, Dir: t.TempDir()}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	for _, x := range m.tasks.st.Tasks { // as the list reads when the state is too big for one frame
		x.Brief = ""
	}
	key(m, "e")
	if m.ov.kind != ovTaskForm || len(m.ov.area.Value()) < len(strings.TrimSpace(brief)) {
		t.Fatalf("the form has the whole brief: %v %d", m.ov.kind, len(m.ov.area.Value()))
	}
	m.ov.edit.SetValue("renamed")
	key(m, "ctrl+s")
	waitFor(t, m, func() bool { return m.tasks.list[0].Title == "renamed" })
	var got task.Task
	m.tasks.cl.Call(t.Context(), coord.MTaskGet, task.RunRef{ID: m.tasks.list[0].ID}, &got)
	if got.Brief != brief {
		t.Fatalf("the brief is %d bytes, was %d", len(got.Brief), len(brief))
	}
}

func TestAClickInTheBriefPutsTheCursorThere(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	key(m, "w")
	m.ov.area.SetWidth(20)
	m.ov.area.SetValue("第一行文字\n" + strings.Repeat("x", 30))
	m.clickX = 2 + 4 // two wide characters in
	m.placeAreaCursor(0, 2)
	if m.ov.area.Line() != 0 || m.ov.area.LineInfo().ColumnOffset != 2 {
		t.Fatalf("line %d col %d", m.ov.area.Line(), m.ov.area.LineInfo().ColumnOffset)
	}
	m.clickX = 2 + 3
	m.placeAreaCursor(2, 2) // the second visual row of the wrapped line
	if li := m.ov.area.LineInfo(); m.ov.area.Line() != 1 || li.RowOffset != 1 || li.ColumnOffset != 3 {
		t.Fatalf("line %d %+v", m.ov.area.Line(), li)
	}
}

func TestARunThatAsksIsAnsweredFromItsDialog(t *testing.T) {
	var launched []node.Spec
	m, _ := tasksModelWith(t, func(dir string, spec node.Spec) (string, error) {
		launched = append(launched, spec)
		b := `{"rev":1,"state":"exited","exit_code":0,"provider":"claude","session":"` + spec.Session + `","attention":"asked","ask":"Which branch?"}`
		return "", os.WriteFile(filepath.Join(dir, "state.json"), []byte(b), 0o600)
	})
	key(m, "5")
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "pick", Dir: t.TempDir(), Agent: "fake"}, &tk); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	key(m, "enter")
	key(m, "enter")
	if m.ov.kind != ovTaskRun {
		t.Fatalf("%v", m.ov.kind)
	}
	waitFor(t, m, func() bool { return m.ov.preview != nil })
	if s := screenText(m); !strings.Contains(s, "后台运行") && !strings.Contains(s, "runs in the background") {
		t.Fatalf("the run dialog says how it will run:\n%s", s)
	}
	key(m, "enter")
	waitFor(t, m, func() bool { r := m.selectedRun(); return r != nil && r.Waiting() })
	key(m, "enter")
	if s := screenText(m); !strings.Contains(s, "Which branch?") {
		t.Fatalf("the dialog shows the question:\n%s", s)
	}
	key(m, "enter")
	if m.ov.kind != ovTaskReply {
		t.Fatalf("Enter on a waiting run replies: %v", m.ov.kind)
	}
	typeText(m, "main")
	for i, l := range strings.Split(m.screen(), "\n") {
		if w := ansi.StringWidth(l); w != m.w {
			t.Fatalf("line %d is %d wide, not %d", i, w, m.w)
		}
	}
	key(m, "ctrl+s")
	waitFor(t, m, func() bool { return len(launched) == 2 })
	if launched[1].Session != launched[0].Session {
		t.Fatalf("the reply continues the session: %+v", launched[1])
	}
	first := m.tasks.st.RunsOf(tk.ID)[0]
	var next *task.Run
	waitFor(t, m, func() bool { rs := m.tasks.st.RunsOf(tk.ID); next = rs[len(rs)-1]; return len(rs) == 2 })
	if next.Resume != first.Session || next.Brief != "main" && next.Brief != "" {
		t.Fatalf("%+v", next)
	}
}

func TestTasksThatNeedYouComeFirstLongestWaitingFirst(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	waitFor(t, m, func() bool { return m.tasks.loaded })
	at := func(min int) *time.Time { x := time.Date(2026, 9, 26, 10, min, 0, 0, time.UTC); return &x }
	two := 2
	st := task.New()
	add := func(id string, q int, r *task.Run) {
		st.Tasks[id] = &task.Task{ID: id, Title: id, Status: task.StatusTodo, CreatedAt: *at(q)}
		if r != nil {
			r.ID, r.Task, r.QueuedAt = "r"+id, id, *at(q)
			st.Runs[r.ID] = r
		}
	}
	add("t_new", 9, nil)
	add("t_late", 1, &task.Run{State: task.Exited, ExitCode: &two, EndedAt: at(8)})
	add("t_early", 2, &task.Run{State: task.Failed, EndedAt: at(3)})
	m.tasks.st = st
	m.filterTasks()
	var got []string
	for _, x := range m.tasks.list {
		got = append(got, x.ID)
	}
	if strings.Join(got, " ") != "t_early t_late t_new" {
		t.Fatal(got)
	}
	if s := screenText(m); !strings.Contains(s, "2 need you") && !strings.Contains(s, "2 个等你处理") {
		t.Fatalf("the title counts them:\n%s", s)
	}
}
