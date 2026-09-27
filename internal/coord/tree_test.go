package coord

import (
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func (e *env) create(p TaskCreate) *task.Task {
	e.t.Helper()
	if p.Dir == "" && p.Agent != "" {
		p.Dir = e.t.TempDir()
	}
	var t task.Task
	e.must(MTaskCreate, p, &t)
	return &t
}

// until waits for the state to satisfy ok.
func (e *env) until(what string, ok func(*task.State) bool) *task.State {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		st := e.c.State()
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			sits := map[string]task.Situation{}
			for id, x := range st.Tasks {
				sits[id] = st.Situation(x)
			}
			e.t.Fatalf("%s: %v", what, sits)
		}
		e.c.poke()
		time.Sleep(50 * time.Millisecond)
	}
}

func status(id, want string) func(*task.State) bool {
	return func(st *task.State) bool { return st.Tasks[id] != nil && st.Tasks[id].Status == want }
}

func TestAStartedTreeRunsItselfByItsDependencies(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	root := e.create(TaskCreate{Title: "feature", Status: task.StatusBacklog})
	api := e.create(TaskCreate{Title: "api", Parent: root.ID, Status: task.StatusBacklog})
	model := e.create(TaskCreate{Title: "model", Parent: api.ID, Agent: "quick", Status: task.StatusBacklog})
	handler := e.create(TaskCreate{Title: "handler", Parent: api.ID, After: []string{model.ID}, Agent: "quick", Status: task.StatusBacklog})
	ui := e.create(TaskCreate{Title: "ui", Parent: root.ID, After: []string{api.ID}, Agent: "quick", Status: task.StatusBacklog})
	time.Sleep(200 * time.Millisecond)
	if st := e.c.State(); len(st.Runs) != 0 {
		t.Fatal("the backlog waits")
	}
	var started task.Task
	e.must(MTaskStart, TaskRef{ID: root.ID}, &started)
	st := e.until("the subtasks of api are done", func(st *task.State) bool {
		return st.Tasks[handler.ID].Status == task.StatusDone && st.Situation(st.Tasks[api.ID]).Reason == task.WhyAccept
	})
	if st.Situation(st.Tasks[ui.ID]).Reason != task.WhyAfter {
		t.Fatalf("ui waits for api to be accepted: %+v", st.Situation(st.Tasks[ui.ID]))
	}
	e.must(MTaskStatus, task.TaskStatus{ID: api.ID, Status: task.StatusDone}, nil)
	st = e.until("ui is done", status(ui.ID, task.StatusDone))
	m, h, u := st.Latest(model.ID), st.Latest(handler.ID), st.Latest(ui.ID)
	if m.Seq >= h.Seq || h.Seq >= u.Seq || h.QueuedAt.Before(*m.EndedAt) {
		t.Fatalf("each leaf goes after what it comes after: %d %d %d", m.Seq, h.Seq, u.Seq)
	}
	if st.Latest(root.ID) != nil || st.Latest(api.ID) != nil || st.Situation(st.Tasks[root.ID]).Reason != task.WhyAccept {
		t.Fatalf("parents never run; the root waits to be accepted: %+v", st.Situation(st.Tasks[root.ID]))
	}
	if h.Dispatcher != "" && h.Dispatcher != Owner.User {
		t.Fatalf("the coordinator dispatches as the task's owner: %q", h.Dispatcher)
	}
}

