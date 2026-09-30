package server

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tracker"
	"github.com/oxsean/fav/internal/tracker/trackertest"
)

func (r *syncRig) settings(f func(*TrackerSettings)) {
	r.t.Helper()
	set := settingsOf(r.tracker())
	f(&set)
	b, _ := json.Marshal(set)
	if err := r.team.SetTrackerSettings(r.x.ID, string(b)); err != nil {
		r.t.Fatal(err)
	}
}

// status sets task id's status and is the command id that did it.
func (r *syncRig) status(id, status string) string {
	r.t.Helper()
	r.call(coord.Owner, coord.MTaskStatus, task.TaskStatus{ID: id, Status: status}, nil)
	return "c" + itoa(r.cmd)
}

func TestTendTakesBackItsOwnWriteBackWhenATaskIsNoLongerDone(t *testing.T) {
	for _, kind := range []string{tracker.KindGitea, tracker.KindGitHub, tracker.KindGitLab} {
		for _, mode := range []string{"close", "label"} {
			t.Run(kind+"/"+mode, func(t *testing.T) { takesBack(t, newSyncRigOf(t, kind), mode) })
		}
	}
}

func takesBack(t *testing.T, r *syncRig, mode string) {
	r.settings(func(s *TrackerSettings) { s.OnAccept = mode })
	r.g.Open(1, "Export CSV", "rows as CSV", "tend", "keep")
	r.pass(0)
	x := r.task(1)
	applied := func() bool {
		i := r.g.Get(1)
		if mode == "label" {
			return slices.Contains(i.Labels, "tend:accepted") && !i.Closed
		}
		return i.Closed && !slices.Contains(i.Labels, "tend:accepted")
	}
	taken := func() bool {
		i := r.g.Get(1)
		return !i.Closed && slices.Equal(i.Labels, []string{"tend", "keep"})
	}
	says := func(what string) bool {
		cs := r.g.CommentsOf(1)
		return len(cs) == 1 && strings.Contains(cs[0].Body, what)
	}

	r.status(x.ID, task.StatusDone)
	r.pass(31 * time.Second)
	if !applied() || !says("done") {
		t.Fatalf("accepted: %+v %+v", r.g.Get(1), r.g.CommentsOf(1))
	}
	r.pass(61 * time.Second)
	if y := r.task(1); y.Source.Closed {
		t.Fatalf("tend's own close is not the issue closed: %+v", y.Source)
	}

	r.status(x.ID, task.StatusTodo)
	r.pass(31 * time.Second)
	if !taken() || !says("not started") {
		t.Fatalf("reopened: tend takes its write-back back and the comment follows: %+v %+v", r.g.Get(1), r.g.CommentsOf(1))
	}
	r.pass(61 * time.Second)
	if sit := r.situation(x.ID); sit.Reason != task.WhyDispatch || r.task(1).Source.Closed {
		t.Fatalf("the reopened task waits to be started, not on its issue: %+v %+v", sit, r.task(1).Source)
	}

	done := r.status(x.ID, task.StatusDone)
	r.pass(31 * time.Second)
	if !applied() {
		t.Fatalf("done again, written back again: %+v", r.g.Get(1))
	}
	r.call(coord.Owner, coord.MTaskUndo, coord.TaskUndo{ID: x.ID, Command: done}, nil)
	r.pass(31 * time.Second)
	if !taken() || !says("not started") {
		t.Fatalf("an undone completion is taken back too: %+v %+v", r.g.Get(1), r.g.CommentsOf(1))
	}
	if row, _ := r.team.TrackerIssue(r.x.ID, 1); row.Closed || row.Applied != "" || row.LastError != "" {
		t.Fatalf("nothing of tend's stands on the issue: %+v", row)
	}

	r.status(x.ID, task.StatusDone)
	r.pass(31 * time.Second)
	r.status(x.ID, task.StatusCanceled)
	r.pass(31 * time.Second)
	if !applied() {
		t.Fatalf("a done task canceled is still finished: its write-back stays: %+v", r.g.Get(1))
	}
	r.status(x.ID, task.StatusTodo)
	r.pass(31 * time.Second)
	if !taken() {
		t.Fatalf("reopened from canceled: %+v", r.g.Get(1))
	}
}

