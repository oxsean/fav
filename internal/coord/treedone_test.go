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

// A tree's root coming to be done tells its owner and approver once, with how it went, besides each task's own done:
// a parent under it raises none, a restart nothing again, a reopening and a second done another.
func TestATreeDoneIsToldOnceToItsOwnerAndApprover(t *testing.T) {
	e := team(t, tend.Config{})
	var mu sync.Mutex
	var got []Notice
	e.notice = func(n Notice) { mu.Lock(); got = append(got, n); mu.Unlock() }
	taken := func(ev string) []Notice {
		mu.Lock()
		defer mu.Unlock()
		var out []Notice
		for _, n := range got {
			if n.Event == ev {
				out = append(out, n)
			}
		}
		got = slices.DeleteFunc(got, func(n Notice) bool { return n.Event == ev })
		return out
	}
	e.start()
	e.project()
	create := func(id, parent, agentName string) *task.Task {
		t.Helper()
		var x task.Task
		if err := callAs(e.as(bob), MTaskCreate, id, TaskCreate{Title: id, Parent: parent, Agent: agentName, Dir: e.t.TempDir(), Project: "p1"}, &x); err != nil {
			t.Fatal(err)
		}
		return &x
	}
	top := create("top", "", "")
	if err := callAs(e.as(bob), MTaskEdit, "e1", task.TaskEdit{ID: top.ID, Approver: ptr(ann.User)}, nil); err != nil {
		t.Fatal(err)
	}
	mid := create("mid", top.ID, "")
	leaf1 := create("leaf1", mid.ID, "quick")
	leaf2 := create("leaf2", top.ID, "quick")
	if err := callAs(e.as(bob), MTaskStart, "s1", TaskRef{ID: top.ID}, nil); err != nil {
		t.Fatal(err)
	}
	accept := func(id, as string) {
		t.Helper()
		e.until(id+" waits to be accepted", func(st *task.State) bool { return st.Situation(st.Tasks[id]).Reason == task.WhyAccept })
		p := map[string]Principal{bob.User: bob, ann.User: ann}[as]
		if err := callAs(e.as(p), MTaskStatus, "done-"+id+"-"+itoa(e.cmd.Add(1)), task.TaskStatus{ID: id, Status: task.StatusDone}, nil); err != nil {
			t.Fatal(err)
		}
	}
	accept(mid.ID, bob.User)
	if ns := taken(NotifyTaskTreeDone); len(ns) != 0 {
		t.Fatalf("a parent under a root is no tree done: %+v", ns)
	}
	accept(top.ID, ann.User)
	st := e.c.State()
	ns := taken(NotifyTaskTreeDone)
	if len(ns) != 1 || ns[0].Task != top.ID || !slices.Equal(ns[0].To, []string{bob.User, ann.User}) || ns[0].Summary == nil {
		t.Fatalf("one tree done, for the root's owner and approver: %+v", ns)
	}
	if s := *ns[0].Summary; s != st.TreeSummary(top.ID) || s.Leaves != 2 || s.Done != 2 || s.Runs != 2 || s.Started.IsZero() || !s.DoneAt.Equal(ns[0].At) {
		t.Fatalf("its summary: %+v", s)
	}
	var seen StateFold
	watchState(t, e.as(dee), WatchParams{}).fold(&seen, st.Seq)
	if a := seen.Aff.Tasks[top.ID]; a == nil || a.TreeDone == nil || a.TreeDone.Leaves != 2 || a.TreeDone.Runs != 2 || !a.TreeDone.DoneAt.Equal(ns[0].At) {
		t.Fatalf("a reader's state stream carries how the tree went: %+v", a)
	}
	if a := seen.Aff.Tasks[mid.ID]; a != nil && a.TreeDone != nil {
		t.Fatalf("not for a parent under the root: %+v", a)
	}
	done := taken(NotifyTaskDone)
	var ids []string
	for _, n := range done {
		ids = append(ids, n.Task)
	}
	slices.Sort(ids)
	want := []string{top.ID, mid.ID, leaf1.ID, leaf2.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("each task still says it is done, once: %v", ids)
	}

	e.stop()
	e.start()
	for range 5 {
		e.c.poke()
		time.Sleep(50 * time.Millisecond)
	}
	if ns := taken(NotifyTaskTreeDone); len(ns) != 0 {
		t.Fatalf("a restart tells nothing again: %+v", ns)
	}
	if err := callAs(e.as(bob), MTaskStatus, "reopen", task.TaskStatus{ID: top.ID, Status: task.StatusTodo}, nil); err != nil {
		t.Fatal(err)
	}
	accept(top.ID, ann.User)
	if ns := taken(NotifyTaskTreeDone); len(ns) != 1 || ns[0].Task != top.ID {
		t.Fatalf("reopened and done again, it is told again: %+v", ns)
	}
}

func TestTheNotifyCommandHearsATreeDoneWhenItNamesIt(t *testing.T) {
	out := filepath.Join(t.TempDir(), "events")
	e := newEnv(t, tend.Config{NotifyCommand: []string{os.Args[0], "_notify", out}, NotifyEvents: []string{NotifyTaskTreeDone}})
	e.start()
	top := e.create(TaskCreate{Title: "top"})
	e.create(TaskCreate{Title: "only", Parent: top.ID, Agent: "quick"})
	e.must(MTaskStart, TaskRef{ID: top.ID}, nil)
	e.until("the root waits to be accepted", func(st *task.State) bool { return st.Situation(st.Tasks[top.ID]).Reason == task.WhyAccept })
	e.must(MTaskStatus, task.TaskStatus{ID: top.ID, Status: task.StatusDone}, nil)
	heard := func() []NotifyEvent {
		var evs []NotifyEvent
		b, _ := os.ReadFile(out)
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var ev NotifyEvent
			if json.Unmarshal([]byte(l), &ev) == nil {
				evs = append(evs, ev)
			}
		}
		return evs
	}
	for deadline := time.Now().Add(10 * time.Second); len(heard()) == 0 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
	}
	time.Sleep(300 * time.Millisecond)
	evs := heard()
	if len(evs) != 1 || evs[0].Event != NotifyTaskTreeDone || evs[0].Task != top.ID || evs[0].Summary == nil ||
		evs[0].Summary.Leaves != 1 || evs[0].Summary.Done != 1 || evs[0].Summary.Runs != 1 {
		t.Fatalf("a tree of one subtask, and only what the command names: %+v", evs)
	}
}
