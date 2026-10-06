package coord

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// A running run's node says when it last put anything out, to the minute: such an observation is journaled, but nobody
// hears of it, and what waits on whom stays as it was.
func TestAnOutputTimeAloneTellsNobodyAnything(t *testing.T) {
	heard := filepath.Join(t.TempDir(), "events")
	e := team(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}, NotifyCommand: []string{os.Args[0], "_notify", heard},
		NotifyEvents: []string{NotifyWaiting, NotifyAsked, NotifyFailed, NotifyStalled, NotifyPermission, NotifyTaskWaiting, NotifyTaskDone}})
	e.dial = unreachable
	var mu sync.Mutex
	var got []Notice
	e.notice = func(n Notice) { mu.Lock(); got = append(got, n); mu.Unlock() }
	e.start()
	e.project()
	tid, rid := e.scene("running", "p1", bob.User, 1)
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	got = nil
	mu.Unlock()
	before, _ := os.ReadFile(heard)
	waiting, _ := e.c.Waiting(bob.User, tid)
	e.c.mu.Lock()
	pending := e.c.st.Pending(e.c.st.Tasks[tid])
	e.c.mu.Unlock()

	at := time.Now().Truncate(time.Minute)
	for i := range 3 {
		e.c.mu.Lock()
		r := e.c.st.Runs[rid]
		out := at.Add(time.Duration(i) * time.Minute)
		o := task.Observation{ID: rid, State: r.State, NodeRev: r.NodeRev + 1, Stream: r.Stream, Attention: r.Attention, Ask: r.Ask, Turn: r.Turn,
			Caps: r.Caps, Requests: r.Requests, OutputAt: &out}
		if !r.Would(o) {
			e.c.mu.Unlock()
			t.Fatal("a new output time is a change to journal")
		}
		err := e.c.commit(journal.System, nil, journal.NewEvent(task.ERunObserved, o))
		e.c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)

	e.c.mu.Lock()
	if r := e.c.st.Runs[rid]; r.OutputAt == nil || !r.OutputAt.Equal(at.Add(2*time.Minute)) {
		t.Errorf("the run keeps its latest output time: %v", r.OutputAt)
	}
	if now := e.c.st.Pending(e.c.st.Tasks[tid]); !reflect.DeepEqual(now, pending) {
		t.Errorf("what waits changed: %+v, was %+v", now, pending)
	}
	e.c.mu.Unlock()
	mu.Lock()
	if len(got) != 0 {
		t.Errorf("notices for an output time: %+v", got)
	}
	mu.Unlock()
	if after, _ := os.ReadFile(heard); string(after) != string(before) {
		t.Errorf("the notify command heard an output time: %s", after[len(before):])
	}
	if now, _ := e.c.Waiting(bob.User, tid); !reflect.DeepEqual(now, waiting) {
		t.Errorf("bob's inbox item changed: %+v, was %+v", now, waiting)
	}
}

func TestAnOutputTimeIsNoNotifyEvent(t *testing.T) {
	was := task.Run{State: task.Running, Attention: task.AttentionPermission}
	now := was
	at := time.Now()
	now.OutputAt = &at
	if ev := notifyEvent(&was, &now); ev != "" {
		t.Fatalf("output time alone: %q", ev)
	}
}