func TestTendLeavesAloneWhatItDidNotDo(t *testing.T) {
	r := newSyncRig(t)
	r.g.Open(1, "Closed outside", "body", "tend")
	r.g.Open(2, "Reopened outside", "body", "tend")
	r.pass(0)
	a, b := r.task(1), r.task(2)
	r.g.Change(1, func(i *trackertest.Issue) { i.Closed = true })
	r.pass(61 * time.Second)
	if r.situation(a.ID).Reason != task.WhySourceClosed {
		t.Fatalf("closed outside tend: %+v", r.situation(a.ID))
	}
	r.call(coord.Owner, coord.MTaskSourceAck, task.SourceAck{ID: a.ID}, nil)
	r.status(a.ID, task.StatusDone)
	r.status(b.ID, task.StatusDone)
	r.pass(31 * time.Second)
	if !r.g.Get(2).Closed {
		t.Fatalf("tend closes #2: %+v", r.g.Get(2))
	}
	r.g.Change(2, func(i *trackertest.Issue) { i.Closed = false })
	r.pass(61 * time.Second)

	r.status(a.ID, task.StatusTodo)
	r.status(b.ID, task.StatusTodo)
	r.pass(31 * time.Second)
	r.pass(61 * time.Second)
	if !r.g.Get(1).Closed || r.g.Count("PATCH /api/v1/repos/acme/app/issues/1") != 0 {
		t.Fatalf("tend never reopens an issue someone closed outside it: %+v", r.g.Get(1))
	}
	if r.g.Get(2).Closed || r.g.Count("PATCH /api/v1/repos/acme/app/issues/2") != 1 {
		t.Fatalf("nor one whose close someone took back already: %d", r.g.Count("PATCH /api/v1/repos/acme/app/issues/2"))
	}
	r.g.Change(2, func(i *trackertest.Issue) { i.Closed = true })
	r.pass(61 * time.Second)
	if r.situation(b.ID).Reason != task.WhySourceClosed {
		t.Fatalf("closed again by someone: that close is theirs: %+v", r.situation(b.ID))
	}

	l := newSyncRigOf(t, tracker.KindGitHub)
	l.settings(func(s *TrackerSettings) { s.OnAccept = "label" })
	l.g.Open(3, "Labelled by hand", "body", "tend", "tend:accepted")
	l.pass(0)
	c := l.task(3)
	l.status(c.ID, task.StatusDone)
	l.pass(31 * time.Second)
	l.status(c.ID, task.StatusTodo)
	l.pass(31 * time.Second)
	if !slices.Contains(l.g.Get(3).Labels, "tend:accepted") {
		t.Fatalf("a label a person put on stays: %v", l.g.Get(3).Labels)
	}
}

func TestATakeBackThatFailsIsTriedAgain(t *testing.T) {
	cases := []struct{ kind, mode, key string }{
		{tracker.KindGitea, "close", "PATCH /api/v1/repos/acme/app/issues/1"},
		{tracker.KindGitHub, "label", "DELETE /api/v3/repos/acme/app/issues/1/labels/tend:accepted"},
		{tracker.KindGitLab, "close", "PUT /api/v4/projects/acme/app/issues/1"},
	}
	for _, c := range cases {
		t.Run(c.kind+"/"+c.mode, func(t *testing.T) {
			r := newSyncRigOf(t, c.kind)
			r.settings(func(s *TrackerSettings) { s.OnAccept = c.mode })
			r.g.Open(1, "Flaky", "body", "tend")
			r.pass(0)
			x := r.task(1)
			r.status(x.ID, task.StatusDone)
			r.pass(31 * time.Second)
			r.status(x.ID, task.StatusTodo)
			r.g.Break[c.key] = 1
			r.pass(31 * time.Second)
			i := r.g.Get(1)
			if row, _ := r.team.TrackerIssue(r.x.ID, 1); row.LastError == "" || !row.Closed || !(i.Closed || slices.Contains(i.Labels, "tend:accepted")) {
				t.Fatalf("the failure is recorded and tend's write-back still stands: %+v %+v", row, i)
			}
			if list, _ := r.s.TaskStates("p1"); len(list) != 1 || list[0].State != SyncFailed {
				t.Fatalf("the task says so: %+v", list)
			}
			r.pass(61 * time.Second)
			i = r.g.Get(1)
			if row, _ := r.team.TrackerIssue(r.x.ID, 1); row.LastError != "" || row.Closed || i.Closed || slices.Contains(i.Labels, "tend:accepted") {
				t.Fatalf("tried again a minute later: %+v %+v", row, i)
			}
		})
	}
}

func TestAnIssueWhoseBindingIsGoneIsNeverTouched(t *testing.T) {
	r := newSyncRig(t)
	r.g.Open(1, "Unbound", "body", "tend")
	r.pass(0)
	x := r.task(1)
	r.status(x.ID, task.StatusDone)
	r.pass(31 * time.Second)
	if err := r.team.RemoveTracker(r.x.ID); err != nil {
		t.Fatal(err)
	}
	r.status(x.ID, task.StatusTodo)
	n := r.g.Count("PATCH /api/v1/repos/acme/app/issues/1")
	r.pass(61 * time.Second)
	again := r.x
	var err error
	if r.x, err = r.team.AddTracker(again); err != nil {
		t.Fatal(err)
	}
	r.pass(61 * time.Second)
	r.pass(61 * time.Second)
	if !r.g.Get(1).Closed || r.g.Count("PATCH /api/v1/repos/acme/app/issues/1") != n {
		t.Fatalf("unbound, and bound anew, tend knows nothing it did there: %+v", r.g.Get(1))
	}
}

func TestASubIssueReopensWithItsSubtask(t *testing.T) {
	r := newSyncRig(t)
	r.settings(func(s *TrackerSettings) { s.SubIssues = true })
	r.g.Open(1, "Parent", "body", "tend")
	r.pass(0)
	x := r.task(1)
	var kid task.Task
	r.call(coord.Owner, coord.MTaskCreate, coord.TaskCreate{Title: "the exporter", Parent: x.ID}, &kid)
	r.pass(5 * time.Second)
	subs := r.g.Titled("the exporter")
	if len(subs) != 1 {
		t.Fatalf("a sub-issue: %+v", subs)
	}
	n := subs[0].Number
	r.status(kid.ID, task.StatusDone)
	r.pass(31 * time.Second)
	if !r.g.Get(n).Closed {
		t.Fatalf("closed with its subtask: %+v", r.g.Get(n))
	}
	r.status(kid.ID, task.StatusTodo)
	r.pass(31 * time.Second)
	if r.g.Get(n).Closed {
		t.Fatalf("and reopened with it: %+v", r.g.Get(n))
	}
}
