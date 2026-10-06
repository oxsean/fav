package task

import (
	"testing"
	"time"

	"github.com/oxsean/fav/internal/journal"
)

func TestATreeDoneSaysHowItCameToBeDone(t *testing.T) {
	w := newWorld(t)
	root := w.task("", nil, StatusBacklog)
	api := w.task(root, nil, StatusBacklog)
	model := w.task(api, nil, StatusBacklog)
	ui := w.task(root, nil, StatusBacklog)
	dropped := w.task(root, nil, StatusBacklog)
	w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: dropped, Status: StatusCanceled}))
	w.do(journal.NewEvent(ETaskStarted, TaskStart{IDs: []string{root, api, model, ui}}))
	var first time.Time
	for range 3 {
		for _, id := range w.settle(nil) {
			at := w.at.Add(time.Second)
			if first.IsZero() {
				first = at
			}
			w.endWith(id, Observation{ID: id, State: Exited, NodeRev: 9, StartedAt: &at}, 0)
		}
		w.settle(nil)
		if w.s.Situation(w.s.Tasks[api]).Reason == WhyAccept {
			w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: api, Status: StatusDone}))
		}
	}
	w.s.Tasks[root].Branch = "tend/root"
	w.s.Tasks[api].Merged = true
	if sit := w.s.Situation(w.s.Tasks[root]); sit.Reason != WhyAccept || w.s.TreeDone(w.s.Tasks[root]) {
		t.Fatalf("a tree waiting to be accepted is not done: %+v", sit)
	}
	w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: root, Status: StatusDone}))
	x := w.s.Tasks[root]
	if !x.DoneAt.Equal(w.at) || !w.s.TreeDone(x) || w.s.TreeDone(w.s.Tasks[api]) || w.s.TreeDone(w.s.Tasks[model]) {
		t.Fatalf("only the root of a tree is a tree done: %v", x.DoneAt)
	}
	got := w.s.TreeSummary(root)
	want := TreeSummary{Leaves: 2, Done: 2, Canceled: 1, Runs: 2, Started: first, DoneAt: w.at, Branch: "tend/root"}
	if got != want {
		t.Fatalf("summary\n got %+v\nwant %+v", got, want)
	}

	w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: root, Status: StatusTodo}))
	if x := w.s.Tasks[root]; !x.DoneAt.IsZero() || w.s.TreeDone(x) {
		t.Fatalf("a reopened tree is not done: %v", x.DoneAt)
	}
	before := RestoreOf(w.s.Tasks[root])
	w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: root, Status: StatusDone}))
	again := w.at
	w.do(journal.NewEvent(ETaskRestored, before))
	if !w.s.Tasks[root].DoneAt.IsZero() {
		t.Fatal("an undone done is not done")
	}
	w.do(journal.NewEvent(ETaskRestored, TaskRestore{ID: root, Status: StatusDone, DoneAt: again}))
	if !w.s.Tasks[root].DoneAt.Equal(again) {
		t.Fatal("an undone reopening puts the done time back")
	}
}

func TestATaskWithoutLiveSubtasksIsNoTree(t *testing.T) {
	w := newWorld(t)
	alone := w.task("", nil, StatusTodo)
	root := w.task("", nil, StatusTodo)
	kid := w.task(root, nil, StatusTodo)
	w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: kid, Status: StatusCanceled}))
	w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: alone, Status: StatusDone}), journal.NewEvent(ETaskStatus, TaskStatus{ID: root, Status: StatusDone}))
	if w.s.TreeDone(w.s.Tasks[alone]) || w.s.TreeDone(w.s.Tasks[root]) {
		t.Fatal("a task alone, or one whose subtasks were all canceled, is no tree done")
	}
}
