package task

import (
	"testing"
	"time"

	"github.com/oxsean/fav/internal/journal"
)

func apply(t *testing.T, s *State, events ...journal.Event) {
	t.Helper()
	if err := s.Apply(journal.Envelope{Seq: s.Seq + 1, At: time.Now(), Events: events}); err != nil {
		t.Fatal(err)
	}
}

func obs(state string, rev int) journal.Event {
	code := 0
	o := Observation{ID: "r_1", State: state, NodeRev: rev}
	if state == Exited {
		o.ExitCode = &code
	}
	return journal.NewEvent(ERunObserved, o)
}

func TestRunsOnlyMoveForward(t *testing.T) {
	s := New()
	apply(t, s, journal.NewEvent(ETaskCreated, Task{ID: "t_1", Title: "x", Status: StatusTodo}),
		journal.NewEvent(ERunQueued, Run{ID: "r_1", Task: "t_1", Machine: "local"}))
	if !s.Running("t_1") || s.Runs["r_1"].State != Queued {
		t.Fatal(s.Runs["r_1"])
	}
	apply(t, s, journal.NewEvent(ERunStarting, RunRef{ID: "r_1"}))
	apply(t, s, obs(Exited, 3))
	apply(t, s, obs(Running, 2)) // a late snapshot
	apply(t, s, journal.NewEvent(ERunStarting, RunRef{ID: "r_1"}))
	if r := s.Runs["r_1"]; r.State != Exited || r.ExitCode == nil || s.Running("t_1") {
		t.Fatalf("an end stays: %+v", r)
	}
}

func TestUnknownCanStillEndAndAbandonedLearnsTheEnd(t *testing.T) {
	s := New()
	apply(t, s, journal.NewEvent(ERunQueued, Run{ID: "r_1", Task: "t_1"}), journal.NewEvent(ERunStarting, RunRef{ID: "r_1"}))
	apply(t, s, obs(Running, 2), obs(Unknown, 2))
	if s.Runs["r_1"].State != Unknown {
		t.Fatal(s.Runs["r_1"].State)
	}
	apply(t, s, journal.NewEvent(ERunAbandoned, RunRef{ID: "r_1"}))
	if s.Running("t_1") {
		t.Fatal("an abandoned run frees its task")
	}
	apply(t, s, obs(Running, 3))
	if s.Runs["r_1"].State != Abandoned {
		t.Fatal("abandoned does not come back to running")
	}
	apply(t, s, obs(Exited, 4))
	if s.Runs["r_1"].State != Exited {
		t.Fatal("but it learns how it ended")
	}
}

func TestCancelOnlyTakesQueuedRuns(t *testing.T) {
	s := New()
	apply(t, s, journal.NewEvent(ERunQueued, Run{ID: "r_1", Task: "t_1"}), journal.NewEvent(ERunStarting, RunRef{ID: "r_1"}),
		journal.NewEvent(ERunCanceled, RunRef{ID: "r_1"}), journal.NewEvent(ERunStopAsked, RunRef{ID: "r_1"}))
	if r := s.Runs["r_1"]; r.State != Starting || r.Want != "stop" {
		t.Fatalf("%+v", r)
	}
	if err := s.Apply(journal.Envelope{Seq: 9, Events: []journal.Event{journal.NewEvent("nope", 1)}}); err == nil {
		t.Fatal("an unknown event")
	}
}
