package coord

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// quiet: nothing new is queued or started for task id through several passes.
func (e *env) quiet(ids ...string) {
	e.t.Helper()
	before := e.c.State()
	for range 6 {
		e.c.poke()
		time.Sleep(50 * time.Millisecond)
	}
	st := e.c.State()
	for _, id := range ids {
		if n, m := len(stageRuns(before, id)), len(stageRuns(st, id)); n != m {
			e.t.Fatalf("%s got a run under a paused tree: %d then %d", id, n, m)
		}
		for _, r := range stageRuns(st, id) {
			if r.State == task.Queued {
				continue
			}
			if was := before.Runs[r.ID]; was != nil && was.State == task.Queued {
				e.t.Fatalf("a queued run started under a paused tree: %+v", r)
			}
		}
	}
}

func paused(st *task.State, id string) bool {
	sit := st.Situation(st.Tasks[id])
	return sit.Kind == task.SitWaiting && sit.Reason == task.WhyPaused
}

// A paused tree lets what runs under it finish and dispatches nothing new: no ready task, no task whose dependency is
// done, no run dispatched by hand. Resumed, it goes on where it stood.
func TestAPausedTreeFinishesItsRunsAndDispatchesNothingNew(t *testing.T) {
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{long}})
	e.start()
	root := e.create(TaskCreate{Title: "feature", Kind: task.KindRequirement, Status: task.StatusBacklog})
	first := e.create(TaskCreate{Title: "first", Parent: root.ID, Agent: "long", Status: task.StatusBacklog})
	second := e.create(TaskCreate{Title: "second", Parent: root.ID, After: []string{first.ID}, Agent: "quick", Status: task.StatusBacklog})
	other := e.create(TaskCreate{Title: "elsewhere", Agent: "quick", Status: task.StatusBacklog})
	e.must(MTaskStart, TaskRef{ID: root.ID}, nil)
	st := e.until("first runs", func(st *task.State) bool {
		r := st.Latest(first.ID)
		return r != nil && r.State == task.Running
	})
	going := st.Latest(first.ID)

	var x task.Task
	e.must(MTaskPause, task.TaskPause{ID: root.ID, On: true}, &x)
	if x.ID != root.ID || x.Paused == nil || x.Paused.By != Owner.User || x.Paused.At.IsZero() {
		t.Fatalf("the answer is the paused task: %+v", x.Paused)
	}
	if p := e.c.State().Tasks[root.ID].Paused; p == nil || p.By != Owner.User {
		t.Fatalf("the state keeps it: %+v", p)
	}
	byHand := e.create(TaskCreate{Title: "by hand", Parent: root.ID, Agent: "quick"})
	queued := e.dispatch(Dispatch{Task: byHand.ID})
	var pv Preview
	e.must(MRunPreview, Dispatch{Task: e.create(TaskCreate{Title: "more", Parent: root.ID, Agent: "quick"}).ID}, &pv)
	if !hasWhy(pv.Notes, WhyPaused) || hasWhy(pv.Blockers, WhyPaused) {
		t.Fatalf("a preview says the tree is paused: %+v", pv)
	}
	if end := e.wait(going.ID, ended); end.State != task.Exited {
		t.Fatalf("what ran finishes: %+v", end)
	}
	st = e.until("first is done", status(first.ID, task.StatusDone))
	e.quiet(second.ID, byHand.ID)
	st = e.c.State()
	for _, id := range []string{root.ID, second.ID, byHand.ID} {
		if !paused(st, id) {
			t.Fatalf("%s says the tree is paused: %+v", st.Tasks[id].Title, st.Situation(st.Tasks[id]))
		}
	}
	if sit := st.Situation(st.Tasks[byHand.ID]); sit.Run != queued.ID || st.Runs[queued.ID].State != task.Queued {
		t.Fatalf("the run dispatched by hand stays queued: %+v %+v", sit, st.Runs[queued.ID])
	}
	if st.Situation(st.Tasks[second.ID]).Run != "" || st.Latest(second.ID) != nil {
		t.Fatal("the task whose dependency is done is not dispatched")
	}
	if in := e.c.inbox(Owner); len(in.Items) != 1 || in.Items[0].Task != root.ID || in.Items[0].Reason != task.WhyPaused {
		t.Fatalf("the paused root waits on its owner, once: %+v", in.Items)
	}
	e.must(MTaskStart, TaskRef{ID: other.ID}, nil)
	e.until("a task outside the tree goes on", status(other.ID, task.StatusDone))

	var on task.Task
	e.must(MTaskPause, task.TaskPause{ID: root.ID}, &on)
	if on.Paused != nil || e.c.State().Tasks[root.ID].Paused != nil {
		t.Fatalf("resumed: %+v", on.Paused)
	}
	e.until("resumed, the tree goes on", func(st *task.State) bool {
		return st.Tasks[second.ID].Status == task.StatusDone && st.Runs[queued.ID].State == task.Exited
	})
}