func TestATreeKeepsItsShape(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	a := e.create(TaskCreate{Title: "a"})
	b := e.create(TaskCreate{Title: "b", Parent: a.ID})
	c := e.create(TaskCreate{Title: "c", Parent: b.ID})
	x := e.create(TaskCreate{Title: "x"})
	for name, err := range map[string]error{
		"a fourth level":              e.call(MTaskCreate, TaskCreate{Title: "d", Parent: c.ID}, nil),
		"under its own subtask":       e.call(MTaskMove, task.TaskMove{ID: a.ID, Parent: &c.ID}, nil),
		"after its own ancestor":      e.call(MTaskMove, task.TaskMove{ID: c.ID, After: &[]string{a.ID}}, nil),
		"a subtree three levels down": e.call(MTaskMove, task.TaskMove{ID: a.ID, Parent: &x.ID}, nil),
		"a cycle": func() error {
			e.must(MTaskMove, task.TaskMove{ID: x.ID, After: &[]string{a.ID}}, nil)
			return e.call(MTaskMove, task.TaskMove{ID: a.ID, After: &[]string{x.ID}}, nil)
		}(),
	} {
		if wire.Code(err) != wire.CodeBadRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := e.call(MRunDispatch, Dispatch{Task: b.ID, Agent: "quick"}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("a task with open subtasks is not dispatched: %v", err)
	}
	var moved task.Task
	e.must(MTaskMove, task.TaskMove{ID: c.ID, Parent: ptr(""), After: &[]string{x.ID}}, &moved)
	if moved.Parent != "" || len(moved.After) != 1 {
		t.Fatalf("%+v", moved)
	}
}

func TestCancelingATaskCancelsItsSubtree(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	root := e.create(TaskCreate{Title: "root"})
	kid := e.create(TaskCreate{Title: "kid", Parent: root.ID, Agent: "slow"})
	r := e.dispatch(Dispatch{Task: kid.ID, Agent: "slow"})
	e.wait(r.ID, state(task.Running))
	e.must(MTaskStatus, task.TaskStatus{ID: root.ID, Status: task.StatusCanceled}, nil)
	st := e.until("the subtree is canceled", status(kid.ID, task.StatusCanceled))
	if st.Tasks[root.ID].Status != task.StatusCanceled || st.Runs[r.ID].Want != "stop" {
		t.Fatalf("%+v %+v", st.Tasks[root.ID], st.Runs[r.ID])
	}
	e.wait(r.ID, ended)
}

func TestAStartedTaskThatCannotGoIsHeldUntilItIsFixed(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	var x task.Task
	e.must(MTaskCreate, TaskCreate{Title: "no dir", Agent: "quick"}, &x)
	e.must(MTaskStart, TaskRef{ID: x.ID}, nil)
	st := e.until("held", func(st *task.State) bool { return st.Tasks[x.ID].Held != "" })
	if sit := st.Situation(st.Tasks[x.ID]); sit.Reason != task.WhyHeld || st.Tasks[x.ID].Held != "bad_request: dir" {
		t.Fatalf("%+v %q", sit, st.Tasks[x.ID].Held)
	}
	dir := t.TempDir()
	e.must(MTaskEdit, task.TaskEdit{ID: x.ID, Dir: &dir}, nil)
	e.until("done once it has a directory", status(x.ID, task.StatusDone))
}

func TestAProjectTellsItsTasksWhereAndWithWhatToRun(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	dir := t.TempDir()
	e.must(MProjectCreate, ProjectCreate{ID: "app", Name: "App"}, nil)
	e.must(MProjectEdit, task.ProjectEdit{ID: "app", Context: ptr("Go service; run mise run gate before you finish."),
		Repos:    &[]task.Repo{{Name: "app", Remote: "git@example:app.git", Base: "main", Dirs: map[string]string{Local: dir}}},
		Defaults: &task.Defaults{Roles: map[string]string{"implement": "quick"}, Machine: Local},
		Hooks:    &map[string][]string{"check": {"mise", "run", "gate"}}}, nil)
	x := e.create(TaskCreate{Title: "endpoint", Brief: "add GET /health", Project: "app"})
	r := e.dispatch(Dispatch{Task: x.ID})
	if r.Dir != dir || r.Agent != "quick" || r.Machine != Local || !strings.Contains(r.Brief, "mise run gate") || !strings.HasSuffix(r.Brief, "add GET /health") {
		t.Fatalf("%+v", r)
	}
	e.wait(r.ID, ended)
	for name, edit := range map[string]task.ProjectEdit{
		"a repo without a name": {ID: "app", Repos: &[]task.Repo{{Dirs: map[string]string{Local: dir}}}},
		"an unknown hook":       {ID: "app", Hooks: &map[string][]string{"deploy": {"x"}}},
		"an unknown agent":      {ID: "app", Defaults: &task.Defaults{Agent: "nobody"}},
	} {
		if err := e.call(MProjectEdit, edit, nil); err == nil {
			t.Errorf("%s is refused", name)
		}
	}
}
