package tui

import (
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/task"
)

// A tree coming to be done is told at the bottom once, and its detail says how it went; a subtask's detail does not.
func TestATreeDoneIsToldOnceAndItsDetailSaysHowItWent(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	m.tasks.layout = layoutList
	call := func(id, method string, params, out any) {
		t.Helper()
		if err := m.tasks.cl.CallCommand(t.Context(), method, id, params, out); err != nil {
			t.Fatal(err)
		}
	}
	var root, kid task.Task
	call("c0", coord.MTaskCreate, coord.TaskCreate{Title: "order flow"}, &root)
	call("c1", coord.MTaskCreate, coord.TaskCreate{Title: "order api", Parent: root.ID}, &kid)
	call("d1", coord.MTaskStatus, task.TaskStatus{ID: kid.ID, Status: task.StatusDone}, nil)
	waitFor(t, m, func() bool { x := m.tasks.st.Tasks[kid.ID]; return x != nil && x.Status == task.StatusDone })
	if strings.Contains(m.notice, i18n.F("tasks.tree_done_flash", "order flow", 1, 1)) {
		t.Fatal("not before the root is done")
	}
	call("d2", coord.MTaskStatus, task.TaskStatus{ID: root.ID, Status: task.StatusDone}, nil)
	flash := i18n.F("tasks.tree_done_flash", "order flow", 1, 1)
	waitFor(t, m, func() bool { a := m.tasks.fold.Aff.Tasks[root.ID]; return a != nil && a.TreeDone != nil })
	if !strings.Contains(m.notice, flash) {
		t.Fatalf("the bottom tells it: %q", m.notice)
	}
	at := func(id string) {
		for i, x := range m.tasks.list {
			if x.ID == id {
				m.tasks.cursor = i
			}
		}
	}
	at(root.ID)
	line := i18n.F("tasks.tree_done_line", 1, 1, 0)
	if s := screenText(m); !strings.Contains(s, line) {
		t.Fatalf("the root's detail says how it went:\n%s", s)
	}
	at(kid.ID)
	if s := screenText(m); strings.Contains(s, line) {
		t.Fatalf("not a subtask's:\n%s", s)
	}
	m.notice = ""
	title := "order flow v1"
	call("e1", coord.MTaskEdit, task.TaskEdit{ID: root.ID, Title: &title}, nil)
	waitFor(t, m, func() bool { return m.tasks.st.Tasks[root.ID].Title == "order flow v1" })
	if m.notice != "" {
		t.Fatalf("told once: %q", m.notice)
	}
}
