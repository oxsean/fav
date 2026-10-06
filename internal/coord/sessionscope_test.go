package coord

import (
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// scoped is a team-mode env whose machine, this one, holds the fixture's sessions and is owned by whom owner says (ann
// to begin with); p1 has bob participating and dee reading, and is given no directory.
func scoped(t *testing.T) (*env, *string) {
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	e := team(t, tend.Config{})
	owner := ann.User
	e.owner = func(string) string { return owner }
	e.start()
	e.c.mu.Lock()
	e.c.opt.MachineOwner = func(string) string { return owner }
	e.c.mu.Unlock()
	if err := callAs(e.as(root), MProjectCreate, "p", ProjectCreate{ID: "p1", Name: "One", Owner: ann.User}, nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range []task.MemberSet{{Project: "p1", User: bob.User, Role: task.RoleParticipant}, {Project: "p1", User: dee.User, Role: task.RoleReader}} {
		if err := callAs(e.as(ann), MProjectMember, "m-"+m.User, m, nil); err != nil {
			t.Fatal(err)
		}
	}
	return e, &owner
}

// reads is what p gets reading this machine's sessions through node.call: "" read, else the error code; sessions.query
// asks the machine exactly when node.call reads it.
func (e *env) reads(p Principal) string {
	e.t.Helper()
	var list remote.List
	err := callAs(e.as(p), MNodeCall, "", NodeCall{Machine: Local, Method: remote.MList}, &list)
	listed, read := projectsOn(e.t, e.as(p))
	if read != (err == nil) {
		e.t.Fatalf("%s: node.call %v, asked by sessions.query: %v", p.User, err, read)
	}
	if err == nil && (len(list.Sessions) == 0 || len(listed) == 0) {
		e.t.Fatalf("%s read %d sessions, %d through sessions.query", p.User, len(list.Sessions), len(listed))
	}
	return wire.Code(err)
}

// listed is this machine as p's machine.list has it; nil when p does not see it.
func (e *env) listed(p Principal) *Machine {
	e.t.Helper()
	var ms Machines
	if err := callAs(e.as(p), MMachineList, "", MachinesParams{}, &ms); err != nil {
		e.t.Fatal(err)
	}
	at := slices.IndexFunc(ms.Machines, func(m Machine) bool { return m.Name == Local })
	if at < 0 {
		return nil
	}
	return &ms.Machines[at]
}

// TestASessionScopeDecidesWhoReadsAMachinesSessions: the owner sets who else reads their machine's sessions; nobody
// else does, admins included; whom the scope names sees the machine and reads it, and nothing more.
func TestASessionScopeDecidesWhoReadsAMachinesSessions(t *testing.T) {
	e, _ := scoped(t)
	everyone := []Principal{root, ann, bob, cy, dee}
	n := 0
	set := func(by Principal, s task.SessionsSet) error {
		n++
		s.Machine = Local
		return callAs(e.as(by), MMachineSessions, "ms"+strconv.Itoa(n), s, nil)
	}
	for _, c := range []struct {
		name  string
		scope task.SessionsSet
		read  []Principal
	}{
		{"private", task.SessionsSet{}, []Principal{ann}},
		{"people", task.SessionsSet{Users: []string{cy.User}}, []Principal{ann, cy}},
		{"a project's members", task.SessionsSet{Projects: []string{"p1"}}, []Principal{ann, bob, dee}},
		{"the team", task.SessionsSet{Team: true}, everyone},
		{"private again", task.SessionsSet{}, []Principal{ann}},
	} {
		if err := set(ann, c.scope); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, p := range everyone {
			want := ""
			if !slices.Contains(c.read, p) {
				want = wire.CodeUnauthorized
			}
			if got := e.reads(p); got != want {
				t.Errorf("%s: %s reads: %q, want %q", c.name, p.User, got, want)
			}
			m := e.listed(p)
			switch {
			case (m != nil) != (p == root || slices.Contains(c.read, p)):
				t.Errorf("%s: %s sees the machine: %v", c.name, p.User, m != nil)
			case m != nil && m.Sessions != slices.Contains(c.read, p):
				t.Errorf("%s: %s is told they read it: %v", c.name, p.User, m.Sessions)
			case m != nil && p != ann && m.SessionShare != nil:
				t.Errorf("%s: %s is told the scope: %+v", c.name, p.User, m.SessionShare)
			}
		}
		if m := e.listed(ann); (m.SessionShare != nil) != (c.scope.Users != nil || c.scope.Projects != nil || c.scope.Team) {
			t.Errorf("%s: the owner is told the scope: %+v", c.name, m.SessionShare)
		}
	}

	if err := set(root, task.SessionsSet{Team: true}); wire.Code(err) != wire.CodeUnauthorized {
		t.Errorf("an admin sets no one else's scope: %v", err)
	}
	if err := set(cy, task.SessionsSet{Team: true}); wire.Code(err) != wire.CodeNotFound {
		t.Errorf("someone who does not see the machine: %v", err)
	}
	if err := set(ann, task.SessionsSet{Users: []string{"u_nobody"}}); wire.Code(err) != wire.CodeNotFound {
		t.Errorf("an unknown user: %v", err)
	}
	if err := set(ann, task.SessionsSet{Projects: []string{"nope"}}); wire.Code(err) != wire.CodeNotFound {
		t.Errorf("an unknown project: %v", err)
	}
	var dirs node.Dirs
	if err := callAs(e.as(root), MNodeCall, "", NodeCall{Machine: Local, Method: node.MDirs, Params: mustJSON(node.DirsParams{})}, &dirs); wire.Code(err) == wire.CodeUnauthorized {
		t.Errorf("an admin still lists the machine's directories: %v", err)
	}

	if err := set(ann, task.SessionsSet{Users: []string{cy.User}}); err != nil {
		t.Fatal(err)
	}
	var sh task.Share
	if err := callAs(e.as(ann), MMachineShare, "share", task.Share{Machine: Local, Users: []string{bob.User}, Sessions: &task.SessionShare{Team: true}}, &sh); err != nil {
		t.Fatal(err)
	}
	if sh.Sessions != nil || e.reads(cy) != "" || e.reads(dee) != wire.CodeUnauthorized {
		t.Errorf("machine.share leaves the scope as it was, whatever it is sent: %+v", sh)
	}
}

// TestASessionScopeChangeResetsWhomItConcerns: who sees the machine changes, so watchers get a new snapshot.
func TestASessionScopeChangeResetsWhomItConcerns(t *testing.T) {
	e, _ := scoped(t)
	w := watchState(t, e.as(cy), WatchParams{})
	var f StateFold
	w.fold(&f, e.c.State().Seq)
	if err := callAs(e.as(ann), MMachineSessions, "ms", task.SessionsSet{Machine: Local, Users: []string{cy.User}}, nil); err != nil {
		t.Fatal(err)
	}
	for {
		p := w.next()
		if p.Method == PushJournal {
			t.Fatalf("the scope comes as a reset, not in a journal push: %s", p.Params)
		}
		if _, err := f.Apply(p); err != nil {
			t.Fatal(err)
		}
		if p.Method == PushReset {
			break
		}
	}
	w.fold(&f, e.c.State().Seq)
	if f.St.Shares[Local] == nil || f.St.Shares[Local].Sessions != nil {
		t.Errorf("cy's snapshot has the machine, not its scope: %+v", f.St.Shares[Local])
	}
}

// TestAProjectScopeOpensEveryShareOfTheMachine (Q2): a project in the scope says who reads, not what: its members read
// the machine's sessions outside the project's directories too.
func TestAProjectScopeOpensEveryShareOfTheMachine(t *testing.T) {
	e, _ := scoped(t)
	if err := callAs(e.as(ann), MMachineSessions, "ms", task.SessionsSet{Machine: Local, Projects: []string{"p1"}}, nil); err != nil {
		t.Fatal(err)
	}
	listed, read := projectsOn(t, e.as(bob))
	in := 0
	for _, id := range listed {
		if id != "" {
			in++
		}
	}
	if !read || len(listed) < 5 || in != 0 {
		t.Fatalf("bob reads every session of the machine, none of them in p1: %d sessions, %d in a project, %v", len(listed), in, read)
	}
}

// TestAMachineUnderLocalIsReadByNoAdmin: a machine owned by the built-in user local is read by no admin; once given
// to a real owner, they read it and local and the admins do not.
func TestAMachineUnderLocalIsReadByNoAdmin(t *testing.T) {
	e, owner := scoped(t)
	*owner = Owner.User
	e.c.Reaffirm()
	if got := e.reads(root); got != wire.CodeUnauthorized {
		t.Errorf("an admin reads a machine under local: %q", got)
	}
	if got := e.reads(Owner); got != "" {
		t.Errorf("local reads its own machine: %q", got)
	}
	*owner = bob.User // tend-server token owner
	e.c.Reaffirm()
	for p, want := range map[Principal]string{bob: "", Owner: wire.CodeUnauthorized, root: wire.CodeUnauthorized, ann: wire.CodeUnauthorized} {
		if got := e.reads(p); got != want {
			t.Errorf("given to bob, %s reads: %q, want %q", p.User, got, want)
		}
	}
	if m := e.listed(bob); m == nil || m.Owner != bob.User || !m.Sessions {
		t.Errorf("bob's machine list: %+v", m)
	}
}

// Mode 1: the one user reads every machine, and sets a scope nobody else uses.
func TestModeOneReadsItsMachinesAsBefore(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	if err := e.call(MNodeCall, NodeCall{Machine: Local, Method: remote.MList}, nil); err != nil {
		t.Fatal(err)
	}
	if _, read := projectsOn(t, e.cli); !read {
		t.Fatal("sessions.query does not ask this machine")
	}
	var ms Machines
	e.must(MMachineList, MachinesParams{}, &ms)
	if len(ms.Machines) == 0 || !ms.Machines[0].Sessions {
		t.Fatalf("mode 1's owner reads this machine: %+v", ms.Machines)
	}
	e.must(MMachineSessions, task.SessionsSet{Machine: Local, Team: true}, nil)
}
