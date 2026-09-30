package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/i18n"
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
		pump(m, nil)
	}
}

func TestTasksViewCreatesRunsAndShowsATask(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	if m.view != viewTasks || m.tasks.cl == nil || !m.tasks.loaded {
		t.Fatalf("5 opens the Tasks view connected: view %v err %v", m.view, m.tasks.err)
	}
	waitFor(t, m, func() bool { // machines.watch pushes them
		return len(m.tasks.machines) == 1 && m.tasks.machines[0].Name == coord.Local && len(m.tasks.agents) > 0
	})
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

	key(m, "o") // the list keeps finished tasks; the home does not
	key(m, "x")
	waitFor(t, m, func() bool { return m.tasks.st.Tasks[x.ID].Status == task.StatusDone })
	key(m, "u")
	waitFor(t, m, func() bool { // an undo is not a reopening: its run still says how it stands
		st := m.tasks.st
		return st.Tasks[x.ID].Status == task.StatusTodo && st.Situation(st.Tasks[x.ID]).Reason == task.WhyEnded
	})
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
	if got := m.View().WindowTitle; got != "tend · 2 need you" && got != "tend · 2 个等你" {
		t.Fatalf("the window title counts them: %q", got)
	}
}

func TestARunThatWaitsIsAnsweredAndSentAMessage(t *testing.T) {
	var runDir string
	m, _ := tasksModelWith(t, func(dir string, spec node.Spec) (string, error) {
		runDir = dir
		unlock, err := filelock.TryLock(filepath.Join(dir, "lock")) // a live supervisor
		if err != nil {
			return "", err
		}
		t.Cleanup(unlock)
		b := `{"rev":1,"state":"running","stream":true,"provider":"claude","session":"` + spec.Session + `","attention":"permission",` +
			`"requests":[{"id":"perm-1","kind":"permission","tool":"Bash","summary":"make deploy","at":"2026-09-27T10:00:00Z"}]}`
		return "", os.WriteFile(filepath.Join(dir, "state.json"), []byte(b), 0o600)
	})
	key(m, "5")
	key(m, "o") // the list layout: the home layout answers in place, tested separately
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "deploy", Dir: t.TempDir(), Agent: "fake"}, &tk); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	key(m, "enter")
	key(m, "enter")
	key(m, "enter")
	waitFor(t, m, func() bool { r := m.selectedRun(); return r != nil && len(r.Requests) == 1 })
	if s := screenText(m); !strings.Contains(s, "make deploy") {
		t.Fatalf("the task shows what it waits on:\n%s", s)
	}
	key(m, "enter")
	key(m, "enter")
	if m.ov.kind != ovTaskAnswer || !strings.Contains(screenText(m), "make deploy") {
		t.Fatalf("Enter on a waiting run answers it: %v\n%s", m.ov.kind, screenText(m))
	}
	for i, l := range strings.Split(m.screen(), "\n") {
		if w := ansi.StringWidth(l); w != m.w {
			t.Fatalf("line %d is %d wide, not %d", i, w, m.w)
		}
	}
	key(m, "enter")
	waitFor(t, m, func() bool {
		b, _ := os.ReadFile(filepath.Join(runDir, "answers.jsonl"))
		return strings.Contains(string(b), `"request":"perm-1","allow":true`)
	})
	m.openSend()
	if m.ov.kind != ovTaskReply || !m.ov.send {
		t.Fatalf("a running stream run takes a message: %v", m.ov.kind)
	}
	typeText(m, "use tabs")
	key(m, "ctrl+s")
	waitFor(t, m, func() bool {
		b, _ := os.ReadFile(filepath.Join(runDir, "inbox.jsonl"))
		return strings.Contains(string(b), "use tabs")
	})
}

