package coord

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// A definition shared for use and not for reading stays unread through its runs: whoever may not read it gets a
// run's brief without its instructions and its profile without how it is set up, in a command's answer, a state, a
// snapshot, a pushed event, a preview and the agent list; its owner, and everyone once it is shared for reading, get
// all of it.
func TestADefinitionUsedButNotReadStaysUnreadThroughItsRuns(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	if err := callAs(e.as(bob), MAgentDefSave, "d1", AgentDefSave{Text: careful}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(bob), MAgentDefShare, "s1", task.AgentDefShare{Name: "careful", Share: task.DefShare{Projects: []string{"p1"}}}, nil); err != nil {
		t.Fatal(err)
	}
	watcher := watchState(t, e.as(dee), WatchParams{})
	var fold StateFold
	watcher.fold(&fold, 0)
	x := e.taskAs(ann, "fix the build", "p1", "careful")

	const body = "Read everything twice."
	unread := func(where string, r *task.Run) {
		t.Helper()
		if r == nil || strings.Contains(r.Brief, body) || !strings.Contains(r.Brief, "fix the build") || len(r.Profile.Deny) > 0 ||
			r.Profile.Effort != "high" {
			t.Fatalf("%s: %+v", where, r)
		}
	}
	read := func(where string, r *task.Run) {
		t.Helper()
		if r == nil || !strings.HasPrefix(r.Brief, body) || !slices.Equal(r.Profile.Deny, []string{"WebFetch"}) {
			t.Fatalf("%s: %+v", where, r)
		}
	}
	var r task.Run
	if err := callAs(e.as(ann), MRunDispatch, "r1", Dispatch{Task: x.ID}, &r); err != nil {
		t.Fatal(err)
	}
	unread("the dispatch's answer", &r)
	for {
		env := watcher.journal()
		i := slices.IndexFunc(env.Events, func(ev journal.Event) bool { return ev.Type == task.ERunQueued })
		if i < 0 {
			continue
		}
		var queued task.Run
		json.Unmarshal(env.Events[i].Data, &queued)
		unread("the pushed event", &queued)
		break
	}
	stateOf := func(p Principal) *task.State {
		var st task.State
		if err := callAs(e.as(p), MStateGet, "", StateParams{}, &st); err != nil {
			t.Fatal(err)
		}
		return &st
	}
	unread("ann's state", stateOf(ann).Runs[r.ID])
	unread("dee's state", stateOf(dee).Runs[r.ID])
	read("bob's state", stateOf(bob).Runs[r.ID])
	late := watchState(t, e.as(ann), WatchParams{})
	var snap StateFold
	late.fold(&snap, 1)
	unread("ann's snapshot", snap.St.Runs[r.ID])

	e.wait(r.ID, ended)
	var pv Preview
	if err := callAs(e.as(ann), MRunPreview, "", Dispatch{Task: x.ID}, &pv); err != nil || len(pv.Profile.Deny) > 0 || pv.Profile.Effort != "high" {
		t.Fatalf("ann's preview: %+v %v", pv.Profile, err)
	}
	agentsOf := func(p Principal) tend.AgentProfile {
		var as Agents
		if err := callAs(e.as(p), MAgentList, "", nil, &as); err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(as.Agents, func(a tend.AgentProfile) bool { return a.Name == "careful" })
		if i < 0 {
			t.Fatalf("%s lists no careful: %+v", p.User, as)
		}
		return as.Agents[i]
	}
	if a := agentsOf(ann); len(a.Deny) > 0 || a.Effort != "high" {
		t.Fatalf("ann's agent list: %+v", a)
	}
	if a := agentsOf(bob); !slices.Equal(a.Deny, []string{"WebFetch"}) {
		t.Fatalf("bob's agent list: %+v", a)
	}

	if err := callAs(e.as(bob), MAgentDefShare, "s2", task.AgentDefShare{Name: "careful", Share: task.DefShare{Projects: []string{"p1"}, View: true}}, nil); err != nil {
		t.Fatal(err)
	}
	read("ann's state once it is shared for reading", stateOf(ann).Runs[r.ID])
}
