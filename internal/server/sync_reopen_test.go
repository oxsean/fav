package server

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tracker"
	"github.com/oxsean/fav/internal/tracker/trackertest"
)

func TestAnIssueReopenedOutsideReopensItsFinishedTask(t *testing.T) {
	for _, kind := range []string{tracker.KindGitea, tracker.KindGitHub, tracker.KindGitLab} {
		t.Run(kind+"/tend closed it", func(t *testing.T) { reopenedAfterTendClosed(t, newSyncRigOf(t, kind)) })
		for _, mode := range []string{"close", "label"} {
			t.Run(kind+"/"+mode+"/someone closed it", func(t *testing.T) { reopenedAfterTheyClosed(t, newSyncRigOf(t, kind), mode) })
		}
		t.Run(kind+"/canceled", func(t *testing.T) { reopenedAfterCanceled(t, newSyncRigOf(t, kind)) })
	}
}

// waitsOnReopen checks that task id is back to be done and waits on its issue reopened.
func (r *syncRig) waitsOnReopen(id, what string) {
	r.t.Helper()
	x := r.task(1)
	if x.Status != task.StatusTodo || !x.Source.Reopened || x.Source.Closed {
		r.t.Fatalf("%s: the task is back to be done: %s %+v", what, x.Status, x.Source)
	}
	if sit := r.situation(id); sit.Kind != task.SitWaiting || sit.Reason != task.WhySourceReopened {
		r.t.Fatalf("%s: it waits on the issue reopened: %+v", what, sit)
	}
	var in coord.Inbox
	r.call(coord.Principal{User: r.ann}, coord.MInboxList, struct{}{}, &in)
	if !slices.ContainsFunc(in.Items, func(i coord.InboxItem) bool { return i.Task == id && i.Reason == task.WhySourceReopened }) {
		r.t.Fatalf("%s: its owner is asked: %+v", what, in.Items)
	}
}

func reopenedAfterTendClosed(t *testing.T, r *syncRig) {
	r.g.Open(1, "Export CSV", "rows as CSV", "tend")
	r.pass(0)
	x := r.task(1)
	r.status(x.ID, task.StatusDone)
	r.pass(31 * time.Second)
	if !r.g.Get(1).Closed {
		t.Fatalf("tend closes it: %+v", r.g.Get(1))
	}
	r.g.Change(1, func(i *trackertest.Issue) { i.Closed = false })
	r.pass(61 * time.Second)
	r.waitsOnReopen(x.ID, "reopened after tend closed it")
	if row, _ := r.team.TrackerIssue(r.x.ID, 1); row.Closed || row.Applied != "" || r.g.Get(1).Closed {
		t.Fatalf("the issue stays open and nothing of tend's stands on it: %+v %+v", row, r.g.Get(1))
	}
	r.pass(61 * time.Second)
	if r.g.Get(1).Closed || r.task(1).Status != task.StatusTodo {
		t.Fatalf("and it stays that way: %+v %s", r.g.Get(1), r.task(1).Status)
	}
	if cs := r.g.CommentsOf(1); !slices.ContainsFunc(cs, func(c trackertest.Comment) bool {
		return c.Author == "tend-bot" && strings.Contains(c.Body, "the issue was reopened; waiting for a decision")
	}) {
		t.Fatalf("its progress comment says why it waits: %+v", cs)
	}

	r.call(coord.Owner, coord.MTaskSourceAck, task.SourceAck{ID: x.ID}, nil)
	if sit := r.situation(x.ID); sit.Reason != task.WhyDispatch || r.task(1).Source.Reopened {
		t.Fatalf("taken on: it waits to be started: %+v %+v", sit, r.task(1).Source)
	}
	r.status(x.ID, task.StatusDone)
	r.pass(31 * time.Second)
	if !r.g.Get(1).Closed {
		t.Fatalf("done again, tend closes it again: %+v", r.g.Get(1))
	}
	r.g.Change(1, func(i *trackertest.Issue) { i.Closed = false })
	r.pass(61 * time.Second)
	r.waitsOnReopen(x.ID, "reopened a second time")
	r.status(x.ID, task.StatusDone)
	r.pass(31 * time.Second)
	if x := r.task(1); x.Status != task.StatusDone || x.Source.Reopened || !r.g.Get(1).Closed {
		t.Fatalf("kept done: the flag goes and tend closes it again: %s %+v %+v", x.Status, x.Source, r.g.Get(1))
	}
}

