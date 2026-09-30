package coord

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// standing is what an undo gives back: how the task stands, what it keeps of its tries, its place and its inbox item.
type standing struct {
	Status, Stage, Parent, Held string
	After                       []string
	Auto, Merged                bool
	StartSeq, StageSeq          int64
	Loops, Stages               int
	Sit                         task.Situation
	Inbox                       string
}

func (e *env) standing(id string) standing {
	e.t.Helper()
	for range 5 { // what flow() would do with it has been done
		e.c.poke()
		time.Sleep(50 * time.Millisecond)
	}
	st := e.c.State()
	x := st.Tasks[id]
	var in Inbox
	e.must(MInboxList, struct{}{}, &in)
	item := ""
	for _, it := range in.Items {
		if it.Task == id {
			item = it.Reason + " " + it.Run
		}
	}
	return standing{Status: x.Status, Stage: x.Stage, Parent: x.Parent, Held: x.Held, After: x.After, Auto: x.Auto, Merged: x.Merged,
		StartSeq: x.StartSeq, StageSeq: x.StageSeq, Loops: x.Loops, Stages: len(x.Stages), Sit: st.Situation(x), Inbox: item}
}

// write sends method as command id, which an undo names.
func (e *env) write(method, id string, params any) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := e.cli.CallCommand(ctx, method, id, params, nil); err != nil {
		e.t.Fatalf("%s: %v", method, err)
	}
}

// undo takes back command of task id.
func (e *env) undo(id, command string) (*task.Task, error) {
	var x task.Task
	err := e.call(MTaskUndo, TaskUndo{ID: id, Command: command}, &x)
	return &x, err
}

// undone changes id's status to status, undoes it, and checks the task stands as it did before.
func (e *env) undone(id, status string) {
	e.t.Helper()
	before := e.standing(id)
	cmd := "set-" + itoa(e.cmd.Add(1))
	e.write(MTaskStatus, cmd, task.TaskStatus{ID: id, Status: status})
	if _, err := e.undo(id, cmd); err != nil {
		e.t.Fatalf("undo %s: %v", status, err)
	}
	if after := e.standing(id); fmt.Sprint(after) != fmt.Sprint(before) {
		e.t.Fatalf("%s then undone:\nbefore %+v\nafter  %+v", status, before, after)
	}
	if _, err := e.undo(id, cmd); wire.Code(err) != wire.CodeConflict {
		e.t.Fatalf("an undo is taken once: %v", err)
	}
}

