// Package task is the coordinator's state: tasks, their runs, the events that change them and how each event applies.
// A run's state only moves forward: a late or repeated observation never undoes an end.
package task

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/tend"
)

// Task statuses.
const (
	StatusTodo     = "todo"
	StatusDone     = "done"
	StatusCanceled = "canceled"
)

// Run states.
const (
	Queued    = "queued"
	Starting  = "starting"
	Running   = "running"
	Unknown   = "unknown"
	Exited    = "exited"
	Stopped   = "stopped"
	Failed    = "failed"
	Canceled  = "canceled"
	Abandoned = "abandoned"
)

// Open: the run still holds its task, its slot and its directory.
func Open(state string) bool {
	return state == Queued || state == Starting || state == Running || state == Unknown
}

type Task struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Brief     string    `json:"brief,omitempty"`
	Dir       string    `json:"dir,omitempty"`
	Machine   string    `json:"machine,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Status    string    `json:"status"`
	Rev       int       `json:"rev"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Run struct {
	ID        string            `json:"id"`
	Task      string            `json:"task"`
	Machine   string            `json:"machine"`
	Agent     string            `json:"agent"`
	Profile   tend.AgentProfile `json:"profile"`
	Dir       string            `json:"dir"`
	From      string            `json:"from,omitempty"`  // the machine Dir was written for; "": the run's own
	Brief     string            `json:"brief,omitempty"` // frozen at dispatch
	Title     string            `json:"title,omitempty"`
	Runner    string            `json:"runner,omitempty"` // "": the node picks
	Want      string            `json:"want"`             // run | stop
	State     string            `json:"state"`
	ExitCode  *int              `json:"exit_code,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Provider  string            `json:"provider,omitempty"`
	Session   string            `json:"session,omitempty"`
	Pane      string            `json:"pane,omitempty"`
	NodeRev   int               `json:"node_rev,omitempty"`
	QueuedAt  time.Time         `json:"queued_at"`
	StartedAt *time.Time        `json:"started_at,omitempty"`
	EndedAt   *time.Time        `json:"ended_at,omitempty"`
}

// Event types and their payloads.
const (
	ETaskCreated  = "task_created"
	ETaskEdited   = "task_edited"
	ETaskStatus   = "task_status_set"
	ERunQueued    = "run_queued"
	ERunStarting  = "run_starting"
	ERunObserved  = "run_observed"
	ERunStopAsked = "run_stop_requested"
	ERunCanceled  = "run_canceled"
	ERunAbandoned = "run_abandoned"
)

type TaskEdit struct {
	ID      string  `json:"id"`
	Title   *string `json:"title,omitempty"`
	Brief   *string `json:"brief,omitempty"`
	Dir     *string `json:"dir,omitempty"`
	Machine *string `json:"machine,omitempty"`
	Agent   *string `json:"agent,omitempty"`
}

type TaskStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type RunRef struct {
	ID string `json:"id"`
}

// RunStarting: run.start went out, for Dir as the machine names it.
type RunStarting struct {
	ID  string `json:"id"`
	Dir string `json:"dir,omitempty"`
}

// Observation is what a node reported about a run.
type Observation struct {
	ID        string     `json:"id"`
	State     string     `json:"state"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	Provider  string     `json:"provider,omitempty"`
	Session   string     `json:"session,omitempty"`
	Pane      string     `json:"pane,omitempty"`
	NodeRev   int        `json:"node_rev"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// State is everything the journal says.
type State struct {
	Seq   int64            `json:"seq"`
	Tasks map[string]*Task `json:"tasks"`
	Runs  map[string]*Run  `json:"runs"`
}

func New() *State { return &State{Tasks: map[string]*Task{}, Runs: map[string]*Run{}} }

// Apply folds one envelope in.
func (s *State) Apply(env journal.Envelope) error {
	for _, e := range env.Events {
		if err := s.apply(e, env.At); err != nil {
			return fmt.Errorf("%s: %w", e.Type, err)
		}
	}
	s.Seq = env.Seq
	return nil
}

func (s *State) apply(e journal.Event, at time.Time) error {
	switch e.Type {
	case ETaskCreated:
		var t Task
		if err := json.Unmarshal(e.Data, &t); err != nil {
			return err
		}
		t.Rev, t.CreatedAt, t.UpdatedAt = 1, at, at
		s.Tasks[t.ID] = &t
	case ETaskEdited:
		var d TaskEdit
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return err
		}
		t := s.Tasks[d.ID]
		if t == nil {
			return fmt.Errorf("no task %s", d.ID)
		}
		for _, f := range []struct {
			v   *string
			dst *string
		}{{d.Title, &t.Title}, {d.Brief, &t.Brief}, {d.Dir, &t.Dir}, {d.Machine, &t.Machine}, {d.Agent, &t.Agent}} {
			if f.v != nil {
				*f.dst = *f.v
			}
		}
		t.Rev++
		t.UpdatedAt = at
	case ETaskStatus:
		var d TaskStatus
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return err
		}
		t := s.Tasks[d.ID]
		if t == nil {
			return fmt.Errorf("no task %s", d.ID)
		}
		t.Status, t.UpdatedAt = d.Status, at
		t.Rev++
	case ERunQueued:
		var r Run
		if err := json.Unmarshal(e.Data, &r); err != nil {
			return err
		}
		r.State, r.Want, r.QueuedAt = Queued, "run", at
		s.Runs[r.ID] = &r
	case ERunStarting:
		var d RunStarting
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return err
		}
		r := s.Runs[d.ID]
		if r == nil {
			return fmt.Errorf("no run %s", d.ID)
		}
		if r.State == Queued {
			r.State = Starting
			if d.Dir != "" {
				r.Dir = d.Dir
			}
		}
	case ERunObserved:
		var o Observation
		if err := json.Unmarshal(e.Data, &o); err != nil {
			return err
		}
		r := s.Runs[o.ID]
		if r == nil {
			return fmt.Errorf("no run %s", o.ID)
		}
		r.observe(o)
	case ERunStopAsked:
		return s.run(e, func(r *Run) { r.Want = "stop" })
	case ERunCanceled:
		return s.run(e, func(r *Run) {
			if r.State == Queued {
				r.State, r.EndedAt = Canceled, &at
			}
		})
	case ERunAbandoned:
		return s.run(e, func(r *Run) {
			if Open(r.State) {
				r.State, r.Want, r.EndedAt = Abandoned, "stop", &at
			}
		})
	default:
		return fmt.Errorf("unknown event")
	}
	return nil
}

func (s *State) run(e journal.Event, f func(*Run)) error {
	var d RunRef
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return err
	}
	r := s.Runs[d.ID]
	if r == nil {
		return fmt.Errorf("no run %s", d.ID)
	}
	f(r)
	return nil
}

// rank orders states so that an observation never moves a run back.
var rank = map[string]int{Queued: 0, Starting: 1, Running: 2, Unknown: 2, Exited: 3, Stopped: 3, Failed: 3, Canceled: 3, Abandoned: 3}

func (r *Run) observe(o Observation) {
	if !Open(r.State) && r.State != Abandoned {
		return // ended runs keep their end
	}
	if o.NodeRev < r.NodeRev {
		return
	}
	if r.State == Abandoned { // the user gave up on it, but the node now knows how it ended
		if rank[o.State] < 3 {
			return
		}
	} else if rank[o.State] < rank[r.State] {
		return
	}
	r.State, r.NodeRev = o.State, o.NodeRev
	if o.ExitCode != nil {
		r.ExitCode = o.ExitCode
	}
	if o.Reason != "" {
		r.Reason = o.Reason
	}
	if o.Session != "" {
		r.Provider, r.Session = o.Provider, o.Session
	}
	if o.Pane != "" {
		r.Pane = o.Pane
	}
	if o.StartedAt != nil {
		r.StartedAt = o.StartedAt
	}
	if o.EndedAt != nil {
		r.EndedAt = o.EndedAt
	}
}

// Would: observing o changes r.
func (r *Run) Would(o Observation) bool {
	cp := *r
	cp.observe(o)
	return !reflect.DeepEqual(cp, *r)
}

// OpenRun is task id's run that has not ended, nil when there is none.
func (s *State) OpenRun(id string) *Run {
	for _, r := range s.Runs {
		if r.Task == id && Open(r.State) {
			return r
		}
	}
	return nil
}

// Running: the task has an open run (shown as running; not a stored status).
func (s *State) Running(id string) bool { return s.OpenRun(id) != nil }

// RunsOf are task id's runs, oldest first.
func (s *State) RunsOf(id string) []*Run {
	var out []*Run
	for _, r := range s.Runs {
		if r.Task == id {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QueuedAt.Before(out[j].QueuedAt) })
	return out
}

// Sorted are the tasks, newest first.
func (s *State) Sorted() []*Task {
	out := make([]*Task, 0, len(s.Tasks))
	for _, t := range s.Tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
