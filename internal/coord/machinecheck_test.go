package coord

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// counted is a probe that answers every CLI installed and signed in, and counts how often each was probed.
type counted struct {
	mu   sync.Mutex
	n    map[string]int
	hold chan struct{} // when set, a probe waits for it to close
}

func (p *counted) probe(provider string) agent.Check {
	p.mu.Lock()
	hold := p.hold
	p.mu.Unlock()
	if hold != nil {
		<-hold
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.n == nil {
		p.n = map[string]int{}
	}
	p.n[provider]++
	return agent.Check{Installed: true, Version: "1.0", Auth: agent.AuthOK}
}

func (p *counted) of(provider string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n[provider]
}

func machineNamed(ms []Machine, name string) *Machine {
	for i := range ms {
		if ms[i].Name == name {
			return &ms[i]
		}
	}
	return nil
}

// A check asks the node to probe its agent CLIs again, even right after the last probe, and says when it did.
func TestACheckProbesTheCLIsAgainAndSaysWhen(t *testing.T) {
	p := &counted{}
	e := newEnv(t, tend.Config{})
	e.probe = p.probe
	e.start()
	var ms Machines
	e.must(MMachineList, MachinesParams{Connect: true}, &ms)
	if m := machineNamed(ms.Machines, Local); m == nil || m.CheckedAt == nil || m.Agents[tend.ProviderClaude].Version != "1.0" {
		t.Fatalf("connecting checks the CLIs at once and says when: %+v", ms)
	}
	before := p.of(tend.ProviderClaude)
	began := time.Now()
	var got MachineChecks
	e.must(MMachineCheck, MachineCheck{Machine: Local}, &got)
	if p.of(tend.ProviderClaude) != before+1 {
		t.Fatalf("a check probes again instead of answering the node's last probe: %d then %d", before, p.of(tend.ProviderClaude))
	}
	m := machineNamed(got.Machines, Local)
	if m == nil || m.CheckedAt == nil || m.CheckedAt.Before(began) || len(got.Failed) != 0 {
		t.Fatalf("the answer is the machine as checked now: %+v", got)
	}
	e.must(MMachineList, MachinesParams{}, &ms)
	if l := machineNamed(ms.Machines, Local); l.CheckedAt == nil || !l.CheckedAt.Equal(*m.CheckedAt) {
		t.Fatalf("the list says when it was last checked: %+v", l)
	}
	var pv Preview
	e.must(MRunPreview, Dispatch{Task: e.task("x", "claude").ID}, &pv)
	e.must(MMachineList, MachinesParams{}, &ms)
	if l := machineNamed(ms.Machines, Local); !l.CheckedAt.Equal(*m.CheckedAt) {
		t.Fatalf("a preview answered from the node's last probe is no new check: %v then %v", m.CheckedAt, l.CheckedAt)
	}
}

// Checking every machine checks those connected; one whose node has no node.agents is named as unsupported, one
// offline is not tried. Checked alone, each answers why it cannot be.
func TestCheckingEveryMachineNamesTheOnesThatCannotBe(t *testing.T) {
	p := &counted{}
	old, gone := newFar(t), newFar(t)
	old.lacks = []string{node.MAgents}
	gone.set(&wire.Error{Code: wire.CodeOffline, Detail: "no route"})
	e := newEnv(t, tend.Config{Hosts: []tend.Host{{Name: "old", SSH: "old"}, {Name: "gone", SSH: "gone"}}})
	e.probe = p.probe
	e.dial = func(h tend.Host, opt wire.Options) (Conn, error) {
		if h.Name == "old" {
			return old.dial(h, opt)
		}
		return gone.dial(h, opt)
	}
	e.start()
	e.must(MMachineList, MachinesParams{Connect: true}, nil)
	var got MachineChecks
	e.must(MMachineCheck, MachineCheck{}, &got)
	if machineNamed(got.Machines, Local) == nil || machineNamed(got.Machines, Local).CheckedAt == nil {
		t.Fatalf("this machine is checked: %+v", got)
	}
	if machineNamed(got.Machines, "gone") != nil || got.Failed["gone"] != "" {
		t.Fatalf("an offline machine is not tried: %+v", got)
	}
	if got.Failed["old"] != wire.CodeUnsupported || machineNamed(got.Machines, "old") != nil {
		t.Fatalf("a node without node.agents cannot be checked: %+v", got)
	}
	if err := e.call(MMachineCheck, MachineCheck{Machine: "old"}, nil); wire.Code(err) != wire.CodeUnsupported {
		t.Fatalf("checked alone: %v", err)
	}
	if err := e.call(MMachineCheck, MachineCheck{Machine: "gone"}, nil); wire.Code(err) != wire.CodeOffline {
		t.Fatalf("an offline machine: %v", err)
	}
	if err := e.call(MMachineCheck, MachineCheck{Machine: "nowhere"}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("no such machine: %v", err)
	}
}

// Whoever sees a machine may check it; to anyone else it is not there, and checking every machine checks none.
func TestOnlyWhoSeesAMachineChecksIt(t *testing.T) {
	p := &counted{}
	e := team(t, tend.Config{})
	e.probe = p.probe
	e.start()
	e.project()
	if err := callAs(e.as(cy), MMachineCheck, "", MachineCheck{Machine: Local}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("a machine he does not see: %v", err)
	}
	var none MachineChecks
	if err := callAs(e.as(cy), MMachineCheck, "", MachineCheck{}, &none); err != nil || len(none.Machines) != 0 {
		t.Fatalf("every machine he sees is none: %+v %v", none, err)
	}
	for _, who := range []Principal{bob, dee, ann, root} {
		var got MachineChecks
		if err := callAs(e.as(who), MMachineCheck, "", MachineCheck{Machine: Local}, &got); err != nil || len(got.Machines) != 1 {
			t.Fatalf("%s sees it through the project or owns it: %+v %v", who.User, got, err)
		}
	}
}

// Checks asked while one is under way wait for it instead of probing again.
func TestChecksAskedTogetherProbeOnce(t *testing.T) {
	p := &counted{}
	e := newEnv(t, tend.Config{})
	e.probe = p.probe
	e.start()
	e.must(MMachineList, MachinesParams{Connect: true}, nil)
	before := p.of(tend.ProviderClaude)
	p.mu.Lock()
	p.hold = make(chan struct{})
	p.mu.Unlock()
	var wg sync.WaitGroup
	var bad atomic.Int32
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got MachineChecks
			if err := e.call(MMachineCheck, MachineCheck{Machine: Local}, &got); err != nil || len(got.Machines) != 1 {
				bad.Add(1)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)
	close(p.hold)
	wg.Wait()
	if bad.Load() != 0 || p.of(tend.ProviderClaude) != before+1 {
		t.Fatalf("three checks at once: %d failed, %d probes", bad.Load(), p.of(tend.ProviderClaude)-before)
	}
}