func TestHomeAnswersAQuestionInPlace(t *testing.T) {
	var runDir string
	m, _ := tasksModelWith(t, func(dir string, spec node.Spec) (string, error) {
		runDir = dir
		unlock, err := filelock.TryLock(filepath.Join(dir, "lock"))
		if err != nil {
			return "", err
		}
		t.Cleanup(unlock)
		b := `{"rev":1,"state":"running","stream":true,"provider":"claude","session":"` + spec.Session + `","attention":"asked",` +
			`"requests":[{"id":"q-1","kind":"question","questions":[{"question":"Which?","options":["A","B","C"]}],"at":"2026-09-27T10:00:00Z"}]}`
		return "", os.WriteFile(filepath.Join(dir, "state.json"), []byte(b), 0o600)
	})
	key(m, "5")
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "deploy", Dir: t.TempDir(), Agent: "fake"}, &tk); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	key(m, "enter")
	key(m, "enter")
	key(m, "enter")
	waitFor(t, m, func() bool { r := m.selectedRun(); return r != nil && len(r.Requests) == 1 })
	if s := screenText(m); !strings.Contains(s, "Which?") || !strings.Contains(s, "1) A") {
		t.Fatalf("the home layout expands the question with numbered options:\n%s", s)
	}
	for i, l := range strings.Split(m.screen(), "\n") {
		if w := ansi.StringWidth(l); w != m.w {
			t.Fatalf("line %d is %d wide, not %d", i, w, m.w)
		}
	}
	key(m, "2") // picks option B
	key(m, "enter")
	if m.ov.active() {
		t.Fatalf("it answers in place, no dialog opens: %v", m.ov.kind)
	}
	waitFor(t, m, func() bool {
		b, _ := os.ReadFile(filepath.Join(runDir, "answers.jsonl"))
		return strings.Contains(string(b), `"Which?":"B"`)
	})
}

func TestHomeDeniesAPermissionWithAReason(t *testing.T) {
	var runDir string
	m, _ := tasksModelWith(t, func(dir string, spec node.Spec) (string, error) {
		runDir = dir
		unlock, err := filelock.TryLock(filepath.Join(dir, "lock"))
		if err != nil {
			return "", err
		}
		t.Cleanup(unlock)
		b := `{"rev":1,"state":"running","stream":true,"provider":"claude","session":"` + spec.Session + `","attention":"permission",` +
			`"requests":[{"id":"perm-1","kind":"permission","tool":"Bash","summary":"rm -rf build","at":"2026-09-27T10:00:00Z"}]}`
		return "", os.WriteFile(filepath.Join(dir, "state.json"), []byte(b), 0o600)
	})
	key(m, "5")
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "deploy", Dir: t.TempDir(), Agent: "fake"}, &tk); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	key(m, "enter")
	key(m, "enter")
	key(m, "enter")
	waitFor(t, m, func() bool { r := m.selectedRun(); return r != nil && len(r.Requests) == 1 })
	key(m, "n")
	if !m.tasks.askDeny {
		t.Fatal("n starts a deny reason")
	}
	typeText(m, "not now")
	key(m, "enter")
	if m.tasks.askDeny {
		t.Fatal("enter sends the deny and closes the reason field")
	}
	waitFor(t, m, func() bool {
		b, _ := os.ReadFile(filepath.Join(runDir, "answers.jsonl"))
		return strings.Contains(string(b), `"allow":false,"message":"not now"`)
	})
}

