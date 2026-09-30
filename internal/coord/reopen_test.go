package coord

import (
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// reopened reopens id, checks it waits to be dispatched over the next commits, starts it again and waits for it to be
// done again.
func (e *env) reopened(id string) (reopened, again *task.State) {
	e.t.Helper()
	e.must(MTaskStatus, task.TaskStatus{ID: id, Status: task.StatusTodo}, nil)
	for range 5 {
		e.c.poke()
		time.Sleep(50 * time.Millisecond)
	}
	reopened = e.c.State()
	if x := reopened.Tasks[id]; x.Status != task.StatusTodo || x.Auto || reopened.Situation(x).Reason != task.WhyDispatch {
		e.t.Fatalf("a reopened task waits to be dispatched: %s %+v", x.Status, reopened.Situation(x))
	}
	e.must(MTaskStart, TaskRef{ID: id}, nil)
	return reopened, e.until("started again, it is done again", status(id, task.StatusDone))
}

func TestAReopenedTaskTheCoordinatorFinishedStaysOpen(t *testing.T) {
	t.Run("started", func(t *testing.T) {
		e := newEnv(t, tend.Config{})
		e.start()
		x := e.create(TaskCreate{Title: "leaf", Agent: "quick", Status: task.StatusBacklog})
		e.must(MTaskStart, TaskRef{ID: x.ID}, nil)
		e.until("its run finishes it", status(x.ID, task.StatusDone))
		if _, st := e.reopened(x.ID); len(stageRuns(st, x.ID)) != 2 {
			t.Fatalf("it ran again: %+v", stageRuns(st, x.ID))
		}
	})
	t.Run("workflow", func(t *testing.T) {
		e := newEnv(t, tend.Config{})
		e.start()
		e.must(MProjectCreate, ProjectCreate{ID: "p1", Name: "One"}, nil)
		duo := "---\nname: duo\nstages:\n  - {name: build, role: implement}\n  - {name: ship, role: implement}\n---\n"
		e.must(MProjectEdit, task.ProjectEdit{ID: "p1", Workflows: &map[string]string{"duo": duo},
			Defaults: &task.Defaults{Roles: map[string]string{"implement": "quick"}}}, nil)
		var x task.Task
		e.must(MTaskCreate, TaskCreate{Title: "build", Dir: t.TempDir(), Project: "p1", Workflow: "duo"}, &x)
		e.must(MTaskStart, TaskRef{ID: x.ID}, nil)
		e.until("its last stage finishes it", status(x.ID, task.StatusDone))
		st, again := e.reopened(x.ID)
		if y := st.Tasks[x.ID]; y.Stage != "build" || y.Loops != 0 || !y.Stages[len(y.Stages)-1].Back {
			t.Fatalf("reopened, it goes back to the stage before its last, as a rework would: %+v", y)
		}
		var stages []string
		for _, r := range stageRuns(again, x.ID) {
			stages = append(stages, r.Stage)
		}
		if !slices.Equal(stages, []string{"build", "ship", "build", "ship"}) {
			t.Fatalf("stages %v", stages)
		}
	})
	t.Run("merged", func(t *testing.T) {
		dir := checkout(t)
		e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{writing("own", "own.txt")}})
		e.start()
		e.must(MProjectCreate, ProjectCreate{ID: "p1", Name: "One"}, nil)
		e.must(MProjectEdit, task.ProjectEdit{ID: "p1", Repos: &[]task.Repo{{Name: "app", Base: "main", Dirs: map[string]string{Local: dir},
			Worktrees: true}}}, nil)
		parent := e.create(TaskCreate{Title: "export", Project: "p1", Status: task.StatusBacklog})
		var kid task.Task // no directory: it works in the project's repository
		e.must(MTaskCreate, TaskCreate{Title: "tsv", Project: "p1", Parent: parent.ID, Agent: "own", Status: task.StatusBacklog}, &kid)
		e.must(MTaskStart, TaskRef{ID: parent.ID}, nil)
		e.until("merged into its parent, it is done", status(kid.ID, task.StatusDone))
		st, again := e.reopened(kid.ID)
		if st.Tasks[kid.ID].Merged {
			t.Fatal("reopened, its branch goes into its parent's again")
		}
		merges := 0
		for _, r := range stageRuns(again, kid.ID) {
			if r.Stage == task.StageMerge {
				merges++
			}
		}
		if merges != 2 || !again.Tasks[kid.ID].Merged {
			t.Fatalf("done again, it merged again: %d", merges)
		}
	})
}
