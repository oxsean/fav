package tui

import (
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/task"
)

// b pauses dispatch under the selected task tree and resumes it; the detail says it is paused; a task that is no
// tree is refused without a write.
func TestBPausesAndResumesATaskTree(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	var leaf, need task.Task
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c1", coord.TaskCreate{Title: "leaf", Dir: t.TempDir()}, &leaf); err != nil {
		t.Fatal(err)
	}
	if err := m.tasks.cl.CallCommand(t.Context(), coord.MTaskCreate, "c2", coord.TaskCreate{Title: "need", Kind: task.KindRequirement}, &need); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, func() bool { return len(m.tasks.list) == 2 })
	at := func(id string) {
		for i, x := range m.tasks.list {
			if x.ID == id {
				m.tasks.cursor = i
			}
		}
	}
	at(leaf.ID)
	key(m, "b")
	if m.tasks.st.Tasks[leaf.ID].Paused != nil || !strings.Contains(screenText(m), i18n.T("tasks.pause_no_tree")) {
		t.Fatalf("a task with no subtasks is no tree:\n%s", screenText(m))
	}
	at(need.ID)
	key(m, "b")
	waitFor(t, m, func() bool { return m.tasks.st.Tasks[need.ID].Paused != nil })
	at(need.ID)
	if s := screenText(m); !strings.Contains(s, i18n.T("tasks.paused_line")) {
		t.Fatalf("the detail says it is paused:\n%s", s)
	}
	key(m, "enter")
	if s := screenText(m); m.ov.kind != ovTask || !strings.Contains(s, i18n.T("tasks.btn_resume")) {
		t.Fatalf("the task dialog offers to resume:\n%s", s)
	}
	key(m, "b")
	waitFor(t, m, func() bool { return m.tasks.st.Tasks[need.ID].Paused == nil })
}

// The home lists a paused tree once, as its root waiting to be resumed: the tasks under it are not waiting on you.
func TestTheHomeListsAPausedTreeOnce(t *testing.T) {
	m, _ := tasksModel(t)
	key(m, "5")
	if m.tasks.layout != layoutHome {
		t.Fatalf("the tasks open on the home: %v", m.tasks.layout)
	}
	call := func(id, method string, params, out any) {
		t.Helper()
		if err := m.tasks.cl.CallCommand(t.Context(), method, id, params, out); err != nil {
			t.Fatal(err)
		}
	}
	var root task.Task
	call("c0", coord.MTaskCreate, coord.TaskCreate{Title: "need", Kind: task.KindRequirement, Status: task.StatusBacklog}, &root)
	for _, title := range []string{"a", "b", "c"} {
		call("c-"+title, coord.MTaskCreate, coord.TaskCreate{Title: title, Parent: root.ID, Status: task.StatusBacklog}, nil)
	}
	call("p", coord.MTaskPause, task.TaskPause{ID: root.ID, On: true}, nil)
	call("s", coord.MTaskStart, coord.TaskRef{ID: root.ID}, nil)
	waitFor(t, m, func() bool {
		return len(m.tasks.st.Tasks) == 4 && m.tasks.st.Tasks[root.ID].Paused != nil && m.tasks.st.Tasks[root.ID].Auto
	})
	if len(m.tasks.list) != 1 || m.tasks.list[0].ID != root.ID || m.tasks.split != 1 {
		var got []string
		for _, x := range m.tasks.list {
			got = append(got, x.Title)
		}
		t.Fatalf("only the root waits: %v", got)
	}
	if s := screenText(m); !strings.Contains(s, i18n.T("sit.paused")) {
		t.Fatalf("it says why:\n%s", s)
	}
	key(m, "b")
	waitFor(t, m, func() bool { return m.tasks.st.Tasks[root.ID].Paused == nil })
}
