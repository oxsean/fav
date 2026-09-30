package coord

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

func TestATaskThatNeedsSomeoneReachesThePeopleItConcerns(t *testing.T) {
	e := team(t, tend.Config{Agents: []tend.AgentProfile{asker("asker", "ASK: Ship it?")}})
	var mu sync.Mutex
	var got []Notice
	e.notice = func(n Notice) { mu.Lock(); got = append(got, n); mu.Unlock() }
	e.start()
	e.project()
	asks := e.taskAs(bob, "asks", "p1", "asker")
	var r task.Run
	if err := callAs(e.as(bob), MRunDispatch, "d1", Dispatch{Task: asks.ID, Runner: "background"}, &r); err != nil {
		t.Fatal(err)
	}
	e.wait(r.ID, ended)
	e.taskAs(bob, "not started", "p1", "")
	parent := e.taskAs(bob, "parent", "p1", "")
	if err := callAs(e.as(bob), MTaskEdit, "e1", task.TaskEdit{ID: parent.ID, Approver: ptr(ann.User)}, nil); err != nil {
		t.Fatal(err)
	}
	var kid task.Task
	if err := callAs(e.as(bob), MTaskCreate, "k1", TaskCreate{Title: "kid", Parent: parent.ID, Agent: "quick", Dir: t.TempDir()}, &kid); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(bob), MTaskStart, "s1", TaskRef{ID: parent.ID}, nil); err != nil {
		t.Fatal(err)
	}
	e.until("the parent waits to be accepted", func(st *task.State) bool { return st.Situation(st.Tasks[parent.ID]).Reason == task.WhyAccept })
	mu.Lock()
	byTask := map[string]Notice{}
	for _, n := range got {
		if n.Event == NotifyTaskWaiting {
			byTask[n.Task] = n
		}
	}
	mu.Unlock()
	if n := byTask[asks.ID]; n.Reason != task.AttentionAsked || !slices.Equal(n.To, []string{bob.User}) {
		t.Fatalf("the asking run's task reaches its owner and dispatcher: %+v", n)
	}
	if n := byTask[parent.ID]; n.Reason != task.WhyAccept || !slices.Equal(n.To, []string{bob.User, ann.User}) {
		t.Fatalf("a task to accept reaches its approver too: %+v", n)
	}
	var in Inbox
	if err := callAs(e.as(ann), MInboxList, "", nil, &in); err != nil || len(in.Items) != 1 || in.Items[0].Task != parent.ID || !slices.Equal(in.Items[0].As, []string{AsApprover}) {
		t.Fatalf("ann's inbox holds what she accepts: %+v %v", in, err)
	}
	if err := callAs(e.as(dee), MInboxList, "", nil, &in); err != nil || len(in.Items) != 0 {
		t.Fatalf("a reader has nothing to act on: %+v %v", in, err)
	}
	if err := callAs(e.as(bob), MInboxList, "", nil, &in); err != nil || len(in.Items) != 2 || in.Items[0].Task != asks.ID ||
		!slices.Equal(in.Items[0].As, []string{AsOwner, AsDispatcher}) {
		t.Fatalf("bob's, longest waiting first: %+v %v", in, err)
	}
}

func TestTheNotifyCommandHearsTaskEventsItNames(t *testing.T) {
	out := filepath.Join(t.TempDir(), "events")
	e := newEnv(t, tend.Config{NotifyCommand: []string{os.Args[0], "_notify", out}, NotifyEvents: []string{NotifyTaskDone}})
	e.start()
	x := e.task("auto", "quick")
	e.must(MTaskStart, TaskRef{ID: x.ID}, nil)
	e.until("done", status(x.ID, task.StatusDone))
	var ev NotifyEvent
	for deadline := time.Now().Add(10 * time.Second); ev.Event == "" && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		b, _ := os.ReadFile(out)
		json.Unmarshal([]byte(strings.TrimSpace(string(b))), &ev)
	}
	if ev.Event != NotifyTaskDone || ev.Task != x.ID || ev.Title != "auto" {
		t.Fatalf("%+v", ev)
	}
}