func reopenedAfterTheyClosed(t *testing.T, r *syncRig, mode string) {
	r.settings(func(s *TrackerSettings) { s.OnAccept = mode })
	r.g.Open(1, "Export CSV", "rows as CSV", "tend")
	r.pass(0)
	x := r.task(1)
	r.g.Change(1, func(i *trackertest.Issue) { i.Closed = true })
	r.pass(61 * time.Second)
	r.call(coord.Owner, coord.MTaskSourceAck, task.SourceAck{ID: x.ID}, nil)
	r.status(x.ID, task.StatusDone)
	r.pass(31 * time.Second)
	if mode == "label" && !slices.Contains(r.g.Get(1).Labels, "tend:accepted") {
		t.Fatalf("labelled: %+v", r.g.Get(1))
	}
	r.g.Change(1, func(i *trackertest.Issue) { i.Closed = false })
	r.pass(61 * time.Second)
	r.waitsOnReopen(x.ID, "reopened after someone closed it")
	r.pass(31 * time.Second)
	i := r.g.Get(1)
	if i.Closed || slices.Contains(i.Labels, "tend:accepted") {
		t.Fatalf("no longer done: tend's label goes, the issue stays open: %+v", i)
	}
}

func reopenedAfterCanceled(t *testing.T, r *syncRig) {
	r.g.Open(1, "Export CSV", "rows as CSV", "tend")
	r.pass(0)
	x := r.task(1)
	r.g.Change(1, func(i *trackertest.Issue) { i.Closed = true })
	r.pass(61 * time.Second)
	r.status(x.ID, task.StatusCanceled)
	r.g.Change(1, func(i *trackertest.Issue) { i.Closed = false })
	r.pass(61 * time.Second)
	r.waitsOnReopen(x.ID, "reopened after its task was canceled")
}

func TestOnlyAClosedIssueReopenedReopensATask(t *testing.T) {
	r := newSyncRigOf(t, tracker.KindGitHub)
	r.settings(func(s *TrackerSettings) { s.OnAccept = "label" })
	r.g.Open(1, "Labelled", "body", "tend")
	r.g.Open(2, "Open all along", "body", "tend")
	r.pass(0)
	a, b := r.task(1), r.task(2)
	r.status(a.ID, task.StatusDone)
	r.status(b.ID, task.StatusDone)
	r.pass(31 * time.Second)
	r.g.Change(1, func(i *trackertest.Issue) {
		i.Labels = slices.DeleteFunc(i.Labels, func(l string) bool { return l == "tend:accepted" })
	})
	r.g.Change(2, func(i *trackertest.Issue) { i.Title = "Open all along, retitled" })
	r.pass(61 * time.Second)
	r.pass(61 * time.Second)
	for _, id := range []string{a.ID, b.ID} {
		if s := r.situation(id); s.Kind != task.SitDone {
			t.Fatalf("a label taken off or an edit is no reopen: %+v", s)
		}
	}

	u := newSyncRig(t)
	u.g.Open(1, "Unfinished", "body", "tend")
	u.pass(0)
	c := u.task(1)
	u.g.Change(1, func(i *trackertest.Issue) { i.Closed = true })
	u.pass(61 * time.Second)
	u.g.Change(1, func(i *trackertest.Issue) { i.Closed = false })
	u.pass(61 * time.Second)
	if s := u.situation(c.ID); s.Reason != task.WhyDispatch || u.task(1).Source.Reopened {
		t.Fatalf("an unfinished task only stops waiting on its issue closed: %+v %+v", s, u.task(1).Source)
	}
}

