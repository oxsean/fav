package coord

import (
	"context"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

var long = tend.AgentProfile{Name: "long", Provider: agent.ProviderFake, Args: []string{"--steps", "30", "--every", "50ms"}}

// still: run id stays queued through several passes.
func (e *env) still(id string) {
	e.t.Helper()
	for range 6 {
		e.c.Pass(context.Background())
		time.Sleep(50 * time.Millisecond)
	}
	if r := e.c.State().Runs[id]; r.State != task.Queued {
		e.t.Fatalf("started on a drained machine: %+v", r)
	}
}

// A drained machine lets what runs there finish and starts nothing new, however free its slots are; a run dispatched
// to it queues and says why, until it is taken back.
func TestADrainedMachineFinishesItsRunsAndStartsNoNewOnes(t *testing.T) {
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{long}})
	e.start()
	going := e.dispatch(Dispatch{Task: e.task("going", "long").ID})
	e.wait(going.ID, state(task.Running))
	var d task.Drain
	e.must(MMachineDrain, task.DrainSet{Machine: Local, On: true}, &d)
	if d.Machine != Local || d.By != Owner.User || d.At.IsZero() {
		t.Fatalf("the answer is the drain: %+v", d)
	}
	if got := e.c.State().Drains[Local]; got == nil || got.By != Owner.User {
		t.Fatalf("the state keeps it: %+v", got)
	}
	tk := e.task("next", "quick")
	next := e.dispatch(Dispatch{Task: tk.ID})
	var pv Preview
	e.must(MRunPreview, Dispatch{Task: e.task("more", "quick").ID}, &pv)
	if !hasWhy(pv.Notes, WhyDrain) || hasWhy(pv.Blockers, WhyDrain) {
		t.Fatalf("a preview says it waits for the machine to take runs again: %+v", pv)
	}
	if end := e.wait(going.ID, ended); end.State != task.Exited {
		t.Fatalf("what ran finishes: %+v", end)
	}
	e.still(next.ID)
	st := e.c.State()
	if sit := st.Situation(st.Tasks[tk.ID]); sit.Kind != task.SitQueued || sit.Reason != task.WhyDrain {
		t.Fatalf("the task says why it waits: %+v", sit)
	}
	var off task.Drain
	e.must(MMachineDrain, task.DrainSet{Machine: Local}, &off)
	if off.Machine != Local || !off.At.IsZero() || e.c.State().Drains[Local] != nil {
		t.Fatalf("taken back: %+v %+v", off, e.c.State().Drains)
	}
	if end := e.wait(next.ID, ended); end.State != task.Exited {
		t.Fatalf("taken back, it runs what waited: %+v", end)
	}
}

// Draining is kept in the journal: a coordinator that starts again starts nothing on the machine either.
func TestADrainOutlivesTheCoordinator(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	e.must(MMachineDrain, task.DrainSet{Machine: Local, On: true}, nil)
	r := e.dispatch(Dispatch{Task: e.task("x", "quick").ID})
	e.stop()
	e.start()
	if e.c.State().Drains[Local] == nil {
		t.Fatal("the drain is gone after a restart")
	}
	e.still(r.ID)
}

// Draining an already drained machine, or taking back one that is not, writes nothing.
func TestDrainingTwiceWritesOnce(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	e.must(MMachineDrain, task.DrainSet{Machine: Local, On: true}, nil)
	seq := e.c.State().Seq
	e.must(MMachineDrain, task.DrainSet{Machine: Local, On: true}, nil)
	e.must(MMachineDrain, task.DrainSet{Machine: Local}, nil)
	e.must(MMachineDrain, task.DrainSet{Machine: Local}, nil)
	if got := e.c.State().Seq; got != seq+1 {
		t.Fatalf("four commands, two changes: seq %d then %d", seq, got)
	}
	if err := e.call(MMachineDrain, task.DrainSet{Machine: "nowhere", On: true}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("no such machine: %v", err)
	}
}

// Only a machine's owner or an admin drains it; those who use or see it are refused, the others are not told it
// exists. Who sees the machine sees it drained.
func TestOnlyTheOwnerOrAnAdminDrainsAMachine(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	if err := callAs(e.as(cy), MMachineDrain, "d0", task.DrainSet{Machine: Local, On: true}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("a machine he does not see: %v", err)
	}
	for _, who := range []Principal{bob, dee} {
		if err := callAs(e.as(who), MMachineDrain, "d-"+who.User, task.DrainSet{Machine: Local, On: true}, nil); wire.Code(err) != wire.CodeUnauthorized {
			t.Fatalf("%s uses or sees it but does not own it: %v", who.User, err)
		}
	}
	if err := callAs(e.as(ann), MMachineDrain, "d1", task.DrainSet{Machine: Local, On: true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(root), MMachineDrain, "d2", task.DrainSet{Machine: Local}, nil); err != nil {
		t.Fatalf("an admin: %v", err)
	}
	if err := callAs(e.as(ann), MMachineDrain, "d3", task.DrainSet{Machine: Local, On: true}, nil); err != nil {
		t.Fatal(err)
	}
	var st task.State
	if err := callAs(e.as(bob), MStateGet, "", StateParams{}, &st); err != nil || st.Drains[Local] == nil || st.Drains[Local].By != ann.User {
		t.Fatalf("who sees the machine sees it drained: %+v %v", st.Drains, err)
	}
	var his task.State
	if err := callAs(e.as(cy), MStateGet, "", StateParams{}, &his); err != nil || len(his.Drains) != 0 {
		t.Fatalf("who does not, does not: %+v %v", his.Drains, err)
	}
}

// A definition that prefers machines goes to the first connected one that takes runs.
func TestADrainedMachineIsNotPreferred(t *testing.T) {
	a, b := newFar(t), newFar(t)
	e := newEnv(t, tend.Config{Hosts: []tend.Host{{Name: "a", SSH: "a"}, {Name: "b", SSH: "b"}}})
	e.dial = func(h tend.Host, opt wire.Options) (Conn, error) {
		if h.Name == "a" {
			return a.dial(h, opt)
		}
		return b.dial(h, opt)
	}
	e.start()
	e.must(MMachineList, MachinesParams{Connect: true}, nil)
	e.must(MAgentDefSave, AgentDefSave{Text: "---\nname: pick\nprofile: quick\nmachines:\n  prefer: [a, b]\n---\nwork\n"}, nil)
	if r := e.dispatch(Dispatch{Task: e.task("x", "pick").ID}); r.Machine != "a" {
		t.Fatalf("the first it prefers: %s", r.Machine)
	}
	e.must(MMachineDrain, task.DrainSet{Machine: "a", On: true}, nil)
	if r := e.dispatch(Dispatch{Task: e.task("y", "pick").ID, Runner: node.RunnerBackground}); r.Machine != "b" {
		t.Fatalf("the first that takes runs: %s", r.Machine)
	}
}