func TestATaskWithoutAnApproverIsItsOwnersToAccept(t *testing.T) {
	st := &task.State{}
	accept := task.Situation{Kind: task.SitWaiting, Reason: task.WhyAccept}
	if got := roles(st, &task.Task{Owner: bob.User}, accept, bob.User); !slices.Equal(got, []string{AsOwner, AsApprover}) {
		t.Fatalf("the owner accepts a task nobody else approves: %v", got)
	}
	named := &task.Task{Owner: bob.User, Approver: ann.User}
	if got := roles(st, named, accept, bob.User); !slices.Equal(got, []string{AsOwner}) {
		t.Fatalf("a named approver takes it from the owner: %v", got)
	}
	if got := roles(st, named, accept, ann.User); !slices.Equal(got, []string{AsApprover}) {
		t.Fatalf("the named approver: %v", got)
	}
}

// A notice goes when something new waits on someone: a request that comes while another is still open, or one that
// takes an answered one's place, though the task waits for the same reason all along. What still waits, or waits on
// in another shape, is not told again.
func TestANoticeGoesWhenAPendingItemAppearsOrIsReplaced(t *testing.T) {
	e := team(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
	e.dial = unreachable
	var mu sync.Mutex
	var got []Notice
	e.notice = func(n Notice) {
		if n.Event == NotifyTaskWaiting {
			mu.Lock()
			got = append(got, n)
			mu.Unlock()
		}
	}
	e.start()
	e.project()
	taken := func() []Notice { mu.Lock(); defer mu.Unlock(); out := got; got = nil; return out }
	tid, rid := e.scene("running", "p1", bob.User, 1)
	items := func(n Notice) []string {
		var out []string
		for _, p := range n.Items {
			out = append(out, p.ID)
		}
		return out
	}
	if ns := taken(); len(ns) != 1 || ns[0].Task != tid || !slices.Equal(items(ns[0]), []string{rid + "/q1", rid + "/q2"}) {
		t.Fatalf("a run that asks twice: %+v", ns)
	}
	observe := func(rev, turn int, reqs ...agent.Request) {
		t.Helper()
		e.c.mu.Lock()
		defer e.c.mu.Unlock()
		err := e.c.commit(journal.System, nil, journal.NewEvent(task.ERunObserved, task.Observation{ID: rid, State: task.Running, NodeRev: rev, Stream: true,
			Attention: task.AttentionPermission, Turn: turn, Caps: e.c.st.Runs[rid].Caps, Requests: reqs}))
		if err != nil {
			t.Fatal(err)
		}
	}
	q2 := agent.Request{ID: "q2", Kind: agent.RequestQuestion, Questions: []agent.Question{{Question: "which?", Options: []string{"a", "b"}}}}
	q3 := agent.Request{ID: "q3", Kind: agent.RequestPermission, Tool: "Bash", Summary: "rm -rf build"}
	observe(2, 1, q2, q3)
	if ns := taken(); len(ns) != 1 || !slices.Equal(items(ns[0]), []string{rid + "/q3"}) || !slices.Equal(ns[0].To, []string{bob.User}) {
		t.Fatalf("q3 in q1's place, the task waiting for a permission all along: %+v", ns)
	}
	observe(3, 2, q2, q3)
	observe(4, 2, q3)
	if ns := taken(); len(ns) != 0 {
		t.Fatalf("nothing new waits: %+v", ns)
	}
	observe(5, 2, q3, agent.Request{ID: "q4", Kind: agent.RequestPermission, Tool: "Edit"})
	if ns := taken(); len(ns) != 1 || !slices.Equal(items(ns[0]), []string{rid + "/q4"}) {
		t.Fatalf("another of the same kind while one is open: %+v", ns)
	}
}
