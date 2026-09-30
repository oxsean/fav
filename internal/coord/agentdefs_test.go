package coord

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

const careful = `---
name: careful
description: slow and careful
role: implement
profile: quick
effort: high
tools: {deny: [WebFetch]}
---
Read everything twice.
`

func TestADefinitionIsAFileInModeOneAndRunsCompiled(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	var v AgentDefView
	e.must(MAgentDefSave, AgentDefSave{Text: careful}, &v)
	if v.Name != "careful" || v.Owner != Owner.User || !strings.Contains(v.Text, "Read everything twice.") {
		t.Fatalf("%+v", v)
	}
	if !slices.Contains(v.Launch, "<brief>") {
		t.Fatalf("its reader sees what a run starts: %q", v.Launch)
	}
	if b, err := os.ReadFile(filepath.Join(e.home, "defs", "agents", "careful.md")); err != nil || !strings.Contains(string(b), "effort: high") {
		t.Fatalf("mode 1 keeps it as a file: %s %v", b, err)
	}
	var list AgentDefList
	e.must(MAgentDefList, nil, &list)
	var agents Agents
	e.must(MAgentList, nil, &agents)
	if len(list.Defs) != 1 || !slices.ContainsFunc(agents.Agents, func(a tend.AgentProfile) bool { return a.Name == "careful" && a.Effort == "high" }) {
		t.Fatalf("%+v %+v", list, agents)
	}
	x := e.task("careful work", "careful")
	r := e.dispatch(Dispatch{Task: x.ID})
	if r.Profile.Name != "careful" || r.Profile.Effort != "high" || !slices.Equal(r.Profile.Deny, []string{"WebFetch"}) ||
		!strings.HasPrefix(r.Brief, "Read everything twice.") || !slices.Contains(runFeatures(r), node.FeatureAgentDef) {
		t.Fatalf("%+v", r)
	}
	if got := e.wait(r.ID, ended); got.State != task.Exited {
		t.Fatalf("the node applies it: %+v", got)
	}
	if err := e.call(MAgentDefSave, AgentDefSave{Text: strings.Replace(careful, "effort: high", "effort: huge", 1)}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("a bad definition is refused: %v", err)
	}
	e.must(MAgentDefRemove, task.AgentDefRef{Name: "careful"}, nil)
	if err := e.call(MRunDispatch, Dispatch{Task: e.task("y", "").ID, Agent: "careful"}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("a removed definition is gone: %v", err)
	}
}

func TestADefinitionIsItsOwnersUntilShared(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	if err := callAs(e.as(bob), MAgentDefSave, "d1", AgentDefSave{Text: careful}, nil); err != nil {
		t.Fatal(err)
	}
	x := e.taskAs(ann, "t1", "p1", "careful")
	var pv Preview
	if err := callAs(e.as(ann), MRunDispatch, "r1", Dispatch{Task: x.ID, Agent: "careful"}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("ann cannot use or even see bob's definition: %v", err)
	}
	if err := callAs(e.as(root), MAgentDefGet, "", task.AgentDefRef{Name: "careful"}, nil); err != nil {
		t.Fatalf("an admin sees every definition: %v", err)
	}
	if err := callAs(e.as(root), MRunDispatch, "r2", Dispatch{Task: x.ID, Agent: "careful"}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("but runs none that is not shared with them: %v", err)
	}
	if err := callAs(e.as(bob), MAgentDefShare, "s1", task.AgentDefShare{Name: "careful", Share: task.DefShare{Projects: []string{"p1"}}}, nil); err != nil {
		t.Fatal(err)
	}
	var v AgentDefView
	if err := callAs(e.as(ann), MAgentDefGet, "", task.AgentDefRef{Name: "careful"}, &v); err != nil || v.Text != "" || v.Manage {
		t.Fatalf("shared for use, not for reading: %+v %v", v, err)
	}
	var r task.Run
	if err := callAs(e.as(ann), MRunDispatch, "r3", Dispatch{Task: x.ID, Agent: "careful"}, &r); err != nil || r.Profile.Effort != "high" {
		t.Fatalf("a participant of p1 runs it for p1's tasks: %+v %v", r, err)
	}
	e.wait(r.ID, ended)
	mine := e.taskAs(ann, "t2", "", "")
	if err := callAs(e.as(ann), MRunDispatch, "r4", Dispatch{Task: mine.ID, Agent: "careful"}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("not for a task outside p1: %v", err)
	}
	if err := callAs(e.as(ann), MAgentDefSave, "d2", AgentDefSave{Text: careful}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("only its owner changes it: %v", err)
	}
	if err := callAs(e.as(ann), MAgentDefSave, "d3", AgentDefSave{Text: strings.Replace(careful, "name: careful", "name: shared", 1), Owner: "project:p1"}, &v); err != nil || v.Owner != "project:p1" {
		t.Fatalf("p1's owner gives a definition to p1: %+v %v", v, err)
	}
	if err := callAs(e.as(bob), MRunPreview, "", Dispatch{Task: x.ID, Agent: "shared"}, &pv); err != nil || len(pv.Blockers) > 0 {
		t.Fatalf("p1's participants use p1's definitions: %+v %v", pv, err)
	}
}