func TestTheAnswerDialogPicksByDigitOrTakesItsOwnWords(t *testing.T) {
	var runDir string
	m, _ := tasksModelWith(t, func(dir string, spec node.Spec) (string, error) {
		runDir = dir
		unlock, err := filelock.TryLock(filepath.Join(dir, "lock"))
		if err != nil {
			return "", err
		}
		t.Cleanup(unlock)
		b := `{"rev":1,"state":"running","stream":true,"provider":"claude","session":"` + spec.Session + `","attention":"asked",` +
			`"requests":[{"id":"q-1","kind":"question","questions":[{"question":"Which?","options":["A","B","C"]}],"at":"2026-09-27T10:00:00Z"}]}`
		return "", os.WriteFile(filepath.Join(dir, "state.json"), []byte(b), 0o600)
	})
	key(m, "5")
	key(m, "o") // list layout: the dialog is opened by hand, not answered in place
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "deploy", Dir: t.TempDir(), Agent: "fake"}, &tk); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	key(m, "enter")
	key(m, "enter")
	key(m, "enter")
	waitFor(t, m, func() bool { r := m.selectedRun(); return r != nil && len(r.Requests) == 1 })
	key(m, "enter")
	key(m, "enter")
	if m.ov.kind != ovTaskAnswer {
		t.Fatalf("enter opens the answer dialog: %v", m.ov.kind)
	}
	key(m, "3") // the digit picks the third option, C
	key(m, "tab")
	typeText(m, "actually, D")
	key(m, "enter") // the other field's own words win over the pick
	waitFor(t, m, func() bool {
		b, _ := os.ReadFile(filepath.Join(runDir, "answers.jsonl"))
		return strings.Contains(string(b), `"Which?":"actually, D"`)
	})
}

func TestTheAnswerDialogDeniesWithAReason(t *testing.T) {
	var runDir string
	m, _ := tasksModelWith(t, func(dir string, spec node.Spec) (string, error) {
		runDir = dir
		unlock, err := filelock.TryLock(filepath.Join(dir, "lock"))
		if err != nil {
			return "", err
		}
		t.Cleanup(unlock)
		b := `{"rev":1,"state":"running","stream":true,"provider":"claude","session":"` + spec.Session + `","attention":"permission",` +
			`"requests":[{"id":"perm-1","kind":"permission","tool":"Bash","summary":"rm -rf build","at":"2026-09-27T10:00:00Z"}]}`
		return "", os.WriteFile(filepath.Join(dir, "state.json"), []byte(b), 0o600)
	})
	key(m, "5")
	key(m, "o")
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "deploy", Dir: t.TempDir(), Agent: "fake"}, &tk); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	key(m, "enter")
	key(m, "enter")
	key(m, "enter")
	waitFor(t, m, func() bool { r := m.selectedRun(); return r != nil && len(r.Requests) == 1 })
	key(m, "enter")
	key(m, "enter")
	if m.ov.kind != ovTaskAnswer {
		t.Fatalf("enter opens the answer dialog: %v", m.ov.kind)
	}
	key(m, "tab") // to the reason field (no selectors on a permission ask)
	typeText(m, "too risky")
	key(m, "enter") // the reason field's Enter denies
	waitFor(t, m, func() bool {
		b, _ := os.ReadFile(filepath.Join(runDir, "answers.jsonl"))
		return strings.Contains(string(b), `"allow":false,"message":"too risky"`)
	})
}

func TestARowShowsItsTaskIDStageAgentAndLastLineWhenRunningAndWhatItWaitsOnOtherwise(t *testing.T) {
	m, _ := tasksModel(t)
	m.w = 220
	m.detail = true // the list alone gets the full width, room enough to keep every field
	key(m, "5")
	waitFor(t, m, func() bool { return m.tasks.loaded })
	st := task.New()
	now := time.Now()
	started := now.Add(-90 * time.Second)
	st.Tasks["t_run12345678"] = &task.Task{ID: "t_run12345678", Title: "build it", Stage: "implement", Status: task.StatusTodo, CreatedAt: now}
	st.Runs["r_run"] = &task.Run{ID: "r_run", Task: "t_run12345678", Agent: "dev-claude", Machine: "mba", State: task.Running,
		Last: "running gate", StartedAt: &started, QueuedAt: now}
	ended := now.Add(-5 * time.Minute)
	st.Tasks["t_wait87654321"] = &task.Task{ID: "t_wait87654321", Title: "review it", Status: task.StatusTodo, CreatedAt: now}
	st.Runs["r_wait"] = &task.Run{ID: "r_wait", Task: "t_wait87654321", State: task.Exited, Attention: task.AttentionAsked,
		Requests: []agent.Request{{ID: "q1", Kind: agent.RequestQuestion, Questions: []agent.Question{{Question: "which env?"}}}},
		EndedAt:  &ended, QueuedAt: now}
	m.tasks.st = st
	m.tasks.layout = layoutList
	m.filterTasks()
	s := screenText(m)
	for _, want := range []string{"t_run12", "implement", "mba", "dev-claude", "running gate", "t_wait87", "which env?"} {
		if !strings.Contains(s, want) {
			t.Fatalf("the row wants %q:\n%s", want, s)
		}
	}
}