// An issue closed and opened again between two reads is seen in its events: a finished task reopens as it would had a
// read found the issue closed. Not when the task was unfinished at the last read, nor when the issue is closed again.
func TestAnIssueClosedAndOpenedBetweenTwoReadsReopensItsFinishedTask(t *testing.T) {
	for _, kind := range []string{tracker.KindGitea, tracker.KindGitHub, tracker.KindGitLab} {
		t.Run(kind+"/labelled", func(t *testing.T) { closedAndOpenedAfterLabelled(t, newSyncRigOf(t, kind)) })
		t.Run(kind+"/canceled", func(t *testing.T) { closedAndOpenedAfterCanceled(t, newSyncRigOf(t, kind)) })
		t.Run(kind+"/unfinished then", func(t *testing.T) { closedAndOpenedWhileUnfinished(t, newSyncRigOf(t, kind)) })
		t.Run(kind+"/closed again", func(t *testing.T) { openedAndClosedAfterTendClosed(t, newSyncRigOf(t, kind)) })
	}
}

// closeAndOpen closes issue n and opens it again, as someone would between two reads.
func (r *syncRig) closeAndOpen(n int64) {
	r.g.Change(n, func(i *trackertest.Issue) { i.Closed = true })
	r.g.Change(n, func(i *trackertest.Issue) { i.Closed = false })
}

func closedAndOpenedAfterLabelled(t *testing.T, r *syncRig) {
	r.settings(func(s *TrackerSettings) { s.OnAccept = "label" })
	r.g.Open(1, "Export CSV", "rows as CSV", "tend")
	r.pass(0)
	x := r.task(1)
	r.status(x.ID, task.StatusDone)
	r.pass(31 * time.Second)
	r.pass(61 * time.Second)
	if !slices.Contains(r.g.Get(1).Labels, "tend:accepted") || r.situation(x.ID).Kind != task.SitDone {
		t.Fatalf("labelled and done: %+v", r.g.Get(1))
	}
	r.closeAndOpen(1)
	r.pass(61 * time.Second)
	r.waitsOnReopen(x.ID, "closed and opened again after it was labelled")
	r.pass(31 * time.Second)
	if i := r.g.Get(1); i.Closed || slices.Contains(i.Labels, "tend:accepted") {
		t.Fatalf("no longer done: tend's label goes, the issue stays open: %+v", i)
	}
	r.pass(61 * time.Second)
	if r.task(1).Status != task.StatusTodo {
		t.Fatalf("told once: %s", r.task(1).Status)
	}
}

func closedAndOpenedAfterCanceled(t *testing.T, r *syncRig) {
	r.g.Open(1, "Export CSV", "rows as CSV", "tend")
	r.pass(0)
	x := r.task(1)
	r.status(x.ID, task.StatusCanceled)
	r.pass(61 * time.Second)
	r.closeAndOpen(1)
	r.pass(61 * time.Second)
	r.waitsOnReopen(x.ID, "closed and opened again after its task was canceled")
}

func closedAndOpenedWhileUnfinished(t *testing.T, r *syncRig) {
	r.g.Open(1, "Export CSV", "rows as CSV", "tend")
	r.pass(0)
	x := r.task(1)
	r.closeAndOpen(1)
	r.status(x.ID, task.StatusDone)
	r.pass(61 * time.Second)
	r.pass(61 * time.Second)
	if s := r.situation(x.ID); s.Kind != task.SitDone || !r.g.Get(1).Closed {
		t.Fatalf("closed and opened before its task was done: no reopen, and tend closes it: %+v %+v", s, r.g.Get(1))
	}
}

func openedAndClosedAfterTendClosed(t *testing.T, r *syncRig) {
	r.g.Open(1, "Export CSV", "rows as CSV", "tend")
	r.pass(0)
	x := r.task(1)
	r.status(x.ID, task.StatusDone)
	r.pass(31 * time.Second)
	r.pass(61 * time.Second)
	r.g.Change(1, func(i *trackertest.Issue) { i.Closed = false })
	r.g.Change(1, func(i *trackertest.Issue) { i.Closed = true })
	r.pass(61 * time.Second)
	r.pass(61 * time.Second)
	if s := r.situation(x.ID); s.Kind != task.SitDone || !r.g.Get(1).Closed {
		t.Fatalf("opened and closed again, it stays done: %+v %+v", s, r.g.Get(1))
	}
}
