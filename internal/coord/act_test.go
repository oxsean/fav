package coord

import (
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// pendingOf is the item of task id that waits on p with the given id.
func (e *env) pendingOf(p Principal, id, item string) task.Pending {
	e.t.Helper()
	it, _ := e.c.Waiting(p.User, id)
	for _, q := range it.Pending {
		if q.ID == item {
			return q
		}
	}
	e.t.Fatalf("%s does not wait on %s: %+v", item, p.User, it)
	return task.Pending{}
}

// A notice's deny is offered only to whoever may deny what it asks, is done once however often and from however many
// devices it comes, and only while its item waits at the version the notice named.
func TestANoticesActionIsDoneOnceWhileItsItemWaits(t *testing.T) {
	e := stage(t, true)
	tid, rid := e.scene("running", "p1", bob.User, 1)
	perm := e.pendingOf(bob, tid, rid+"/q1")
	on := ActOn{Task: tid, Item: perm.ID, Version: perm.Version, Action: ActDeny}

	if n := e.c.WaitingCount(bob.User); n != 2 {
		t.Fatalf("a permission and a question wait on bob: %d", n)
	}
	if got := e.c.NoticeActs(bob.User, tid, perm); !slices.Equal(got, []string{ActDeny}) {
		t.Fatalf("the dispatcher may deny: %v", got)
	}
	if got := e.c.NoticeActs(dee.User, tid, perm); len(got) != 0 {
		t.Fatalf("a reader may not: %v", got)
	}
	if got := e.c.NoticeActs(bob.User, tid, e.pendingOf(bob, tid, rid+"/q2")); len(got) != 0 {
		t.Fatalf("a question has no deny: %v", got)
	}
	for _, bad := range []ActOn{{Task: tid, Item: perm.ID, Version: perm.Version, Action: "allow"}, {Task: tid, Item: tid + "/ended", Version: perm.Version, Action: ActDeny}} {
		if err := e.c.Act(bob.User, bad); wire.Code(err) != wire.CodeBadRequest {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	if err := e.c.Act(dee.User, on); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a reader's deny: %v", err)
	}
	if err := e.c.Act(cy.User, on); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("someone outside the project: %v", err)
	}
	stale := on
	stale.Version++
	if err := e.c.Act(bob.User, stale); wire.Code(err) != wire.CodeRequestGone || detailOf(err) != "" {
		t.Fatalf("another version of the item: %v", err)
	}

	if err := e.c.Act(bob.User, on); err != nil {
		t.Fatal(err)
	}
	if err := e.c.Act(bob.User, on); err != nil {
		t.Fatalf("the same deny again, as a second device sends it: %v", err)
	}
	as := e.c.State().Runs[rid].Answers
	if len(as) != 1 || as[0].Request != "q1" || as[0].Allow || as[0].Decision != agent.DecisionDeny || as[0].By != bob.User {
		t.Fatalf("one deny: %+v", as)
	}
	if err := e.c.Act(ann.User, ActOn{Task: tid, Item: perm.ID, Version: perm.Version, Action: ActDeny}); wire.Code(err) != wire.CodeRequestGone || detailOf(err) != bob.User {
		t.Fatalf("the machine's owner after bob: %v", err)
	}

	tid2, rid2 := e.scene("running", "p1", bob.User, 2)
	first := e.pendingOf(bob, tid2, rid2+"/q1")
	e.c.mu.Lock()
	err := e.c.commit(journal.System, nil, journal.NewEvent(task.ERunObserved, task.Observation{ID: rid2, State: task.Running, NodeRev: 2, Stream: true,
		Attention: task.AttentionPermission, Turn: 1, Caps: e.c.st.Runs[rid2].Caps, Requests: []agent.Request{{ID: "q3", Kind: agent.RequestPermission, Tool: "Edit"}}}))
	e.c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.c.Act(bob.User, ActOn{Task: tid2, Item: first.ID, Version: first.Version, Action: ActDeny}); wire.Code(err) != wire.CodeRequestGone || detailOf(err) != "" {
		t.Fatalf("a request another took the place of: %v", err)
	}

	next := e.pendingOf(bob, tid2, rid2+"/q3")
	e.usersM.Lock()
	u := e.users[bob.User]
	u.Disabled = true
	e.users[bob.User] = u
	e.usersM.Unlock()
	if got := e.c.NoticeActs(bob.User, tid2, next); len(got) != 0 {
		t.Fatalf("someone disabled: %v", got)
	}
	if err := e.c.Act(bob.User, ActOn{Task: tid2, Item: next.ID, Version: next.Version, Action: ActDeny}); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("someone disabled: %v", err)
	}
}