func TestBoardCardsShowStageAgentMachineReworkAndSubtasks(t *testing.T) {
	m, _ := tasksModel(t)
	m.w = 260 // wide enough that a card's meta line keeps agent@machine, situation, rework and subtasks together
	key(m, "5")
	waitFor(t, m, func() bool { return m.tasks.loaded })
	st := task.New()
	st.Tasks["t_1"] = &task.Task{ID: "t_1", Title: "ship it", Status: task.StatusTodo, Loops: 2,
		Flow: &task.Flow{Stages: []task.Stage{{Name: "implement"}, {Name: "review"}, {Name: "accept"}}}, Stage: "review", CreatedAt: time.Now()}
	st.Tasks["t_1a"] = &task.Task{ID: "t_1a", Parent: "t_1", Status: task.StatusDone, CreatedAt: time.Now()}
	st.Tasks["t_1b"] = &task.Task{ID: "t_1b", Parent: "t_1", Status: task.StatusTodo, CreatedAt: time.Now()}
	st.Runs["r_1"] = &task.Run{ID: "r_1", Task: "t_1", Agent: "reviewer-codex", Machine: "mba", State: task.Running, QueuedAt: time.Now()}
	m.tasks.st = st
	m.tasks.layout = layoutBoard
	m.filterTasks()
	s := screenText(m)
	for _, want := range []string{"ship it", "implement > [review]", "reviewer-codex@mba", "↺2", "1/2"} {
		if !strings.Contains(s, want) {
			t.Fatalf("board card wants %q:\n%s", want, s)
		}
	}
	for i, l := range strings.Split(m.screen(), "\n") {
		if w := ansi.StringWidth(l); w != m.w {
			t.Fatalf("line %d is %d wide, not %d", i, w, m.w)
		}
	}
}

func TestTheBoardFilterAcceptsProjectStageAndNeedsYouTerms(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	waitFor(t, m, func() bool { return m.tasks.loaded })
	st := task.New()
	st.Tasks["t_a"] = &task.Task{ID: "t_a", Title: "alpha", Project: "p_1", Stage: "review", Status: task.StatusTodo, CreatedAt: time.Now()}
	st.Tasks["t_b"] = &task.Task{ID: "t_b", Title: "beta", Project: "p_2", Stage: "implement", Status: task.StatusTodo, CreatedAt: time.Now()}
	st.Runs["r_b"] = &task.Run{ID: "r_b", Task: "t_b", State: task.Failed, EndedAt: func() *time.Time { x := time.Now(); return &x }()}
	m.tasks.st = st

	m.search.SetValue("project:p_1")
	m.filterTasks()
	if got := ids(m); got != "t_a" {
		t.Fatalf("project: filters to that project: %q", got)
	}
	m.search.SetValue("stage:implement")
	m.filterTasks()
	if got := ids(m); got != "t_b" {
		t.Fatalf("stage: filters to that stage: %q", got)
	}
	m.search.SetValue("needs:you")
	m.filterTasks()
	if got := ids(m); got != "t_b" {
		t.Fatalf("needs:you filters to what needs you: %q", got)
	}
}

