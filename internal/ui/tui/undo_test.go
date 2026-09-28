package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/task"
)

func TestUTakesBackTheLastRecordAction(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	if r == nil {
		t.Fatal("no row")
	}
	was, status := r.Favorite(), r.Status
	m.Update(press("f"))
	if !strings.Contains(m.notice, "u") || m.undo == nil {
		t.Fatalf("the notice offers undo: %q", m.notice)
	}
	m.Update(press("u"))
	if got := m.store.Get(r.ID); got == nil || got.Favorite() != was {
		t.Fatalf("u restores the favorite: %+v", got)
	}

	m.Update(press("x"))
	m.Update(press("ctrl+z"))
	if got := m.store.Get(r.ID); got.Status != status {
		t.Fatalf("Ctrl+Z restores the status %q: %q", status, got.Status)
	}

	m.Update(press("u"))
	if m.notice != "Nothing to undo" && m.notice != "没有可撤销的" {
		t.Fatalf("one undo per action: %q", m.notice)
	}

	m.Update(press("a"))
	m.undo.at = time.Now().Add(-undoFor - time.Second)
	m.Update(press("u"))
	if got := m.store.Get(r.ID); !got.Archived() {
		t.Fatal("an old action stays")
	}
}

func TestUReopensATaskJustMarkedDone(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	var tk task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "one", Dir: t.TempDir()}, &tk); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 1 })
	key(m, "x")
	waitFor(t, m, func() bool { return m.tasks.st.Tasks[tk.ID].Status == task.StatusDone && m.undo != nil })
	key(m, "u")
	waitFor(t, m, func() bool { return m.tasks.st.Tasks[tk.ID].Status == task.StatusTodo })
}

func TestARangeOfTasksIsMarkedDoneTogetherAndRunTogether(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	for _, title := range []string{"one", "two", "three"} {
		if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c-"+title, coord.TaskCreate{Title: title, Dir: t.TempDir(), Agent: "fake"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 3 })
	key(m, "o") // the list keeps what is done
	m.tasks.cursor = 0
	key(m, "shift+down")
	key(m, "shift+down")
	if len(m.tasks.marked) != 3 {
		t.Fatalf("Shift+Down marks from the start of the range: %v", m.tasks.marked)
	}
	if s := screenText(m); strings.Count(s, "*") < 3 {
		t.Fatalf("marked rows show it:\n%s", s)
	}
	key(m, "x")
	allAre := func(status string) bool {
		for _, x := range m.tasks.st.Tasks {
			if x.Status != status {
				return false
			}
		}
		return true
	}
	waitFor(t, m, func() bool { return allAre(task.StatusDone) && m.undo != nil })
	if len(m.tasks.marked) != 0 {
		t.Fatal("the marks go once acted on")
	}
	key(m, "u")
	waitFor(t, m, func() bool { return allAre(task.StatusTodo) })

	m.tasks.cursor = 0
	key(m, "shift+down")
	key(m, "enter")
	if m.ov.kind != ovTaskRun || len(m.ov.taskIDs) != 2 {
		t.Fatalf("Enter on a range opens one run dialog for them all: %v %v", m.ov.kind, m.ov.taskIDs)
	}
	if s := screenText(m); !strings.Contains(s, "2 个任务") && !strings.Contains(s, "2 tasks") {
		t.Fatalf("the dialog says how many:\n%s", s)
	}
	key(m, "enter")
	waitFor(t, m, func() bool { return len(m.tasks.st.Runs) == 2 })

	key(m, "shift+down")
	key(m, "esc")
	if len(m.tasks.marked) != 0 {
		t.Fatal("Esc clears the marks")
	}
}

func TestARunWatchedBesideStaysUnderTheDetail(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	var a task.Task
	for _, title := range []string{"first", "second"} {
		var tk task.Task
		if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c-"+title, coord.TaskCreate{Title: title, Dir: t.TempDir(), Agent: "fake"}, &tk); err != nil {
			t.Fatal(err)
		}
		if err := m.tasks.cl.CallCommand(t.Context(), coord.MRunDispatch, "d-"+title, coord.Dispatch{Task: tk.ID, Machine: coord.Local, Agent: "fake"}, nil); err != nil {
			t.Fatal(err)
		}
		if title == "first" {
			a = tk
		}
	}
	waitFor(t, m, func() bool { return len(m.tasks.st.Runs) == 2 })
	key(m, "o")
	for m.selectedTask().ID != a.ID {
		key(m, "j")
	}
	key(m, "enter")
	for _, b := range m.taskButtons() {
		if b.label == "盯在旁边" || b.label == "Watch beside" {
			b.act(m)
		}
	}
	if m.tasks.watch == "" {
		t.Fatal("the task dialog watches its run")
	}
	key(m, "k")
	if m.selectedTask().ID == a.ID {
		t.Fatal("another task is selected")
	}
	waitFor(t, m, func() bool {
		o, ok := m.tasks.out[m.tasks.watch]
		return ok && o.text != "" && strings.Contains(screenText(m), m.tasks.watch)
	})
	if s := screenText(m); !strings.Contains(s, "first") || !strings.Contains(s, "step two") {
		t.Fatalf("the watched run's task and output stay in sight:\n%s", s)
	}
	for i, l := range strings.Split(m.screen(), "\n") {
		if w := ansi.StringWidth(l); w != m.w {
			t.Fatalf("line %d is %d wide, not %d", i, w, m.w)
		}
	}
}