func TestADefinitionSaysWhoLastSavedItAndWhereItRuns(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	placed := strings.Replace(careful, "effort: high\n", "effort: high\nmachines: {require: [mba, linux], prefer: [linux]}\n", 1)
	var v AgentDefView
	if err := callAs(e.as(bob), MAgentDefSave, "d1", AgentDefSave{Text: placed}, &v); err != nil {
		t.Fatal(err)
	}
	if v.SavedBy != bob.User || v.SavedAt.IsZero() || !slices.Equal(v.Require, []string{"linux", "mba"}) && !slices.Equal(v.Require, []string{"mba", "linux"}) || !slices.Equal(v.Prefer, []string{"linux"}) {
		t.Fatalf("%+v", v)
	}
	saved := v.SavedAt
	if err := callAs(e.as(root), MAgentDefSave, "d2", AgentDefSave{Text: strings.Replace(placed, "twice", "three times", 1)}, &v); err != nil || v.SavedBy != root.User {
		t.Fatalf("an admin's edit is theirs: %+v %v", v, err)
	}
	if err := callAs(e.as(bob), MAgentDefShare, "s1", task.AgentDefShare{Name: "careful", Share: task.DefShare{All: true}}, nil); err != nil {
		t.Fatal(err)
	}
	var seen AgentDefView
	if err := callAs(e.as(ann), MAgentDefGet, "", task.AgentDefRef{Name: "careful"}, &seen); err != nil || seen.SavedBy != root.User || seen.SavedAt.Before(saved) ||
		len(seen.Require) != 2 || seen.Text != "" {
		t.Fatalf("sharing is no save, and whoever may use it sees who saved it and where it runs: %+v %v", seen, err)
	}
}

