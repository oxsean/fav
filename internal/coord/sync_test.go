package coord

import (
	"slices"
	"sync"
	"testing"

	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func TestAnIssueBecomesARequirementThatFollowsIt(t *testing.T) {
	e := team(t, tend.Config{})
	var mu sync.Mutex
	var notices []Notice
	e.notice = func(n Notice) { mu.Lock(); notices = append(notices, n); mu.Unlock() }
	e.start()
	e.project()
	sys := e.as(System)
	issue := TaskSync{Project: "p1", Kind: "gitea", Tracker: "tr1", Base: "http://git", Repo: "o/r", RepoID: 9, Number: 4,
		Title: "Export CSV", Text: "rows as CSV", Digest: "d1", Owner: "u_nobody", Unmapped: true}
	if err := callAs(e.as(root), MTaskSync, "s0", issue, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("only the server itself syncs, not even an admin: %v", err)
	}
	var x task.Task
	if err := callAs(sys, MTaskSync, "s1", issue, &x); err != nil {
		t.Fatal(err)
	}
	if x.Kind != task.KindRequirement || x.Owner != ann.User || !slices.Contains(x.Tags, TagUnmapped) || x.Brief != "rows as CSV" ||
		x.Source.Rev != 1 || x.Source.Number != 4 {
		t.Fatalf("an unmapped assignee leaves it with the project's owner: %+v %+v", x, x.Source)
	}
	var again task.Task
	if err := callAs(sys, MTaskSync, "s2", issue, &again); err != nil || again.ID != x.ID || again.Rev != x.Rev {
		t.Fatalf("the same issue read again is the same task, unchanged: %+v %v", again, err)
	}
	if err := callAs(e.as(cy), MTaskGet, "", task.RunRef{ID: x.ID}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("someone outside p1 never sees it: %v", err)
	}
	issue.Owner, issue.Unmapped = bob.User, false
	issue.Digest, issue.Title, issue.Text = "d2", "Export CSV and TSV", "rows as CSV or TSV"
	x = task.Task{}
	if err := callAs(sys, MTaskSync, "s3", issue, &x); err != nil {
		t.Fatal(err)
	}
	if x.Owner != bob.User || slices.Contains(x.Tags, TagUnmapped) || x.Title != "Export CSV" || x.Source.Pending == nil {
		t.Fatalf("the assignee maps now, the change waits: %+v %+v", x, x.Source)
	}
	e.until("the change waits", func(st *task.State) bool { return st.Situation(st.Tasks[x.ID]).Reason == task.WhySourceChanged })
	e.until("its owner hears of it", func(*task.State) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.ContainsFunc(notices, func(n Notice) bool {
			return n.Task == x.ID && n.Reason == task.WhySourceChanged && slices.Contains(n.To, bob.User)
		})
	})
	if err := callAs(e.as(bob), MTaskStatus, "st1", task.TaskStatus{ID: x.ID, Status: task.StatusDone}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("not done while a change is open: %v", err)
	}
	if err := callAs(e.as(dee), MTaskSourceAck, "a0", task.SourceAck{ID: x.ID, Accept: true}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a reader decides nothing: %v", err)
	}
	x = task.Task{ID: x.ID}
	if err := callAs(e.as(bob), MTaskSourceAck, "a1", task.SourceAck{ID: x.ID, Accept: true}, &x); err != nil || x.Title != "Export CSV and TSV" ||
		x.Brief != "rows as CSV or TSV" || x.Source.Rev != 2 {
		t.Fatalf("taken: %+v %v", x, err)
	}
	if err := callAs(e.as(bob), MTaskSourceAck, "a2", task.SourceAck{ID: x.ID}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("nothing left to decide: %v", err)
	}
	issue.Closed = true
	var shut task.Task
	if err := callAs(sys, MTaskSync, "s4", issue, &shut); err != nil || task.SourceWaits(&shut) != task.WhySourceClosed {
		t.Fatalf("closed outside tend: %+v %v", shut.Source, err)
	}
	closed := issue
	closed.Number, closed.Digest = 5, "z"
	var none *task.Task
	if err := callAs(sys, MTaskSync, "s5", closed, &none); err != nil || none != nil {
		t.Fatalf("a closed issue is not imported: %+v %v", none, err)
	}
}

func TestTheServersOwnCommandReplaysByItsID(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	sys := e.as(System)
	issue := TaskSync{Project: "p1", Kind: "gitea", Tracker: "tr1", Base: "http://git", Repo: "o/r", RepoID: 9, Number: 4,
		Title: "Export CSV", Text: "rows as CSV", Digest: "d1"}
	var x task.Task
	if err := callAs(sys, MTaskSync, "s1", issue, &x); err != nil {
		t.Fatal(err)
	}
	closed := issue
	closed.Closed = true
	if err := callAs(sys, MTaskSync, "s2", closed, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(sys, MTaskSync, "s3", issue, nil); err != nil {
		t.Fatal(err)
	}
	var again task.Task
	if err := callAs(sys, MTaskSync, "s2", closed, &again); err != nil || !again.Source.Closed {
		t.Fatalf("a replay answers what it answered first: %+v %v", again.Source, err)
	}
	e.c.Read(func(st *task.State) {
		if st.Tasks[x.ID].Source.Closed {
			t.Fatalf("and changes nothing: %+v", st.Tasks[x.ID].Source)
		}
	})
}
