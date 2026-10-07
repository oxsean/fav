package node

import (
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/proc"
)

// crashed starts a run whose supervisor exits at point, and waits until it is gone.
func crashed(t *testing.T, n *Node, point string, args ...string) Snapshot {
	t.Helper()
	t.Setenv(proc.EnvCrashAt, point)
	s := start(t, n, StartParams{Task: "t_1", Profile: fake(args...), Brief: "b"})
	t.Setenv(proc.EnvCrashAt, "")
	return wait(t, n, s.Run, func(s Snapshot) bool { return s.State.State == StateUnknown || Terminal(s.State.State) })
}

func TestASupervisorThatDiesAfterItsClaimLeavesARunNotLaunched(t *testing.T) {
	n := New(t.TempDir())
	s := crashed(t, n, "claimed")
	if s.State.State != StateFailed || s.Reason != "not_launched" {
		t.Fatalf("%+v", s)
	}
	if err := Supervise(n.runDir(s.Run)); err != nil {
		t.Fatal(err)
	}
	if got, _ := n.Snapshot(s.Run); got.Pid != 0 || got.State.State != StateFailed {
		t.Fatalf("a second supervisor starts nothing: %+v", got)
	}
}

func TestStoppingARunWhoseSupervisorDiedEndsItsAgent(t *testing.T) {
	n := New(t.TempDir())
	s := crashed(t, n, "running", "--steps", "30", "--every", "200ms")
	if s.State.State != StateUnknown || s.Pid == 0 {
		t.Fatalf("%+v", s)
	}
	if _, err := n.Stop(RunRef{Run: s.Run}); err != nil {
		t.Fatal(err)
	}
	got, _ := n.Snapshot(s.Run)
	if got.State.State != StateStopped || got.Reason != "orphan_stopped" && got.Reason != "supervisor_gone" {
		t.Fatalf("%+v", got)
	}
	for deadline := time.Now().Add(5 * time.Second); proc.Alive(s.Pid); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("agent %d still runs", s.Pid)
		}
	}
}

func TestAnAgentWhoseIdentityCannotBeProvedIsLeftAlone(t *testing.T) {
	n := New(t.TempDir())
	s := crashed(t, n, "running", "--steps", "30", "--every", "200ms")
	defer proc.KillTree(s.Pid)
	st := s.State
	st.PidStart++ // another process now has its pid
	st.Rev++
	fileio.WriteJSON(n.runDir(s.Run)+"/state.json", st)
	if !proc.Alive(s.Pid) {
		t.Skip("the agent ended with its supervisor here")
	}
	n.Stop(RunRef{Run: s.Run})
	if !proc.Alive(s.Pid) {
		t.Fatal("a process that may not be the agent is never killed")
	}
}

func TestASupervisorThatDiesBeforeItsLastWordIsSettledByAStop(t *testing.T) {
	n := New(t.TempDir())
	s := crashed(t, n, "ending", "--steps", "1", "--every", "10ms")
	if s.State.State != StateUnknown {
		t.Fatalf("%+v", s)
	}
	n.Stop(RunRef{Run: s.Run})
	if got, _ := n.Snapshot(s.Run); got.State.State != StateStopped || got.Reason != "supervisor_gone" {
		t.Fatalf("%+v", got)
	}
}

func TestARunWhoseAgentIsNotKnownStaysUnknownAfterAStop(t *testing.T) {
	n := New(t.TempDir())
	s := crashed(t, n, "started", "--steps", "1", "--every", "10ms")
	n.Stop(RunRef{Run: s.Run})
	if got, _ := n.Snapshot(s.Run); got.State.State != StateUnknown {
		t.Fatalf("without the agent's pid nothing proves it ended: %+v", got)
	}
}
