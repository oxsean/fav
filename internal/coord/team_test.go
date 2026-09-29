package coord

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

var (
	root = Principal{User: "u_root", Admin: true}
	ann  = Principal{User: "u_ann"} // owns the machines
	bob  = Principal{User: "u_bob"} // participates in p1
	cy   = Principal{User: "u_cy"}  // in no project
	dee  = Principal{User: "u_dee"} // reads p1
)

// team is an env in team mode: ann owns every machine, and p1 has bob participating and dee reading.
func team(t *testing.T, cfg tend.Config) *env {
	e := newEnv(t, cfg)
	e.owner = func(string) string { return ann.User }
	e.users = map[string]User{}
	for _, p := range []Principal{root, ann, bob, cy, dee} {
		e.users[p.User] = User{ID: p.User, Name: p.User[2:], Email: p.User[2:] + "@example.com", Admin: p.Admin}
	}
	return e
}

func (e *env) project() {
	e.t.Helper()
	as := e.as(root)
	if err := callAs(as, MProjectCreate, "p", ProjectCreate{ID: "p1", Name: "One", Owner: ann.User}, nil); err != nil {
		e.t.Fatal(err)
	}
	for _, m := range []task.MemberSet{{Project: "p1", User: bob.User, Role: task.RoleParticipant}, {Project: "p1", User: dee.User, Role: task.RoleReader}} {
		if err := callAs(e.as(ann), MProjectMember, "m-"+m.User, m, nil); err != nil {
			e.t.Fatal(err)
		}
	}
	for name := range e.c.ms {
		if err := callAs(e.as(ann), MMachineShare, "s-"+name, task.Share{Machine: name, Projects: []string{"p1"}}, nil); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *env) taskAs(p Principal, id, project string, agentName string) *task.Task {
	e.t.Helper()
	var tk task.Task
	if err := callAs(e.as(p), MTaskCreate, id, TaskCreate{Title: id, Dir: e.t.TempDir(), Agent: agentName, Project: project}, &tk); err != nil {
		e.t.Fatal(err)
	}
	return &tk
}

func TestSomeoneOutsideAProjectSeesNothingOfIt(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	var mu sync.Mutex
	var pushed []journal.Envelope
	watcher, _ := wire.Pipe(wire.Options{OnPush: func(method string, params json.RawMessage) {
		var env journal.Envelope
		if method == PushJournal && json.Unmarshal(params, &env) == nil {
			mu.Lock()
			pushed = append(pushed, env)
			mu.Unlock()
		}
	}}, wire.Options{Handler: e.c.HandlerFor(cy)})
	defer watcher.Close()
	if err := callAs(watcher, MSubscribe, "", SubscribeParams{}, nil); err != nil {
		t.Fatal(err)
	}

	e.project()
	tk := e.taskAs(bob, "b1", "p1", "quick")
	var r task.Run
	if err := callAs(e.as(bob), MRunDispatch, "d1", Dispatch{Task: tk.ID, Runner: node.RunnerBackground}, &r); err != nil {
		t.Fatalf("bob dispatches to ann's machine, which his project may use: %v", err)
	}
	end := e.wait(r.ID, ended)
	var spec node.Spec
	if b, err := os.ReadFile(filepath.Join(e.home, "node", "runs", r.ID, "spec.json")); err != nil || json.Unmarshal(b, &spec) != nil ||
		spec.Dispatcher == nil || spec.Dispatcher.Email != "bob@example.com" || spec.Project != "p1" {
		t.Fatalf("the node keeps who dispatched it, to author its commits: %+v %v", spec, err)
	}

	c := e.as(cy)
	var st task.State
	if err := callAs(c, MStateGet, "", StateParams{}, &st); err != nil || len(st.Tasks)+len(st.Runs)+len(st.Projects) != 0 {
		t.Fatalf("cy's state: %+v %v", st, err)
	}
	for _, call := range []struct {
		method string
		params any
		code   string
	}{
		{MTaskGet, task.RunRef{ID: tk.ID}, wire.CodeNotFound},
		{MRunTail, TailParams{Run: r.ID, Before: -1}, wire.CodeNotFound},
		{MRunOutputPage, OutputPageParams{Run: r.ID, Before: -1}, wire.CodeNotFound},
		{MRunMessages, RunMessages{Run: r.ID, Before: -1, N: 10}, wire.CodeNotFound},
		{MNodeCall, NodeCall{Machine: Local, Method: remote.MList}, wire.CodeUnauthorized},
		{MRunPreview, Dispatch{Task: tk.ID}, wire.CodeNotFound},
	} {
		if err := callAs(c, call.method, "", call.params, nil); wire.Code(err) != call.code {
			t.Errorf("cy calls %s: %v, want %s", call.method, err, call.code)
		}
	}
	for _, call := range []struct {
		method string
		params any
	}{
		{MTaskEdit, task.TaskEdit{ID: tk.ID, Title: ptr("mine")}},
		{MRunDispatch, Dispatch{Task: tk.ID}},
		{MRunContinue, Continue{Run: end.ID, Text: "go on"}},
		{MRunContinue, Continue{Session: "s1", Provider: tend.ProviderClaude, Dir: t.TempDir(), Text: "go on"}},
		{MRunStop, task.RunRef{ID: r.ID}},
	} {
		if err := callAs(c, call.method, "x-"+call.method, call.params, nil); wire.Code(err) != wire.CodeNotFound && wire.Code(err) != wire.CodeUnauthorized {
			t.Errorf("cy calls %s: %v", call.method, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := slices.Clone(pushed)
		mu.Unlock()
		if n := len(got); n > 0 && got[n-1].Seq == e.c.State().Seq {
			for i, env := range got {
				if env.Seq != int64(i+1) || len(env.Events) != 0 || env.Command != nil {
					b, _ := json.Marshal(env)
					t.Fatalf("cy gets every seq and nothing in it: %s", b)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pushes to cy: %d, state at %d", len(got), e.c.State().Seq)
		}
		time.Sleep(20 * time.Millisecond)
	}

	var ms Machines
	if err := callAs(c, MMachineList, "", MachinesParams{}, &ms); err != nil || len(ms.Machines) != 0 {
		t.Fatalf("cy sees no machine of ann's: %+v %v", ms, err)
	}
	if err := callAs(e.as(bob), MMachineList, "", MachinesParams{}, &ms); err != nil || len(ms.Machines) != 1 {
		t.Fatalf("bob sees the machine his project may use: %+v %v", ms, err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestAReaderSeesButChangesNothing(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	tk := e.taskAs(bob, "b1", "p1", "quick")
	d := e.as(dee)
	var got task.Task
	if err := callAs(d, MTaskGet, "", task.RunRef{ID: tk.ID}, &got); err != nil || got.ID != tk.ID {
		t.Fatalf("dee reads p1: %v", err)
	}
	if err := callAs(d, MTaskCreate, "d1", TaskCreate{Title: "x", Dir: t.TempDir(), Project: "p1"}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a reader creates nothing: %v", err)
	}
	if err := callAs(d, MRunDispatch, "d2", Dispatch{Task: tk.ID}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a reader dispatches nothing: %v", err)
	}
	var mine task.Task
	if err := callAs(e.as(bob), MTaskCreate, "b2", TaskCreate{Title: "x", Dir: t.TempDir()}, &mine); err != nil || mine.Owner != bob.User {
		t.Fatalf("a task outside any project is its creator's: %+v %v", mine, err)
	}
	if err := callAs(d, MTaskGet, "", task.RunRef{ID: mine.ID}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("and nobody else's: %v", err)
	}
	if err := callAs(e.as(bob), MProjectMember, "b3", task.MemberSet{Project: "p1", User: cy.User, Role: task.RoleParticipant}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("only the owner or an admin manages members: %v", err)
	}
	if err := callAs(e.as(ann), MProjectMember, "a1", task.MemberSet{Project: "p1", User: "u_nobody", Role: task.RoleReader}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("a member is a user: %v", err)
	}
}

func TestAReplayedCommandIsNotAnsweredOnceItsSubjectIsGone(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	p := TaskCreate{Title: "b1", Dir: t.TempDir(), Project: "p1"}
	var first task.Task
	if err := callAs(e.as(bob), MTaskCreate, "b1", p, &first); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(ann), MProjectMember, "rm", task.MemberSet{Project: "p1", User: bob.User}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(bob), MTaskCreate, "b1", p, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("bob replays a command about a task he no longer sees: %v", err)
	}
}

func TestAQueuedRunIsCanceledWhenItsDispatcherLosesAccess(t *testing.T) {
	f := newFar(t)
	f.set(&wire.Error{Code: wire.CodeOffline, Detail: "no route"})
	e := team(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
	e.dial = f.dial
	e.start()
	e.project()
	tk := e.taskAs(bob, "b1", "p1", "quick")
	var r task.Run
	if err := callAs(e.as(bob), MRunDispatch, "d1", Dispatch{Task: tk.ID, Machine: "far"}, &r); err != nil {
		t.Fatal(err)
	}
	if r.Dispatcher != bob.User || r.Project != "p1" {
		t.Fatalf("a run keeps who queued it and for which project: %+v", r)
	}
	if err := callAs(e.as(ann), MProjectMember, "rm", task.MemberSet{Project: "p1", User: bob.User}, nil); err != nil {
		t.Fatal(err)
	}
	f.set(nil)
	e.must(MMachineList, MachinesParams{Connect: true}, nil)
	end := e.wait(r.ID, ended)
	if end.State != task.Canceled || end.Reason != ReasonAccessRevoked {
		t.Fatalf("%+v", end)
	}
}

func TestAMachineIsOnlyForWhomItIsShared(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	if err := callAs(e.as(ann), MMachineShare, "s0", task.Share{Machine: Local}, nil); err != nil {
		t.Fatal(err)
	}
	tk := e.taskAs(bob, "b1", "p1", "quick")
	var pv Preview
	if err := callAs(e.as(bob), MRunPreview, "", Dispatch{Task: tk.ID}, &pv); err != nil || !hasWhy(pv.Blockers, WhyNoAccess) {
		t.Fatalf("the preview says the machine is not open to him: %+v %v", pv, err)
	}
	if err := callAs(e.as(bob), MRunDispatch, "d1", Dispatch{Task: tk.ID}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("an unshared machine: %v", err)
	}
	if err := callAs(e.as(bob), MMachineShare, "s1", task.Share{Machine: Local, Users: []string{bob.User}}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("a machine not shared with him is not there for him: %v", err)
	}
	if err := callAs(e.as(ann), MMachineShare, "s2", task.Share{Machine: Local, Users: []string{bob.User}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(bob), MRunDispatch, "d2", Dispatch{Task: tk.ID}, nil); err != nil {
		t.Fatalf("shared with him: %v", err)
	}
}

func TestOnlyTheOwnerOrTheDispatcherGrantsAPermission(t *testing.T) {
	e := team(t, tend.Config{Agents: []tend.AgentProfile{{Name: "gated", Provider: agent.ProviderFake,
		Args: []string{"--steps", "2", "--every", "20ms", "--permission", "Bash:make deploy"}}}})
	e.start()
	e.project()
	if err := callAs(e.as(ann), MProjectMember, "m-cy", task.MemberSet{Project: "p1", User: cy.User, Role: task.RoleParticipant}, nil); err != nil {
		t.Fatal(err)
	}
	tk := e.taskAs(bob, "b1", "p1", "gated")
	var r task.Run
	if err := callAs(e.as(bob), MRunDispatch, "d1", Dispatch{Task: tk.ID, Runner: node.RunnerBackground}, &r); err != nil {
		t.Fatal(err)
	}
	w := e.wait(r.ID, func(r *task.Run) bool { return len(r.Requests) == 1 })
	allow := Answer{Run: r.ID, Answer: agent.Answer{Request: w.Requests[0].ID, Allow: true}}
	if err := callAs(e.as(cy), MRunAnswer, "c1", allow, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("another participant grants no permission: %v", err)
	}
	if err := callAs(e.as(bob), MRunAnswer, "b2", allow, nil); err != nil {
		t.Fatalf("its dispatcher does: %v", err)
	}
	if end := e.wait(r.ID, ended); end.State != task.Exited {
		t.Fatalf("%+v", end)
	}
}

func TestMembershipChangesTellSubscribersToFetchAgain(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	got := make(chan string, 16)
	w, _ := wire.Pipe(wire.Options{OnPush: func(method string, _ json.RawMessage) { got <- method }}, wire.Options{Handler: e.c.HandlerFor(bob)})
	defer w.Close()
	if err := callAs(w, MSubscribe, "", SubscribeParams{AfterSeq: e.c.State().Seq}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(ann), MProjectMember, "m-cy", task.MemberSet{Project: "p1", User: cy.User, Role: task.RoleReader}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-got:
			if m == PushRefetch {
				return
			}
		case <-deadline:
			t.Fatal("no refetch")
		}
	}
}

func TestHandingOverATaskOutsideAProjectTellsSubscribersToFetchAgain(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	tk := e.taskAs(bob, "b1", "", "")
	got := make(chan string, 16)
	w, _ := wire.Pipe(wire.Options{OnPush: func(method string, _ json.RawMessage) { got <- method }}, wire.Options{Handler: e.c.HandlerFor(bob)})
	defer w.Close()
	if err := callAs(w, MSubscribe, "", SubscribeParams{AfterSeq: e.c.State().Seq}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(bob), MTaskEdit, "e1", task.TaskEdit{ID: tk.ID, Owner: ptr(root.User)}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-got:
			if m == PushRefetch {
				return
			}
		case <-deadline:
			t.Fatal("no refetch")
		}
	}
}

func TestTheLocalOwnerKeepsEverythingInModeOne(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "quick").ID})
	if r.Dispatcher != Owner.User {
		t.Fatalf("%+v", r)
	}
	if end := e.wait(r.ID, ended); end.State != task.Exited {
		t.Fatalf("%+v", end)
	}
	if err := e.cli.Call(context.Background(), MNodeCall, NodeCall{Machine: Local, Method: remote.MList}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestALeavingMemberHandsTheirWorkOn(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	x := e.taskAs(bob, "bobs", "p1", "")
	mine := e.taskAs(bob, "private", "", "")
	if err := callAs(e.as(bob), MAgentDefSave, "d", AgentDefSave{Text: careful}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(bob), MUserOffboard, "o0", Offboard{User: bob.User}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("only an admin hands someone's work on: %v", err)
	}
	if err := callAs(e.as(root), MUserOffboard, "o1", Offboard{User: bob.User, To: cy.User}, nil); err != nil {
		t.Fatal(err)
	}
	st := e.c.State()
	if st.Tasks[x.ID].Owner != ann.User || st.Tasks[mine.ID].Owner != cy.User || st.Projects["p1"].Role(bob.User) != "" ||
		st.AgentDefs["careful"].Owner != cy.User {
		t.Fatalf("p1's task goes to p1's owner, the rest to cy; bob leaves p1: %+v %+v %v", st.Tasks[x.ID], st.Tasks[mine.ID], st.Projects["p1"].Members)
	}
	if err := callAs(e.as(root), MUserOffboard, "o2", Offboard{User: ann.User}, nil); err != nil {
		t.Fatal(err)
	}
	st = e.c.State()
	if len(st.Shares) != 0 || st.Projects["p1"].Owner != root.User {
		t.Fatalf("ann's machines are open to no one else, and p1 is root's: %+v %s", st.Shares, st.Projects["p1"].Owner)
	}
}

func TestOwnersAndApproversChangeHandsByTheRules(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	x := e.taskAs(bob, "bobs", "p1", "")
	if err := callAs(e.as(bob), MTaskEdit, "e1", task.TaskEdit{ID: x.ID, Approver: ptr(dee.User)}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a reader accepts nothing: %v", err)
	}
	if err := callAs(e.as(bob), MTaskEdit, "e2", task.TaskEdit{ID: x.ID, Approver: ptr(ann.User)}, nil); err != nil {
		t.Fatal(err)
	}
	y := e.taskAs(ann, "anns", "p1", "")
	if err := callAs(e.as(bob), MTaskEdit, "e3", task.TaskEdit{ID: y.ID, Owner: ptr(bob.User)}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a participant does not take another's task: %v", err)
	}
	var got task.Task
	if err := callAs(e.as(ann), MTaskEdit, "e4", task.TaskEdit{ID: x.ID, Owner: ptr(ann.User)}, &got); err != nil || got.Owner != ann.User {
		t.Fatalf("the project's owner hands tasks on: %+v %v", got, err)
	}
	if err := callAs(e.as(bob), MTaskCreate, "c1", TaskCreate{Title: "z", Project: "p1", Approver: cy.User}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("an approver outside the project: %v", err)
	}
}

// Once its owner is disabled a machine is retired, and a run still open on it waits for an admin to stop or abandon.
func TestTheRunsOfARetiredMachineWaitForAnAdmin(t *testing.T) {
	e := team(t, tend.Config{Agents: []tend.AgentProfile{{Name: "long", Provider: agent.ProviderFake, Args: []string{"--steps", "400", "--every", "50ms"}}}})
	e.start()
	e.project()
	tk := e.taskAs(bob, "b1", "p1", "long")
	var r task.Run
	if err := callAs(e.as(bob), MRunDispatch, "d1", Dispatch{Task: tk.ID, Runner: node.RunnerBackground}, &r); err != nil {
		t.Fatal(err)
	}
	e.wait(r.ID, state(task.Running))
	inbox := func(p Principal) []InboxItem {
		var in Inbox
		if err := callAs(e.as(p), MInboxList, "", nil, &in); err != nil {
			t.Fatal(err)
		}
		return in.Items
	}
	if len(inbox(root)) != 0 {
		t.Fatal("a running task waits for no one")
	}
	if err := callAs(e.as(root), MUserOffboard, "o", Offboard{User: ann.User}, nil); err != nil {
		t.Fatal(err)
	}
	e.usersM.Lock()
	u := e.users[ann.User]
	u.Disabled = true
	e.users[ann.User] = u
	e.usersM.Unlock()
	var ms Machines
	if err := callAs(e.as(root), MMachineList, "", nil, &ms); err != nil {
		t.Fatal(err)
	}
	for _, m := range ms.Machines {
		if !m.Retired {
			t.Fatalf("ann's machines retire with her: %+v", m)
		}
	}
	got := inbox(root)
	if len(got) != 1 || got[0].Task != tk.ID || got[0].Run != r.ID || got[0].Reason != task.Running || !slices.Equal(got[0].As, []string{AsAdmin}) {
		t.Fatalf("the admin is to stop or abandon it: %+v", got)
	}
	if len(inbox(bob)) != 0 || len(inbox(cy)) != 0 {
		t.Fatal("only admins")
	}
	if err := callAs(e.as(root), MRunStop, "s", task.RunRef{ID: r.ID}, nil); err != nil {
		t.Fatalf("an admin stops it: %v", err)
	}
	e.wait(r.ID, ended)
	if len(inbox(root)) != 0 {
		t.Fatalf("%+v", inbox(root))
	}
}

func TestTaskEventsReachWhoeverReadsTheTask(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	tk := e.taskAs(bob, "b1", "p1", "quick")
	about, _ := json.Marshal(map[string]string{"id": tk.ID})
	for _, typ := range []string{task.EPlanDrafted, task.EPlanApplied, task.ETaskLinked} {
		ev := journal.Event{Type: typ, Data: about}
		e.c.mu.Lock()
		reader, outsider := e.c.sees(dee, ev), e.c.sees(cy, ev)
		e.c.mu.Unlock()
		if !reader || outsider {
			t.Errorf("%s: the project's reader sees it %v, someone outside %v", typ, reader, outsider)
		}
	}
}
