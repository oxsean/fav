package coord

import (
	"context"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// personalProject is bob's project side, with no other member, and a task of his in it.
func (e *env) personalProject() *task.Task {
	e.t.Helper()
	if err := callAs(e.as(bob), MProjectCreate, "side", ProjectCreate{ID: "side", Name: "Side"}, nil); err != nil {
		e.t.Fatal(err)
	}
	return e.taskAs(bob, "in-side", "side", "")
}

// foldReset folds w's pushes into f up to seq, and says whether a reset came on the way.
func (w *watched) foldReset(f *StateFold, seq int64) (reset bool) {
	w.t.Helper()
	for f.St == nil || f.St.Seq < seq || f.next != nil {
		p := w.next()
		reset = reset || p.Method == PushReset
		if _, err := f.Apply(p); err != nil {
			w.t.Fatal(err)
		}
	}
	return reset
}

func (e *env) disable(u Principal) {
	e.usersM.Lock()
	defer e.usersM.Unlock()
	x := e.users[u.User]
	x.Disabled = true
	e.users[u.User] = x
}

func TestAPrivateTaskIsHiddenFromAdmins(t *testing.T) {
	e := stage(t, true)
	var roots StateFold
	w := watchState(t, e.as(root), WatchParams{})
	mine := e.taskAs(bob, "private", "", "")
	if err := callAs(e.as(bob), MTaskEdit, "e1", task.TaskEdit{ID: mine.ID, Title: ptr("still mine")}, nil); err != nil {
		t.Fatal(err)
	}
	tid, rid := e.scene("running", "", bob.User, 1)
	team, _ := e.scene("running", "p1", bob.User, 2)
	w.fold(&roots, e.c.State().Seq)
	for _, id := range []string{mine.ID, tid} {
		if roots.St.Tasks[id] != nil {
			t.Errorf("the admin's watch holds %s", id)
		}
		if err := callAs(e.as(root), MTaskGet, "", task.RunRef{ID: id}, nil); wire.Code(err) != wire.CodeNotFound {
			t.Errorf("the admin gets %s: %v", id, err)
		}
		if err := callAs(e.as(bob), MTaskGet, "", task.RunRef{ID: id}, nil); err != nil {
			t.Errorf("bob gets his own %s: %v", id, err)
		}
	}
	if roots.St.Runs[rid] != nil || roots.St.Tasks[team] == nil {
		t.Fatalf("the admin sees the team's task, not the private run: %v %v", roots.St.Runs[rid] != nil, roots.St.Tasks[team] != nil)
	}
	var st task.State
	if err := callAs(e.as(root), MStateGet, "", StateParams{}, &st); err != nil || st.Tasks[mine.ID] != nil || st.Tasks[tid] != nil {
		t.Fatalf("state.get: %v", err)
	}

	e.disable(ann) // far retires: its open runs wait for an admin, those they may read
	var in Inbox
	if err := callAs(e.as(root), MInboxList, "", nil, &in); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range in.Items {
		got = append(got, it.Task)
	}
	if !slices.Contains(got, team) || slices.Contains(got, tid) {
		t.Fatalf("the admin's inbox: %v", got)
	}
}

func TestAPersonalProjectIsHiddenFromAdminsUntilItsFirstMember(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	var roots StateFold
	w := watchState(t, e.as(root), WatchParams{})
	tk := e.personalProject()
	w.fold(&roots, e.c.State().Seq)
	if roots.St.Projects["side"] != nil || roots.St.Tasks[tk.ID] != nil || roots.St.Projects["p1"] == nil {
		t.Fatalf("the admin sees p1, not bob's own side: %v", roots.St.Projects)
	}
	for _, c := range []struct {
		method string
		params any
	}{
		{MProjectEdit, task.ProjectEdit{ID: "side", Name: ptr("Mine")}},
		{MProjectMember, task.MemberSet{Project: "side", User: cy.User, Role: task.RoleReader}},
		{MTaskCreate, TaskCreate{Title: "x", Project: "side"}},
		{MTaskGet, task.RunRef{ID: tk.ID}},
	} {
		id := "c-" + c.method
		if c.method == MTaskGet {
			id = ""
		}
		if err := callAs(e.as(root), c.method, id, c.params, nil); wire.Code(err) != wire.CodeNotFound {
			t.Errorf("%s by an admin: %v", c.method, err)
		}
	}
	if err := callAs(e.as(bob), MProjectEdit, "b-edit", task.ProjectEdit{ID: "side", Name: ptr("Mine")}, nil); err != nil {
		t.Fatalf("its owner manages it: %v", err)
	}
	if err := callAs(e.as(System), MTaskCreate, "sys", TaskCreate{Title: "from an issue", Dir: t.TempDir(), Project: "side"}, nil); err != nil {
		t.Fatalf("tend-server's own work, as a tracker's sync, reaches it: %v", err)
	}

	if err := callAs(e.as(bob), MProjectMember, "b-cy", task.MemberSet{Project: "side", User: cy.User, Role: task.RoleReader}, nil); err != nil {
		t.Fatal(err)
	}
	if !w.foldReset(&roots, e.c.State().Seq) || roots.St.Projects["side"] == nil || roots.St.Tasks[tk.ID] == nil {
		t.Fatal("with a member side is the team's: the admin's copy is reset and holds it")
	}
	if err := callAs(e.as(root), MProjectEdit, "r-edit", task.ProjectEdit{ID: "side", Name: ptr("Ours")}, nil); err != nil {
		t.Fatalf("and manages it: %v", err)
	}
	w.fold(&roots, e.c.State().Seq)

	if err := callAs(e.as(bob), MProjectMember, "b-cy-out", task.MemberSet{Project: "side", User: cy.User}, nil); err != nil {
		t.Fatal(err)
	}
	if !w.foldReset(&roots, e.c.State().Seq) || roots.St.Projects["side"] != nil || roots.St.Tasks[tk.ID] != nil {
		t.Fatal("its last member gone, side is bob's alone again")
	}
}

func TestOffboardingKeepsPrivateWorkAndStopsItsRuns(t *testing.T) {
	e := stage(t, true)
	side := e.personalProject()
	if err := callAs(e.as(bob), MProjectCreate, "duo", ProjectCreate{ID: "duo", Name: "Duo"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(bob), MProjectMember, "duo-cy", task.MemberSet{Project: "duo", User: cy.User, Role: task.RoleParticipant}, nil); err != nil {
		t.Fatal(err)
	}
	inDuo := e.taskAs(bob, "in-duo", "duo", "")
	inP1 := e.taskAs(bob, "in-p1", "p1", "")
	running, runningRun := e.scene("running", "", bob.User, 1)
	queued := task.Run{ID: "r-queued", Task: side.ID, Machine: "far", Agent: "quick", Profile: tend.AgentProfile{Name: "quick", Provider: agent.ProviderFake},
		Dir: side.Dir, Project: "side", Dispatcher: bob.User}
	e.c.mu.Lock()
	err := e.c.commit(journal.System, nil, journal.NewEvent(task.ERunQueued, queued))
	e.c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	if err := callAs(e.as(bob), MUserOffboardPreview, "", Offboard{User: bob.User}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("only an admin previews: %v", err)
	}
	var plan OffboardPlan
	if err := callAs(e.as(root), MUserOffboardPreview, "", Offboard{User: bob.User, To: dee.User}, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.User != bob.User || plan.To != dee.User || plan.Private != 2 || !slices.Equal(plan.Projects, []string{"duo"}) || !slices.Equal(plan.Left, []string{"p1"}) {
		t.Fatalf("bob's private tasks stay, duo goes, he leaves p1: %+v", plan)
	}
	heirs := map[string]string{}
	for _, x := range plan.Tasks {
		heirs[x.ID] = x.To
	}
	if len(heirs) != 2 || heirs[inDuo.ID] != dee.User || heirs[inP1.ID] != ann.User {
		t.Fatalf("duo's task goes with duo, p1's to its owner: %+v", plan.Tasks)
	}

	if err := callAs(e.as(root), MUserOffboard, "o-bob", Offboard{User: bob.User, To: dee.User}, nil); err != nil {
		t.Fatal(err)
	}
	st := e.c.State()
	if st.Tasks[running].Owner != bob.User || st.Tasks[side.ID].Owner != bob.User || st.Projects["side"].Owner != bob.User {
		t.Fatalf("his private work is not handed over: %s %s %s", st.Tasks[running].Owner, st.Tasks[side.ID].Owner, st.Projects["side"].Owner)
	}
	if st.Projects["duo"].Owner != dee.User || st.Tasks[inDuo.ID].Owner != dee.User || st.Tasks[inP1.ID].Owner != ann.User {
		t.Fatalf("the rest goes as the preview said: %+v %+v %+v", st.Projects["duo"], st.Tasks[inDuo.ID], st.Tasks[inP1.ID])
	}
	if r := st.Runs[runningRun]; r.Want != "stop" {
		t.Fatalf("his private run is asked to stop: %+v", r)
	}
	if r := st.Runs[queued.ID]; r.State != task.Canceled {
		t.Fatalf("his queued private run is canceled: %+v", r)
	}

	var anns OffboardPlan
	if err := callAs(e.as(root), MUserOffboardPreview, "", Offboard{User: ann.User}, &anns); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(anns.Machines, []string{"far", "local", "solo"}) || anns.Private != 0 || !slices.Equal(anns.Projects, []string{"p1"}) {
		t.Fatalf("ann's machines close, p1 goes to root: %+v", anns)
	}
	if err := callAs(e.as(root), MUserOffboard, "o-ann", Offboard{User: ann.User}, nil); err != nil {
		t.Fatal(err)
	}
	e.c.mu.Lock()
	shares, reads := len(e.c.st.Shares), e.c.readsSessions(eve, "far")
	e.c.mu.Unlock()
	if shares != 0 || reads {
		t.Fatalf("ann's machines are open to no one, their sessions included: %d %v", shares, reads)
	}
}

// TestTheLocalOwnerSeesEveryTaskInModeOne: mode 1's owner sees and changes the tasks without a creator too.
func TestTheLocalOwnerSeesEveryTaskInModeOne(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	old := task.Task{ID: "t-old", Title: "old", Dir: t.TempDir(), Status: task.StatusTodo}
	e.c.mu.Lock()
	err := e.c.commit(journal.System, nil, journal.NewEvent(task.ETaskCreated, old))
	e.c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var sf StateFold
	watchState(t, e.cli, WatchParams{}).fold(&sf, e.c.State().Seq)
	if sf.St.Tasks[old.ID] == nil {
		t.Fatal("the owner's watch lacks the old task")
	}
	if err := e.cli.Call(context.Background(), MTaskGet, task.RunRef{ID: old.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.cli, MTaskEdit, "e-old", task.TaskEdit{ID: old.ID, Title: ptr("kept")}, nil); err != nil {
		t.Fatalf("the owner changes it: %v", err)
	}
}
