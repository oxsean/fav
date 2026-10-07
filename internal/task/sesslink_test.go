package task

import (
	"testing"
	"time"
)

func TestSessionLinks(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st := &State{
		Tasks: map[string]*Task{"t1": {ID: "t1"}, "t2": {ID: "t2"}},
		Runs: map[string]*Run{
			"a": {Task: "t1", Session: "s1", Seq: 1},
			"b": {Task: "t2", Session: "s1", Seq: 2},
			"c": {Task: "t1", Session: "s2", Seq: 1, QueuedAt: t0},
			"d": {Task: "t2", Resume: "s2", Seq: 1, QueuedAt: t0.Add(time.Hour)},
			"g": {Task: "gone", Session: "s4", Seq: 9},
			"h": {Task: "t1", Seq: 5},
			"i": {Task: "t1", Session: "s5", Resume: "s6", Seq: 1},
		},
	}
	for _, c := range []struct{ name, session, want string }{
		{"a higher Seq wins", "s1", "t2"},
		{"same Seq: later QueuedAt, a queued resume counts", "s2", "t2"},
		{"a run of a missing task counts for nothing", "s4", ""},
		{"a run without a session counts for nothing", "", ""},
		{"Session wins over Resume", "s5", "t1"},
		{"Resume is not used when Session is set", "s6", ""},
	} {
		got := ""
		if x := SessionTasks(st)[c.session]; x != nil {
			got = x.ID
		}
		if got != c.want {
			t.Errorf("%s: SessionTasks[%q] = %q, want %q", c.name, c.session, got, c.want)
		}
	}
}

func TestNewestRuns(t *testing.T) {
	n := Newest[string]{}
	n.Add("k", &Run{Task: "old", Seq: 1})
	n.Add("k", &Run{Task: "new", Seq: 2})
	n.Add("k", &Run{Task: "older", Seq: 1})
	if n["k"].Task != "new" {
		t.Errorf("got %q", n["k"].Task)
	}
}