func ids(m *Model) string {
	var out []string
	for _, x := range m.tasks.list {
		out = append(out, x.ID)
	}
	return strings.Join(out, " ")
}

func TestTasksAreArrangedAsHomeListTreeOrBoard(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	waitFor(t, m, func() bool { return m.tasks.loaded })
	at := func(min int) time.Time { return time.Date(2026, 9, 26, 10, min, 0, 0, time.UTC) }
	st := task.New()
	add := func(id, parent, status string, q int, r *task.Run) {
		st.Tasks[id] = &task.Task{ID: id, Title: id, Parent: parent, Status: status, CreatedAt: at(q)}
		if r != nil {
			r.ID, r.Task, r.QueuedAt = "r"+id, id, at(q)
			st.Runs[r.ID] = r
		}
	}
	add("t_wait", "", task.StatusTodo, 1, &task.Run{State: task.Failed, EndedAt: func() *time.Time { x := at(2); return &x }()})
	add("t_run", "", task.StatusTodo, 2, &task.Run{State: task.Running})
	add("t_kid", "t_run", task.StatusTodo, 3, &task.Run{State: task.Running})
	add("t_later", "", task.StatusBacklog, 4, nil)
	add("t_done", "", task.StatusDone, 5, nil)
	m.tasks.st = st
	m.filterTasks()
	ids := func() string {
		var out []string
		for _, x := range m.tasks.list {
			out = append(out, x.ID)
		}
		return strings.Join(out, " ")
	}

	if m.tasks.layout != layoutHome || !strings.HasPrefix(ids(), "t_wait ") || len(m.tasks.list) != 3 || m.tasks.split != 1 {
		t.Fatalf("the home holds what waits, then what runs: %v %q split %d", m.tasks.layout, ids(), m.tasks.split)
	}
	s := screenText(m)
	for _, want := range []string{"t_wait", "t_run"} {
		if !strings.Contains(s, want) {
			t.Fatalf("home lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "t_done") || strings.Contains(s, "t_later") {
		t.Fatalf("the home leaves out what neither waits nor runs:\n%s", s)
	}

	key(m, "o")
	if m.tasks.layout != layoutList || len(m.tasks.list) != 5 {
		t.Fatalf("o goes on to the list of every task: %v %q", m.tasks.layout, ids())
	}

	key(m, "o")
	if m.tasks.layout != layoutTree || !strings.Contains(ids(), "t_run t_kid") || m.tasks.depth["t_kid"] != 1 || m.tasks.depth["t_run"] != 0 {
		t.Fatalf("the tree puts subtasks under their parents: %q %v", ids(), m.tasks.depth)
	}
	if !strings.Contains(screenText(m), "   ") {
		t.Fatal("the tree indents")
	}

	key(m, "o")
	if m.tasks.layout != layoutBoard {
		t.Fatalf("%v", m.tasks.layout)
	}
	var cols []int
	for _, c := range m.tasks.cols {
		cols = append(cols, len(c))
	}
	if fmt.Sprint(cols) != "[1 2 0 1 1]" {
		t.Fatalf("a column per situation, waiting running queued backlog done: %v", cols)
	}
	m.tasks.cursor = 0
	cell := func() string { c, r := m.boardAt(m.tasks.cursor); return fmt.Sprint(c, r) }
	key(m, "l")
	if cell() != "1 0" {
		t.Fatalf("l moves to the running column: %s", cell())
	}
	key(m, "j")
	key(m, "l")
	if cell() != "3 0" || m.selectedTask().ID != "t_later" {
		t.Fatalf("l skips the empty column and keeps to the last row there: %s", cell())
	}
	key(m, "h")
	if cell() != "1 0" {
		t.Fatalf("h moves back left: %s", cell())
	}
	for i, l := range strings.Split(m.screen(), "\n") {
		if w := ansi.StringWidth(l); w != m.w {
			t.Fatalf("line %d is %d wide, not %d", i, w, m.w)
		}
	}

	key(m, "o")
	if m.tasks.layout != layoutHome {
		t.Fatalf("o comes back to the home: %v", m.tasks.layout)
	}
}

func TestTheHomeShowsRunsAsTheyStand(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	waitFor(t, m, func() bool { return m.tasks.loaded })
	zero := 0
	st := task.New()
	add := func(id string, r task.Run) {
		st.Tasks[id] = &task.Task{ID: id, Title: id, Status: task.StatusTodo}
		r.ID, r.Task = "r"+id, id
		st.Runs[r.ID] = &r
	}
	add("t_ok", task.Run{State: task.Exited, ExitCode: &zero})
	add("t_run", task.Run{State: task.Running})
	add("t_q", task.Run{State: task.Queued})
	m.tasks.st = st
	m.filterTasks()
	if got := ids(m); got != "t_ok t_run" {
		t.Fatalf("the home holds what waits, then what runs, not what queues: %q", got)
	}
	s := screenText(m)
	if !strings.Contains(s, i18n.T("sit.ended")) || strings.Contains(s, i18n.T("sit.exited")) {
		t.Fatalf("a run that exited 0 ended well:\n%s", s)
	}
	if want := i18n.F("tasks.title", m.openTaskCount(), 3); !strings.Contains(s, want) {
		t.Fatalf("the title counts every matching task, not the rows the home shows: want %q\n%s", want, s)
	}
	key(m, "o")
	if !strings.Contains(screenText(m), i18n.T("sit.slot")) {
		t.Fatalf("a queued task in the list says what it waits for:\n%s", screenText(m))
	}
}

func TestAQuestionShowsOnce(t *testing.T) {
	q := "Which database for the cache?"
	r := &task.Run{State: task.Running, Attention: task.AttentionAsked, Ask: q,
		Requests: []agent.Request{{ID: "q1", Kind: agent.RequestQuestion, Questions: []agent.Question{{Question: q}}}}}
	if n := strings.Count(ansi.Strip(strings.Join(runFacts(r, 100, 4), "\n")), q); n != 1 {
		t.Fatalf("the question shows %d times", n)
	}
}

func TestTheHomeAskSaysWhatThePickedOptionMeans(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	waitFor(t, m, func() bool { return m.tasks.loaded })
	st := task.New()
	st.Tasks["t_ask"] = &task.Task{ID: "t_ask", Title: "t_ask", Status: task.StatusTodo}
	q := agent.Question{Question: "Which DB?", Options: []string{"pg", "sqlite"}, Descriptions: []string{"Postgres, as production runs", "One file, for tests"}}
	st.Runs["r1"] = &task.Run{ID: "r1", Task: "t_ask", State: task.Running, Attention: task.AttentionAsked, Ask: q.Question,
		Requests: []agent.Request{{ID: "q1", Kind: agent.RequestQuestion, Questions: []agent.Question{q}}}}
	m.tasks.st = st
	m.filterTasks()
	if s := screenText(m); !strings.Contains(s, "Postgres, as production runs") || strings.Contains(s, "One file, for tests") {
		t.Fatalf("the first option is picked and says what it means:\n%s", s)
	}
	key(m, "2")
	if s := screenText(m); !strings.Contains(s, "One file, for tests") || strings.Contains(s, "Postgres, as production runs") {
		t.Fatalf("picking the second says what it means:\n%s", s)
	}
}

func TestTheRunDialogAddsAWordUnderThisRunsBrief(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "word", Brief: "Fix it.", Dir: t.TempDir(), Agent: "fake"}, &tk); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	key(m, "enter")
	key(m, "enter")
	if m.ov.kind != ovTaskRun {
		t.Fatalf("%v", m.ov.kind)
	}
	label := i18n.T("tasks.field_run_note")
	rows := strings.Split(screenText(m), "\n")
	y := slices.IndexFunc(rows, func(l string) bool { return strings.Contains(l, label) })
	if y < 0 {
		t.Fatalf("the run dialog offers a word:\n%s", screenText(m))
	}
	click(m, strings.Index(rows[y+1], "│")+4, y+1)
	if m.ov.field != runNote || !m.ov.edit.Focused() {
		t.Fatalf("a click on the word's row focuses it: field %d", m.ov.field)
	}
	typeText(m, " keep hjkl q ")
	if m.ov.kind != ovTaskRun || m.ov.edit.Value() != " keep hjkl q " {
		t.Fatalf("letters go into the word: %v %q", m.ov.kind, m.ov.edit.Value())
	}
	key(m, "enter")
	waitFor(t, m, func() bool { r := m.selectedRun(); return r != nil })
	st := task.New()
	if err := m.tasks.cl.Call(t.Context(), coord.MStateGet, coord.StateParams{}, st); err != nil {
		t.Fatal(err)
	}
	if len(st.Runs) != 1 {
		t.Fatalf("one run: %d", len(st.Runs))
	}
	for _, r := range st.Runs {
		if !strings.HasSuffix(r.Brief, "Fix it.\n\n---\n\nkeep hjkl q") {
			t.Fatalf("the word goes under this run's brief, trimmed: %q", r.Brief)
		}
	}
}

