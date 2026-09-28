package task

import (
	"strings"
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

func TestNeedsYouIsTheLatestRunOfOpenTasksThatWantsSomeone(t *testing.T) {
	at := func(m int) *time.Time { x := time.Date(2026, 9, 26, 10, m, 0, 0, time.UTC); return &x }
	two, zero := 2, 0
	s := New()
	add := func(id, status string) { s.Tasks[id] = &Task{ID: id, Status: status} }
	run := func(id, tk string, q int, r Run) {
		r.ID, r.Task, r.QueuedAt = id, tk, *at(q)
		s.Runs[id] = &r
	}
	add("t_wait", StatusTodo)
	run("r_old", "t_wait", 0, Run{State: Failed, EndedAt: at(1)})
	run("r_wait", "t_wait", 2, Run{State: Exited, ExitCode: &zero, Attention: AttentionAsked, EndedAt: at(5)})
	add("t_fail", StatusTodo)
	run("r_fail", "t_fail", 1, Run{State: Exited, ExitCode: &two, EndedAt: at(3)})
	add("t_ok", StatusTodo)
	run("r_ok", "t_ok", 1, Run{State: Exited, ExitCode: &zero, EndedAt: at(2)})
	add("t_done", StatusDone)
	run("r_done", "t_done", 1, Run{State: Failed, EndedAt: at(1)})
	add("t_stall", StatusTodo)
	run("r_stall", "t_stall", 1, Run{State: Running, Attention: AttentionStalled, StartedAt: at(4)})
	add("t_run", StatusTodo)
	run("r_run", "t_run", 1, Run{State: Running, StartedAt: at(1)})
	var got []string
	for _, r := range s.NeedsYou() {
		got = append(got, r.ID)
	}
	if strings.Join(got, " ") != "r_wait r_fail r_stall" {
		t.Fatal(got)
	}
}
