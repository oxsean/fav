package task

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/oxsean/fav/internal/journal"
)

// Tree events.
const (
	ETaskMoved   = "task_moved"   // TaskMove
	ETaskStarted = "task_started" // TaskStart
	ETaskHeld    = "task_held"    // TaskHold
)

// MaxDepth is how deep a task tree goes: a root and two levels under it.
const MaxDepth = 3

// TaskMove puts a task under another parent or after other tasks.
type TaskMove struct {
	ID     string    `json:"id"`
	Parent *string   `json:"parent,omitempty"`
	After  *[]string `json:"after,omitempty"`
}

// TaskStart starts tasks: a backlog one becomes todo, and each is dispatched once what it comes after is done.
type TaskStart struct {
	IDs []string `json:"ids"`
}

// TaskHold: the coordinator could not dispatch a started task.
type TaskHold struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

func (s *State) applyTree(e journal.Event, seq int64, at time.Time) (bool, error) {
	switch e.Type {
	case ETaskMoved:
		var d TaskMove
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		t := s.Tasks[d.ID]
		if t == nil {
			return true, fmt.Errorf("no task %s", d.ID)
		}
		if d.Parent != nil {
			t.Parent = *d.Parent
		}
		if d.After != nil {
			t.After = *d.After
		}
		t.Held = ""
		t.Rev++
		t.UpdatedAt = at
	case ETaskStarted:
		var d TaskStart
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		for _, id := range d.IDs {
			t := s.Tasks[id]
			if t == nil {
				return true, fmt.Errorf("no task %s", id)
			}
			if t.Status == StatusBacklog {
				t.Status = StatusTodo
			}
			t.Auto, t.StartSeq, t.Held = true, seq, ""
			t.Rev++
			t.UpdatedAt = at
		}
	case ETaskHeld:
		var d TaskHold
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		t := s.Tasks[d.ID]
		if t == nil {
			return true, fmt.Errorf("no task %s", d.ID)
		}
		t.Held = d.Reason
		if d.Detail != "" {
			t.Held += ": " + d.Detail
		}
		t.UpdatedAt = at
	default:
		return false, nil
	}
	return true, nil
}

// Finished: done or canceled.
func Finished(status string) bool { return status == StatusDone || status == StatusCanceled }

// Children are id's direct subtasks, oldest first.
func (s *State) Children(id string) []*Task {
	var out []*Task
	for _, t := range s.Tasks {
		if t.Parent == id && id != "" {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID
	})
	return out
}

// Subtree is id and every task under it, parents before their children.
func (s *State) Subtree(id string) []*Task {
	t := s.Tasks[id]
	if t == nil {
		return nil
	}
	out := []*Task{t}
	for i := 0; i < len(out); i++ {
		out = append(out, s.Children(out[i].ID)...)
	}
	return out
}

// Depth is how many tasks are on the way from the root to id, id included.
func (s *State) Depth(id string) int {
	n := 0
	for t := s.Tasks[id]; t != nil && n <= len(s.Tasks); t = s.Tasks[t.Parent] {
		n++
	}
	return n
}

// Height is how many levels id's subtree has, id included.
func (s *State) Height(id string) int {
	h := 0
	for _, c := range s.Children(id) {
		h = max(h, s.Height(c.ID))
	}
	return h + 1
}

// Under: id is anc or lies under it.
func (s *State) Under(id, anc string) bool {
	for n, t := 0, s.Tasks[id]; t != nil && n <= len(s.Tasks); n, t = n+1, s.Tasks[t.Parent] {
		if t.ID == anc {
			return true
		}
	}
	return false
}

// Reaches: following After from id comes to target.
func (s *State) Reaches(id, target string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(x string) bool {
		if x == target {
			return true
		}
		if seen[x] || s.Tasks[x] == nil {
			return false
		}
		seen[x] = true
		return slices.ContainsFunc(s.Tasks[x].After, walk)
	}
	return walk(id)
}

// Situations: how an unfinished task stands. Each is exactly one of these, with a reason.
const (
	SitBacklog  = "backlog" // not started
	SitRunning  = "running" // a run of it is open
	SitQueued   = "queued"  // it will go on by itself: its run waits for a machine, or its dependencies or subtasks are not done
	SitWaiting  = "waiting" // someone has to act
	SitDone     = StatusDone
	SitCanceled = StatusCanceled
)

// Situation reasons.
const (
	WhyAfter         = "after"          // queued: tasks it comes after are not done
	WhyChildren      = "children"       // queued: its subtasks are not done
	WhySlot          = "slot"           // queued: its run waits for its machine or a slot
	WhyDir           = "dir"            // queued: another run of the same machine works in its directory
	WhyReady         = "ready"          // queued: the coordinator dispatches it next
	WhyCompleting    = "completing"     // queued: its run succeeded; the coordinator marks it done next
	WhyAccept        = "accept"         // waiting: its subtasks are done; its approver accepts it
	WhyDispatch      = "dispatch"       // waiting: not started, nothing dispatched it
	WhyAfterCanceled = "after_canceled" // waiting: a task it comes after was canceled
	WhyHeld          = "held"           // waiting: the coordinator could not dispatch it (Held says why)
	WhyEnded         = "ended"          // waiting: its run ended well; someone marks it done
)