// A paused workflow task finishes its stage's run and does not move to the next stage, which would start one.
func TestAPausedWorkflowStaysAtItsStage(t *testing.T) {
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{long}})
	e.start()
	e.must(MProjectCreate, ProjectCreate{ID: "p1", Name: "One"}, nil)
	two := "---\nname: two\nstages:\n  - {name: build, role: implement}\n  - {name: review, role: review}\n---\n"
	e.must(MProjectEdit, task.ProjectEdit{ID: "p1", Workflows: &map[string]string{"two": two},
		Defaults: &task.Defaults{Roles: map[string]string{"implement": "long", "review": "quick"}}}, nil)
	root := e.create(TaskCreate{Title: "need", Project: "p1", Kind: task.KindRequirement})
	var x task.Task
	e.must(MTaskCreate, TaskCreate{Title: "staged", Dir: t.TempDir(), Project: "p1", Workflow: "two", Parent: root.ID}, &x)
	e.must(MTaskStart, TaskRef{ID: root.ID}, nil)
	st := e.until("build runs", func(st *task.State) bool {
		r := st.Latest(x.ID)
		return r != nil && r.State == task.Running
	})
	build := st.Latest(x.ID)
	e.must(MTaskPause, task.TaskPause{ID: root.ID, On: true}, nil)
	e.wait(build.ID, ended)
	e.quiet(x.ID)
	st = e.c.State()
	if y := st.Tasks[x.ID]; y.Stage != "build" || len(stageRuns(st, x.ID)) != 1 || !paused(st, x.ID) {
		t.Fatalf("it stays at build: %s %+v", y.Stage, st.Situation(y))
	}
	e.must(MTaskPause, task.TaskPause{ID: root.ID}, nil)
	st = e.until("resumed, it reviews", func(st *task.State) bool {
		runs := stageRuns(st, x.ID)
		return len(runs) == 2 && runs[1].Stage == "review"
	})
}

// Pausing is kept in the journal: a coordinator that starts again dispatches nothing under the tree either.
func TestAPauseOutlivesTheCoordinator(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	root := e.create(TaskCreate{Title: "root", Status: task.StatusBacklog})
	kid := e.create(TaskCreate{Title: "kid", Parent: root.ID, Agent: "quick", Status: task.StatusBacklog})
	e.must(MTaskPause, task.TaskPause{ID: root.ID, On: true}, nil)
	e.must(MTaskStart, TaskRef{ID: root.ID}, nil)
	e.stop()
	e.start()
	if e.c.State().Tasks[root.ID].Paused == nil {
		t.Fatal("the pause is gone after a restart")
	}
	e.quiet(kid.ID)
	if st := e.c.State(); st.Latest(kid.ID) != nil || !paused(st, kid.ID) {
		t.Fatalf("nothing is dispatched: %+v", st.Situation(st.Tasks[kid.ID]))
	}
}

