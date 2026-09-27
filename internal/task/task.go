// Package task is the coordinator's state: tasks, their runs, the events that change them and how each event applies.
// A run's state only moves forward: a late or repeated observation never undoes an end.
package task

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/tend"
)

// Task statuses.
const (
	StatusBacklog  = "backlog" // written down, not started: nothing dispatches it
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

// Attentions: why a run wants someone to look.
const (
	AttentionAsked      = "asked"
	AttentionPermission = "permission"
	AttentionStalled    = "stalled"
)

// Waiting: the run ended on a question or a denied permission; a reply continues its session.
func (r *Run) Waiting() bool {
	return !Open(r.State) && (r.Attention == AttentionAsked || r.Attention == AttentionPermission)
}

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
	Project   string    `json:"project,omitempty"`
	Owner     string    `json:"owner,omitempty"`    // who answers for it (its creator unless handed on); a task outside any project is theirs
	Approver  string    `json:"approver,omitempty"` // who accepts its work; "": its owner
	Parent    string    `json:"parent,omitempty"`
	After     []string  `json:"after,omitempty"` // the tasks that must be done before it starts
	Kind      string    `json:"kind,omitempty"`  // requirement | "" (work)
	Accept    []string  `json:"acceptance,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	Status    string    `json:"status"`
	Auto      bool      `json:"auto,omitempty"`      // started: the coordinator dispatches it once what it comes after is done
	StartSeq  int64     `json:"start_seq,omitempty"` // the seq of its last start; runs queued before it are earlier tries
	Held      string    `json:"held,omitempty"`      // why the coordinator could not dispatch it; cleared by an edit or a start
	Rev       int       `json:"rev,omitzero"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

type Run struct {
	ID         string            `json:"id"`
	Task       string            `json:"task"`
	Machine    string            `json:"machine"`
	Agent      string            `json:"agent"`
	Profile    tend.AgentProfile `json:"profile"`
	Dir        string            `json:"dir"`
	From       string            `json:"from,omitempty"`  // the machine Dir was written for; "": the run's own
	Brief      string            `json:"brief,omitempty"` // frozen at dispatch
	Title      string            `json:"title,omitempty"`
	Runner     string            `json:"runner,omitempty"`     // "": the node picks
	Resume     string            `json:"resume,omitempty"`     // the session this run continues (Brief is the reply)
	Parent     string            `json:"parent,omitempty"`     // the run it answers
	Project    string            `json:"project,omitempty"`    // its task's when it was queued
	Dispatcher string            `json:"dispatcher,omitempty"` // the user who queued it
	Want       string            `json:"want,omitempty"`       // run | stop
	Seq        int64             `json:"seq,omitempty"`        // the seq that queued it
	State      string            `json:"state,omitempty"`
	ExitCode   *int              `json:"exit_code,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	Detail     string            `json:"detail,omitempty"`
	Attention  string            `json:"attention,omitempty"` // asked | permission | stalled
	Ask        string            `json:"ask,omitempty"`
	Note       string            `json:"note,omitempty"`
	Last       string            `json:"last,omitempty"`     // the newest thing its agent said
	Usage      *agent.Usage      `json:"usage,omitempty"`    // what its agent spent
	Stream     bool              `json:"stream,omitempty"`   // it takes answers and messages while it runs
	Requests   []agent.Request   `json:"requests,omitempty"` // what it waits on, as its node last said
	Answers    []agent.Answer    `json:"answers,omitempty"`  // given, not yet taken by the node
	Sends      []agent.Send      `json:"sends,omitempty"`    // messages for it and how far they got
	Provider   string            `json:"provider,omitempty"`
	Session    string            `json:"session,omitempty"`
	Pane       string            `json:"pane,omitempty"`
	NodeRev    int               `json:"node_rev,omitempty"`
	QueuedAt   time.Time         `json:"queued_at,omitzero"`
	StartedAt  *time.Time        `json:"started_at,omitzero"`
	EndedAt    *time.Time        `json:"ended_at,omitzero"`
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
	ERunAnswered  = "run_answered"
	ERunSent      = "run_sent"
)

// RunAnswer: the user answered a request of run ID.
type RunAnswer struct {
	ID     string       `json:"id"`
	Answer agent.Answer `json:"answer"`
}

// RunSend: the user sent run ID a message.
type RunSend struct {
	ID   string     `json:"id"`
	Send agent.Send `json:"send"`
}

type TaskEdit struct {
	ID       string    `json:"id"`
	Title    *string   `json:"title,omitempty"`
	Brief    *string   `json:"brief,omitempty"`
	Dir      *string   `json:"dir,omitempty"`
	Machine  *string   `json:"machine,omitempty"`
	Agent    *string   `json:"agent,omitempty"`
	Project  *string   `json:"project,omitempty"`
	Owner    *string   `json:"owner,omitempty"`
	Approver *string   `json:"approver,omitempty"`
	Kind     *string   `json:"kind,omitempty"`
	Accept   *[]string `json:"acceptance,omitempty"`
	Tags     *[]string `json:"tags,omitempty"`
}

type TaskStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type RunRef struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"` // run_canceled: why, when not the user
}

// RunStarting: run.start went out, for Dir as the machine names it.
type RunStarting struct {
	ID  string `json:"id"`
	Dir string `json:"dir,omitempty"`
}

// Observation is what a node reported about a run.
type Observation struct {
	ID        string          `json:"id"`
	State     string          `json:"state"`
	ExitCode  *int            `json:"exit_code,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Detail    string          `json:"detail,omitempty"`
	Attention string          `json:"attention,omitempty"`
	Ask       string          `json:"ask,omitempty"`
	Note      string          `json:"note,omitempty"`
	Last      string          `json:"last,omitempty"`
	Usage     *agent.Usage    `json:"usage,omitempty"`
	Stream    bool            `json:"stream,omitempty"`
	Requests  []agent.Request `json:"requests,omitempty"`
	Sends     []agent.Send    `json:"sends,omitempty"`
	Provider  string          `json:"provider,omitempty"`
	Session   string          `json:"session,omitempty"`
	Pane      string          `json:"pane,omitempty"`
	NodeRev   int             `json:"node_rev,omitzero"`
	StartedAt *time.Time      `json:"started_at,omitzero"`
	EndedAt   *time.Time      `json:"ended_at,omitzero"`
}

// State is everything the journal says.
type State struct {
	Seq       int64                `json:"seq"`
	Tasks     map[string]*Task     `json:"tasks"`
	Runs      map[string]*Run      `json:"runs"`
	Projects  map[string]*Project  `json:"projects"`
	Shares    map[string]*Share    `json:"shares"`               // by machine
	AgentDefs map[string]*AgentDef `json:"agent_defs,omitempty"` // mode 2; mode 1 keeps them in files
}

func New() *State {
	return &State{Tasks: map[string]*Task{}, Runs: map[string]*Run{}, Projects: map[string]*Project{}, Shares: map[string]*Share{},
		AgentDefs: map[string]*AgentDef{}}
}

// Apply folds one envelope in.
func (s *State) Apply(env journal.Envelope) error {
	if s.Projects == nil {
		s.Projects = map[string]*Project{}
	}
	if s.Shares == nil {
		s.Shares = map[string]*Share{}
	}
	if s.AgentDefs == nil {
		s.AgentDefs = map[string]*AgentDef{}
	}
	for _, e := range env.Events {
		if err := s.apply(e, env.Seq, env.At); err != nil {
			return fmt.Errorf("%s: %w", e.Type, err)
		}
	}
	s.Seq = env.Seq
	return nil
}

func (s *State) apply(e journal.Event, seq int64, at time.Time) error {
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
		}{{d.Title, &t.Title}, {d.Brief, &t.Brief}, {d.Dir, &t.Dir}, {d.Machine, &t.Machine}, {d.Agent, &t.Agent},
			{d.Project, &t.Project}, {d.Owner, &t.Owner}, {d.Approver, &t.Approver}, {d.Kind, &t.Kind}} {
			if f.v != nil {
				*f.dst = *f.v
			}
		}
		if d.Accept != nil {
			t.Accept = *d.Accept
		}
		if d.Tags != nil {
			t.Tags = *d.Tags
		}
		t.Held = ""
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
		r.State, r.Want, r.QueuedAt, r.Seq = Queued, "run", at, seq
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
		var d RunRef
		json.Unmarshal(e.Data, &d)
		return s.run(e, func(r *Run) {
			if r.State == Queued {
				r.State, r.EndedAt = Canceled, &at
				if d.Reason != "" {
					r.Reason = d.Reason
				}
			}
		})
	case ERunAbandoned:
		return s.run(e, func(r *Run) {
			if Open(r.State) {
				r.State, r.Want, r.EndedAt = Abandoned, "stop", &at
			}
		})
	case ERunAnswered:
		var d RunAnswer
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return err
		}
		return s.run(e, func(r *Run) {
			if !slices.ContainsFunc(r.Answers, func(a agent.Answer) bool { return a.Request == d.Answer.Request }) {
				r.Answers = append(r.Answers, d.Answer)
			}
		})
	case ERunSent:
		var d RunSend
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return err
		}
		return s.run(e, func(r *Run) {
			if !slices.ContainsFunc(r.Sends, func(m agent.Send) bool { return m.ID == d.Send.ID }) {
				r.Sends = append(r.Sends, d.Send)
			}
		})
	default:
		if ok, err := s.applyTree(e, seq, at); ok {
			return err
		}
		if ok, err := s.applyTeam(e, at); ok {
			return err
		}
		if ok, err := s.applyDefs(e, at); ok {
			return err
		}
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
	if o.Detail != "" {
		r.Detail = o.Detail
	}
	if o.NodeRev > 0 { // what the node says now; the coordinator's own observations carry none of it
		r.Attention, r.Ask, r.Note, r.Last, r.Usage = o.Attention, o.Ask, o.Note, o.Last, o.Usage
		r.Stream, r.Requests = o.Stream, o.Requests
		r.Sends = mergeSends(r.Sends, o.Sends)
		r.Answers = slices.DeleteFunc(slices.Clone(r.Answers), func(a agent.Answer) bool { // taken, or no longer asked
			return !slices.ContainsFunc(r.Requests, func(q agent.Request) bool { return q.ID == a.Request })
		})
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
	if !Open(r.State) { // nothing waits any more, and what never went out will not
		r.Requests, r.Answers = nil, nil
		r.Sends = slices.Clone(r.Sends)
		for i := range r.Sends {
			if r.Sends[i].State == agent.SendQueued {
				r.Sends[i].State = agent.SendFailed
			}
		}
	}
}

// mergeSends is ours with the node's word on each message; the node's order comes first.
func mergeSends(ours, node []agent.Send) []agent.Send {
	out := slices.Clone(node)
	for _, m := range ours {
		if !slices.ContainsFunc(out, func(x agent.Send) bool { return x.ID == m.ID }) {
			out = append(out, m)
		}
	}
	return out
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

// NeedsYou: r waits for an answer or a permission, asked something, went quiet, failed, or its end is unknown.
func (r *Run) NeedsYou() bool {
	switch {
	case r.Waiting(), r.State == Failed, r.State == Unknown:
		return true
	case r.State == Exited:
		return r.ExitCode != nil && *r.ExitCode != 0
	case Open(r.State):
		return r.Attention == AttentionAsked || r.Attention == AttentionStalled || r.Attention == AttentionPermission
	}
	return false
}

// Since is when r started to need someone, as far as the state tells: its end, else its start.
func (r *Run) Since() time.Time {
	switch {
	case r.EndedAt != nil:
		return *r.EndedAt
	case r.StartedAt != nil:
		return *r.StartedAt
	}
	return r.QueuedAt
}

// NeedsYou are the latest runs of the open tasks that need someone, longest waiting first.
func (s *State) NeedsYou() []*Run {
	var out []*Run
	for _, t := range s.Tasks {
		if t.Status != StatusTodo {
			continue
		}
		if runs := s.RunsOf(t.ID); len(runs) > 0 && runs[len(runs)-1].NeedsYou() {
			out = append(out, runs[len(runs)-1])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since().Before(out[j].Since()) })
	return out
}
