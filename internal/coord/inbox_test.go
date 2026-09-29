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