func TestOneSharedWithADefinitionLeavesItAndItsOwnerGivesItToAProject(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	if err := callAs(e.as(bob), MAgentDefSave, "d1", AgentDefSave{Text: careful}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(bob), MAgentDefShare, "s1", task.AgentDefShare{Name: "careful", Share: task.DefShare{Users: []string{cy.User, dee.User}}}, nil); err != nil {
		t.Fatal(err)
	}
	var v AgentDefView
	if err := callAs(e.as(cy), MAgentDefGet, "", task.AgentDefRef{Name: "careful"}, &v); err != nil || !v.Leave {
		t.Fatalf("one it is shared with by name may leave it: %+v %v", v, err)
	}
	if err := callAs(e.as(cy), MAgentDefLeave, "l1", task.AgentDefRef{Name: "careful"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(cy), MAgentDefGet, "", task.AgentDefRef{Name: "careful"}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("gone for cy: %v", err)
	}
	var owned AgentDefView
	if err := callAs(e.as(bob), MAgentDefGet, "", task.AgentDefRef{Name: "careful"}, &owned); err != nil || !slices.Equal(owned.Share.Users, []string{dee.User}) || owned.Leave {
		t.Fatalf("the others it is shared with keep it: %+v %v", owned, err)
	}
	if err := callAs(e.as(cy), MAgentDefLeave, "l2", task.AgentDefRef{Name: "careful"}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("left already: %v", err)
	}
	if err := callAs(e.as(bob), MAgentDefLeave, "l3", task.AgentDefRef{Name: "careful"}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("its owner does not leave it: %v", err)
	}

	if err := callAs(e.as(bob), MAgentDefTransfer, "t1", task.AgentDefTransfer{Name: "careful", Owner: "project:p1"}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("only to a project its owner owns: %v", err)
	}
	if err := callAs(e.as(bob), MAgentDefTransfer, "t2", task.AgentDefTransfer{Name: "careful", Owner: bob.User}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("to a project only: %v", err)
	}
	if err := callAs(e.as(root), MAgentDefTransfer, "t3", task.AgentDefTransfer{Name: "careful", Owner: "project:p9"}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("no such project: %v", err)
	}
	var moved AgentDefView
	if err := callAs(e.as(root), MAgentDefTransfer, "t4", task.AgentDefTransfer{Name: "careful", Owner: "project:p1"}, &moved); err != nil ||
		moved.Owner != "project:p1" || !slices.Equal(moved.Share.Users, []string{dee.User}) {
		t.Fatalf("an admin gives it to any project, its sharing kept: %+v %v", moved, err)
	}
	var ann1 AgentDefView
	if err := callAs(e.as(ann), MAgentDefGet, "", task.AgentDefRef{Name: "careful"}, &ann1); err != nil || !ann1.Manage {
		t.Fatalf("p1's owner manages it now: %+v %v", ann1, err)
	}
	if err := callAs(e.as(bob), MAgentDefSave, "d2", AgentDefSave{Text: careful}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("and bob, a participant, only uses it: %v", err)
	}
	st := e.c.state(true)
	if st.AgentDefs["careful"].Owner != "project:p1" {
		t.Fatalf("%+v", st.AgentDefs["careful"])
	}
}

// One who has a definition through a project or everyone leaves it too: it is gone for them alone, its owner's later
// sharing keeps them out, and sharing it with them by name brings them back.
func TestOneWhoHasADefinitionThroughAProjectOrEveryoneLeavesIt(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	x := e.taskAs(bob, "t1", "p1", "")
	if err := callAs(e.as(ann), MAgentDefSave, "d1", AgentDefSave{Text: careful}, nil); err != nil {
		t.Fatal(err)
	}
	share := func(id string, s task.DefShare) {
		t.Helper()
		if err := callAs(e.as(ann), MAgentDefShare, id, task.AgentDefShare{Name: "careful", Share: s}, nil); err != nil {
			t.Fatal(err)
		}
	}
	has := func(p Principal) bool {
		var v AgentDefView
		err := callAs(e.as(p), MAgentDefGet, "", task.AgentDefRef{Name: "careful"}, &v)
		if err == nil && !v.Leave {
			t.Fatalf("%s has it and may leave it: %+v", p.User, v)
		}
		return err == nil
	}
	share("s1", task.DefShare{Projects: []string{"p1"}, View: true})
	if !has(bob) {
		t.Fatal("bob has it through p1")
	}
	if err := callAs(e.as(bob), MAgentDefLeave, "l1", task.AgentDefRef{Name: "careful"}, nil); err != nil {
		t.Fatal(err)
	}
	var pv Preview
	if has(bob) || callAs(e.as(bob), MRunPreview, "", Dispatch{Task: x.ID, Agent: "careful"}, &pv) == nil && len(pv.Blockers) == 0 {
		t.Fatalf("gone for bob, who can no longer run it: %+v", pv)
	}
	if _, ok := e.c.visibleState(bob, e.c.state(true)).AgentDefs["careful"]; ok {
		t.Fatal("bob's state has it no more")
	}
	shared := func() task.DefShare {
		t.Helper()
		var v AgentDefView
		if err := callAs(e.as(ann), MAgentDefGet, "", task.AgentDefRef{Name: "careful"}, &v); err != nil {
			t.Fatal(err)
		}
		return *v.Share
	}
	if s := shared(); !slices.Equal(s.Left, []string{bob.User}) {
		t.Fatalf("its owner sees who left it: %+v", s)
	}
	share("s2", task.DefShare{Projects: []string{"p1"}, All: true})
	if has(bob) || !has(cy) {
		t.Fatal("shared with everyone, it stays gone for bob and reaches cy")
	}
	if err := callAs(e.as(cy), MAgentDefLeave, "l2", task.AgentDefRef{Name: "careful"}, nil); err != nil || has(cy) || !has(dee) {
		t.Fatalf("cy leaves it; dee keeps it: %v", err)
	}
	share("s3", task.DefShare{Users: []string{bob.User}, Projects: []string{"p1"}})
	if !has(bob) {
		t.Fatal("shared with bob by name, bob has it again")
	}
	if s := shared(); !slices.Equal(s.Left, []string{cy.User}) {
		t.Fatalf("named, bob is no longer one who left it: %+v", s)
	}
	if err := callAs(e.as(bob), MAgentDefLeave, "l3", task.AgentDefRef{Name: "careful"}, nil); err != nil || has(bob) {
		t.Fatalf("bob leaves it again, the name and the project both: %v", err)
	}
	if s := shared(); len(s.Users) != 0 || !slices.Equal(s.Left, []string{bob.User, cy.User}) {
		t.Fatalf("%+v", s)
	}
}