func TestAnUndoneStatusChangeLeavesTheTaskAsItStood(t *testing.T) {
	t.Run("ended leaf done", func(t *testing.T) {
		e := newEnv(t, tend.Config{})
		e.start()
		x := e.task("leaf", "quick")
		e.dispatch(Dispatch{Task: x.ID})
		e.until("its run ended", func(s *task.State) bool { return s.Situation(s.Tasks[x.ID]).Reason == task.WhyEnded })
		e.undone(x.ID, task.StatusDone)
	})
	t.Run("started parent done", func(t *testing.T) {
		e := newEnv(t, tend.Config{})
		e.start()
		parent := e.create(TaskCreate{Title: "parent", Status: task.StatusBacklog})
		e.create(TaskCreate{Title: "kid", Parent: parent.ID, Agent: "quick", Status: task.StatusBacklog})
		e.must(MTaskStart, TaskRef{ID: parent.ID}, nil)
		e.until("its child done, it waits to be accepted", func(s *task.State) bool { return s.Situation(s.Tasks[parent.ID]).Reason == task.WhyAccept })
		e.undone(parent.ID, task.StatusDone)
	})
	t.Run("workflow at its gate", func(t *testing.T) {
		e := newEnv(t, tend.Config{})
		e.start()
		x := e.gated()
		e.undone(x.ID, task.StatusBacklog)
		e.must(MTaskGate, TaskGate{ID: x.ID, Pass: true}, nil)
		e.until("passed, it is done", status(x.ID, task.StatusDone))
		e.undone(x.ID, task.StatusTodo)
		e.undone(x.ID, task.StatusBacklog)
	})
	t.Run("merged child", func(t *testing.T) {
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
		e.undone(kid.ID, task.StatusTodo)
		e.undone(kid.ID, task.StatusBacklog)
		e.undone(parent.ID, task.StatusDone)
	})
	t.Run("merge still queued", func(t *testing.T) {
		dir := checkout(t)
		e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{writing("own", "own.txt")}, Machines: map[string]tend.MachineConfig{Local: {Slots: 1}}})
		e.start()
		e.must(MProjectCreate, ProjectCreate{ID: "p1", Name: "One"}, nil)
		e.must(MProjectEdit, task.ProjectEdit{ID: "p1", Repos: &[]task.Repo{{Name: "app", Base: "main", Dirs: map[string]string{Local: dir},
			Worktrees: true}}}, nil)
		parent := e.create(TaskCreate{Title: "export", Project: "p1", Status: task.StatusBacklog})
		var kid task.Task
		e.must(MTaskCreate, TaskCreate{Title: "tsv", Project: "p1", Parent: parent.ID, Agent: "own"}, &kid)
		e.dispatch(Dispatch{Task: kid.ID})
		e.until("its run ended", func(s *task.State) bool { return s.Situation(s.Tasks[kid.ID]).Reason == task.WhyEnded })
		asks := e.task("asks", "slow")
		e.dispatch(Dispatch{Task: asks.ID})
		e.until("the one slot is taken", func(s *task.State) bool { return s.Situation(s.Tasks[asks.ID]).Kind == task.SitRunning })
		e.undone(kid.ID, task.StatusDone)

		e.write(MTaskStatus, "done-again", task.TaskStatus{ID: kid.ID, Status: task.StatusDone})
		e.must(MRunStop, task.RunRef{ID: e.c.State().OpenRun(asks.ID).ID}, nil)
		e.until("merged, it is done", status(kid.ID, task.StatusDone))
		if _, err := e.undo(kid.ID, "done-again"); wire.Code(err) != wire.CodeConflict {
			t.Fatalf("once the merge went, the done stays: %v", err)
		}
	})
}

func TestAnUndoneMoveLeavesTheTaskWhereItStood(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	first := e.create(TaskCreate{Title: "first", Status: task.StatusBacklog})
	var x task.Task
	e.must(MTaskCreate, TaskCreate{Title: "no dir", Agent: "quick", Status: task.StatusBacklog}, &x)
	e.must(MTaskStart, TaskRef{ID: x.ID}, nil)
	e.until("its dispatch fails, it is held", func(s *task.State) bool { return s.Tasks[x.ID].Held != "" })
	before := e.standing(x.ID)
	after := []string{first.ID}
	e.write(MTaskMove, "move", task.TaskMove{ID: x.ID, After: &after})
	if st := e.standing(x.ID); st.Held != "" || st.Sit.Reason != task.WhyAfter {
		t.Fatalf("moved after a task not done, it waits for it: %+v", st)
	}
	e.stop()
	e.start() // what a command replaced is folded again from the journal
	y, err := e.undo(x.ID, "move")
	if err != nil || y.Held != before.Held || len(y.After) != 0 {
		t.Fatalf("undone, it is held again where it stood: %v %+v", err, y)
	}
	if st := e.standing(x.ID); fmt.Sprint(st) != fmt.Sprint(before) {
		t.Fatalf("before %+v\nafter  %+v", before, st)
	}
	if _, err := e.undo(first.ID, "move"); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("a command undoes its own task only: %v", err)
	}
}

// gated is a started workflow task waiting at its human gate.
func (e *env) gated() *task.Task {
	e.t.Helper()
	e.must(MProjectCreate, ProjectCreate{ID: "p1", Name: "One"}, nil)
	gate := "---\nname: gate\nstages:\n  - {name: build, role: implement}\n  - {name: accept, gate: human}\n---\n"
	e.must(MProjectEdit, task.ProjectEdit{ID: "p1", Workflows: &map[string]string{"gate": gate},
		Defaults: &task.Defaults{Roles: map[string]string{"implement": "quick"}}}, nil)
	var x task.Task
	e.must(MTaskCreate, TaskCreate{Title: "build", Dir: e.t.TempDir(), Project: "p1", Workflow: "gate"}, &x)
	e.must(MTaskStart, TaskRef{ID: x.ID}, nil)
	e.until("built, it waits at its gate", func(s *task.State) bool { return s.Situation(s.Tasks[x.ID]).Reason == task.WhyAccept })
	return &x
}