// Situation is how a task stands and why.
type Situation struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason,omitempty"`
	Run    string `json:"run,omitempty"` // the run it is about
}

// Latest is task id's newest run, nil when it has none; a run an undo canceled before it started never counts.
func (s *State) Latest(id string) *Run {
	var out *Run
	for _, r := range s.Runs {
		if r.Task == id && !(r.State == Canceled && r.Reason == WhyUndone) && (out == nil || r.Seq > out.Seq || r.Seq == out.Seq && r.QueuedAt.After(out.QueuedAt)) {
			out = r
		}
	}
	return out
}

// Situation says how t stands.
func (s *State) Situation(t *Task) Situation {
	switch t.Status {
	case StatusBacklog:
		return Situation{Kind: SitBacklog}
	case StatusDone, StatusCanceled:
		return Situation{Kind: t.Status}
	}
	if r := s.OpenRun(t.ID); r != nil {
		switch {
		case r.State == Queued && s.dirHeld(r):
			return Situation{Kind: SitQueued, Reason: WhyDir, Run: r.ID}
		case r.State == Queued:
			return Situation{Kind: SitQueued, Reason: WhySlot, Run: r.ID}
		case r.Attention == AttentionAsked || r.Attention == AttentionPermission:
			return Situation{Kind: SitWaiting, Reason: r.Attention, Run: r.ID}
		case r.State == Unknown:
			return Situation{Kind: SitWaiting, Reason: Unknown, Run: r.ID}
		}
		return Situation{Kind: SitRunning, Reason: r.State, Run: r.ID}
	}
	if why := SourceWaits(t); why != "" {
		return Situation{Kind: SitWaiting, Reason: why}
	}
	if last := s.Latest(t.ID); last != nil && last.Stage == StageMerge && last.Seq > t.StartSeq {
		return mergeSituation(last)
	}
	if kids := s.Children(t.ID); slices.ContainsFunc(kids, func(k *Task) bool { return k.Status != StatusCanceled }) {
		if slices.ContainsFunc(kids, func(k *Task) bool { return !Finished(k.Status) }) {
			return Situation{Kind: SitQueued, Reason: WhyChildren}
		}
		if t.Flow == nil {
			return Situation{Kind: SitWaiting, Reason: WhyAccept}
		}
	} else if last := s.Latest(t.ID); last != nil && last.Stage == StagePlan && last.Seq > t.StartSeq {
		return planSituation(t, last)
	}
	if t.Flow != nil && t.Auto { // a workflow goes stage by stage once started
		if sit, ok := s.held(t); ok {
			return sit
		}
		return s.stageSituation(t)
	}
	last := s.Latest(t.ID)
	if last != nil && last.Seq > t.StartSeq { // what came of its latest try
		switch {
		case last.Waiting():
			return Situation{Kind: SitWaiting, Reason: last.Attention, Run: last.ID}
		case last.State == Exited && last.ExitCode != nil && *last.ExitCode == 0 && last.Attention == "":
			if t.Auto {
				return Situation{Kind: SitQueued, Reason: WhyCompleting, Run: last.ID}
			}
			return Situation{Kind: SitWaiting, Reason: WhyEnded, Run: last.ID}
		case last.Reason != "" && last.State != Exited:
			return Situation{Kind: SitWaiting, Reason: last.Reason, Run: last.ID}
		}
		return Situation{Kind: SitWaiting, Reason: last.State, Run: last.ID}
	}
	if !t.Auto {
		return Situation{Kind: SitWaiting, Reason: WhyDispatch}
	}
	if sit, ok := s.held(t); ok {
		return sit
	}
	return Situation{Kind: SitQueued, Reason: WhyReady}
}

// dirHeld: queued run q waits for another run of its machine working in the same directory, as the node refuses it.
// Runs on their own branch or copy (Work) never share a directory this way.
func (s *State) dirHeld(q *Run) bool {
	if q.Dir == "" || q.Work != nil {
		return false
	}
	for _, r := range s.Runs {
		if r.ID != q.ID && r.Machine == q.Machine && r.Dir == q.Dir && r.Work == nil && Open(r.State) && r.State != Queued {
			return true
		}
	}
	return false
}

// held is what keeps started task t from going on before its own work: a failed dispatch, or tasks it comes after.
func (s *State) held(t *Task) (Situation, bool) {
	if t.Held != "" {
		return Situation{Kind: SitWaiting, Reason: WhyHeld}, true
	}
	for _, a := range t.After {
		switch d := s.Tasks[a]; {
		case d == nil:
		case d.Status == StatusCanceled:
			return Situation{Kind: SitWaiting, Reason: WhyAfterCanceled}, true
		case d.Status != StatusDone:
			return Situation{Kind: SitQueued, Reason: WhyAfter}, true
		}
	}
	return Situation{}, false
}

// Ready are the started tasks the coordinator dispatches now, oldest first: new work, and stages to judge again.
func (s *State) Ready() []*Task { return s.situated(WhyReady, WhyStale) }

// Completing are the started tasks whose run succeeded, which the coordinator marks done now.
func (s *State) Completing() []*Task { return s.situated(WhyCompleting) }

func (s *State) situated(why ...string) []*Task {
	var out []*Task
	for _, t := range s.Tasks {
		if t.Status == StatusTodo && slices.Contains(why, s.Situation(t).Reason) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID
	})
	return out
}