// Pausing a paused task, or resuming one that is not, writes nothing; a finished task is neither paused nor resumed,
// nor is a task that is no tree (no subtasks, no requirement); a pause holds only its own subtree, and a pause inside
// it stays when the root resumes.
func TestPausingTwiceWritesOnce(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	root := e.create(TaskCreate{Title: "root", Status: task.StatusBacklog})
	kid := e.create(TaskCreate{Title: "kid", Parent: root.ID, Status: task.StatusBacklog})
	e.create(TaskCreate{Title: "grandchild", Parent: kid.ID, Status: task.StatusBacklog})
	sibling := e.create(TaskCreate{Title: "sibling", Kind: task.KindRequirement, Status: task.StatusBacklog})
	leaf := e.create(TaskCreate{Title: "leaf", Status: task.StatusBacklog})
	if err := e.call(MTaskPause, task.TaskPause{ID: leaf.ID, On: true}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("a task with no subtasks: %v", err)
	}
	e.must(MTaskPause, task.TaskPause{ID: root.ID, On: true}, nil)
	seq := e.c.State().Seq
	e.must(MTaskPause, task.TaskPause{ID: root.ID, On: true}, nil)
	e.must(MTaskPause, task.TaskPause{ID: sibling.ID}, nil)
	if got := e.c.State().Seq; got != seq {
		t.Fatalf("nothing to change: seq %d then %d", seq, got)
	}
	e.must(MTaskPause, task.TaskPause{ID: kid.ID, On: true}, nil)
	e.must(MTaskPause, task.TaskPause{ID: root.ID}, nil)
	st := e.c.State()
	if st.PausedBy(kid.ID) == nil || st.PausedBy(kid.ID).ID != kid.ID || st.PausedBy(root.ID) != nil || st.PausedBy(sibling.ID) != nil {
		t.Fatalf("each pause holds its own subtree: %v %v", st.PausedBy(kid.ID), st.PausedBy(root.ID))
	}
	e.must(MTaskStatus, task.TaskStatus{ID: sibling.ID, Status: task.StatusDone}, nil)
	if err := e.call(MTaskPause, task.TaskPause{ID: sibling.ID, On: true}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("a finished task: %v", err)
	}
	if err := e.call(MTaskPause, task.TaskPause{ID: "t_nowhere", On: true}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("no such task: %v", err)
	}
}

// Who may write a task pauses its tree; a reader may not, and who cannot read it is not told it exists. Who reads the
// task sees it paused.
func TestOnlyAWriterPausesATree(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	x := e.taskAs(bob, "x", "p1", "")
	if err := callAs(e.as(bob), MTaskCreate, "kid", TaskCreate{Title: "kid", Parent: x.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(cy), MTaskPause, "p0", task.TaskPause{ID: x.ID, On: true}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("a task he does not see: %v", err)
	}
	if err := callAs(e.as(dee), MTaskPause, "p1", task.TaskPause{ID: x.ID, On: true}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a reader: %v", err)
	}
	if err := callAs(e.as(bob), MTaskPause, "p2", task.TaskPause{ID: x.ID, On: true}, nil); err != nil {
		t.Fatal(err)
	}
	var st task.State
	if err := callAs(e.as(dee), MStateGet, "", StateParams{}, &st); err != nil || st.Tasks[x.ID] == nil || st.Tasks[x.ID].Paused == nil ||
		st.Tasks[x.ID].Paused.By != bob.User {
		t.Fatalf("who reads the task sees it paused: %v", err)
	}
}

// A paused tree is one line of the inbox, its root's: the tasks under it wait for nothing but the resume, and nobody
// is told about them.
func TestAPausedTreeIsOneLineOfTheInbox(t *testing.T) {
	heard := filepath.Join(t.TempDir(), "events")
	e := newEnv(t, tend.Config{NotifyCommand: []string{os.Args[0], "_notify", heard}, NotifyEvents: []string{NotifyTaskWaiting}})
	e.start()
	root := e.create(TaskCreate{Title: "need", Kind: task.KindRequirement, Status: task.StatusBacklog})
	var kids []string
	for _, title := range []string{"a", "b", "c"} {
		kids = append(kids, e.create(TaskCreate{Title: title, Parent: root.ID, Agent: "quick", Status: task.StatusBacklog}).ID)
	}
	e.must(MTaskPause, task.TaskPause{ID: root.ID, On: true}, nil)
	e.must(MTaskStart, TaskRef{ID: root.ID}, nil)
	e.quiet(kids...)
	st := e.c.State()
	for _, id := range append([]string{root.ID}, kids...) {
		if !paused(st, id) {
			t.Fatalf("%s: %+v", st.Tasks[id].Title, st.Situation(st.Tasks[id]))
		}
	}
	in := e.c.inbox(Owner)
	if len(in.Items) != 1 || in.Items[0].Task != root.ID || in.Items[0].Reason != task.WhyPaused || len(in.Items[0].Pending) != 0 {
		t.Fatalf("only the root, waiting to be resumed: %+v", in.Items)
	}
	if b, _ := os.ReadFile(heard); len(b) != 0 {
		t.Fatalf("pausing tells nobody: %s", b)
	}
	e.must(MTaskPause, task.TaskPause{ID: root.ID}, nil)
	e.until("resumed, the tree runs", func(st *task.State) bool { return st.Tasks[kids[2]].Status == task.StatusDone })
	if in := e.c.inbox(Owner); slices.ContainsFunc(in.Items, func(x InboxItem) bool { return x.Reason == task.WhyPaused }) {
		t.Fatalf("resumed, it waits no more: %+v", in.Items)
	}
}