// The watched run's edit steps show a row per file with its counts, and every line of the frame stays the terminal's
// width however narrow it is (at 80 columns the list has no detail pane).
func TestTheOutputShowsEachEditedFileWithItsCounts(t *testing.T) {
	stat := `"tend":{"edits":[{"path":"internal/server/web/pages/tasks.js","op":"modify","add":7,"del":2,"hunks":3},{"path":"docs/notes.md","op":"add","add":12,"hunks":1}]}`
	change := `{"method":"item/completed","params":{"item":{"type":"fileChange","id":"f1","changes":[` +
		`{"path":"internal/server/web/pages/tasks.js","kind":{"type":"update"},"diff":"@@ -1 +1 @@\n-a\n+b\n"},` +
		`{"path":"docs/notes.md","kind":{"type":"add"},"diff":"x\n"}],"status":"completed"}},` + stat + `}`
	m, _ := tasksModelWith(t, func(dir string, spec node.Spec) (string, error) {
		now := time.Now()
		os.WriteFile(filepath.Join(dir, "output.log"), []byte(change+"\n"), 0o600)
		return "", writeState(dir, node.State{Rev: 1, State: node.StateExited, ExitCode: new(0), Provider: spec.Provider,
			Session: spec.Session, StartedAt: &now, EndedAt: &now})
	})
	key(m, "5")
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "edits", Dir: t.TempDir(), Agent: "fake"}, &tk); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	key(m, "enter")
	key(m, "enter")
	key(m, "enter")
	waitFor(t, m, func() bool {
		r := m.selectedRun()
		return r != nil && m.tasks.out[r.ID] != nil && m.tasks.out[r.ID].end
	})
	hunks, added := "+7 −2 · "+i18n.F("tasks.output_hunks", 3), i18n.F("tasks.output_new_file", 12)
	for _, size := range []struct {
		w, h   int
		detail bool
	}{{140, 40, true}, {100, 30, true}, {80, 24, false}} {
		m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		s := screenText(m)
		if size.detail && (!strings.Contains(s, "  "+hunks) || !strings.Contains(s, "notes.md") || !strings.Contains(s, "  "+added)) {
			t.Fatalf("%dx%d: a row per edited file with its counts:\n%s", size.w, size.h, s)
		}
		for i, l := range strings.Split(m.screen(), "\n") {
			if w := ansi.StringWidth(l); w != size.w {
				t.Fatalf("%dx%d: line %d is %d wide: %q", size.w, size.h, i, w, ansi.Strip(l))
			}
		}
	}
}
