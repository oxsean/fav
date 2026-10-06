package coord

import (
	"testing"

	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func sessionContinue(machine, project string) Continue {
	return Continue{Machine: machine, Provider: tend.ProviderClaude, Session: "s-native", Dir: "/w/notes", Title: "Tag rules", Text: "add tests",
		Project: project}
}

func TestASessionContinuedAloneIsAPrivateTaskOfItsOwner(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	var r task.Run
	e.must(MRunContinue, sessionContinue("", ""), &r)
	tk := e.c.State().Tasks[r.Task]
	if tk == nil || tk.Project != "" || r.Project != "" || tk.Owner != Owner.User || tk.Title != "Tag rules" || r.Resume != "s-native" {
		t.Fatalf("a private task continuing the session: %+v %+v", tk, r)
	}

	e.must(MProjectCreate, ProjectCreate{ID: "notes", Name: "Notes"}, nil)
	e.must(MRunContinue, sessionContinue("", "notes"), &r)
	if tk := e.c.State().Tasks[r.Task]; tk == nil || tk.Project != "notes" || r.Project != "notes" || r.Resume != "s-native" {
		t.Fatalf("the task and its run are the project's: %+v %+v", tk, r)
	}
	if err := e.call(MRunContinue, sessionContinue("", "nowhere"), nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("a project that is not there: %v", err)
	}
}

func TestASessionGoesIntoAProjectOnlyForItsParticipants(t *testing.T) {
	e := stage(t, true)
	var r task.Run
	if err := callAs(e.as(ann), MRunContinue, "c-p1", sessionContinue("far", "p1"), &r); err != nil {
		t.Fatalf("ann owns far and takes part in p1: %v", err)
	}
	if tk := e.c.State().Tasks[r.Task]; tk == nil || tk.Project != "p1" || r.Project != "p1" || r.Resume != "s-native" || r.Machine != "far" {
		t.Fatalf("%+v %+v", tk, r)
	}
	if err := callAs(e.as(bob), MTaskGet, "", task.RunRef{ID: r.Task}, nil); err != nil {
		t.Fatalf("p1's members see it: %v", err)
	}

	if err := callAs(e.as(root), MProjectCreate, "p-read", ProjectCreate{ID: "p2", Name: "Two", Owner: bob.User}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(bob), MProjectMember, "m-ann", task.MemberSet{Project: "p2", User: ann.User, Role: task.RoleReader}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(ann), MRunContinue, "c-p2", sessionContinue("far", "p2"), nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("ann only reads p2: %v", err)
	}
	for _, machine := range []string{"far", "solo"} {
		if err := callAs(e.as(bob), MRunContinue, "c-bob-"+machine, sessionContinue(machine, "p1"), nil); wire.Code(err) != wire.CodeUnauthorized {
			t.Fatalf("bob takes part in p1 but %s is ann's: %v", machine, err)
		}
	}
	if err := callAs(e.as(cy), MRunContinue, "c-cy", sessionContinue("far", "p1"), nil); err == nil {
		t.Fatal("cy is in no project")
	}
}

func TestASessionContinuedAloneIsHiddenFromAdmins(t *testing.T) {
	e := stage(t, true)
	var r task.Run
	if err := callAs(e.as(ann), MRunContinue, "c-alone", sessionContinue("far", ""), &r); err != nil {
		t.Fatal(err)
	}
	tk := e.c.State().Tasks[r.Task]
	if tk == nil || tk.Project != "" || tk.Owner != ann.User || r.Resume != "s-native" {
		t.Fatalf("%+v %+v", tk, r)
	}
	if err := callAs(e.as(ann), MTaskGet, "", task.RunRef{ID: tk.ID}, nil); err != nil {
		t.Fatalf("ann reads her own task: %v", err)
	}
	for _, p := range []Principal{root, bob} {
		if err := callAs(e.as(p), MTaskGet, "", task.RunRef{ID: tk.ID}, nil); wire.Code(err) != wire.CodeNotFound {
			t.Errorf("%s gets ann's private task: %v", p.User, err)
		}
	}
}
