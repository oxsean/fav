package coord

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// PushAffordances carries what changed in the viewer's affordances: an entry that is null is gone.
const PushAffordances = "affordances"

// Affordances are what the viewer may do now: each run's actions and each task's, with where its messages go. Runs and
// tasks with nothing to do are left out.
type Affordances struct {
	Runs  map[string][]string        `json:"runs"`
	Tasks map[string]*TaskAffordance `json:"tasks"`
}

type TaskAffordance struct {
	Actions []string   `json:"actions"`
	Route   task.Route `json:"route"`
}

// dry is do's word on what p asks with params, nothing committed; the caller holds mu.
func dry(p Principal, do func(Principal, *wire.Request) (string, []journal.Event, error), params any) bool {
	b, _ := json.Marshal(params)
	_, _, err := do(p, &wire.Request{Params: b})
	return err == nil
}

// runAllows: p may do act with run r, as the method behind it says; the caller holds mu.
func (c *Coord) runAllows(p Principal, r *task.Run, act string) bool {
	switch act {
	case task.ActSteer:
		return dry(p, c.runSend, SendMessage{Run: r.ID, Text: "x"})
	case task.ActAfter:
		return dry(p, c.runSend, SendMessage{Run: r.ID, Text: "x", Mode: agent.SendAfter})
	case task.ActInterrupt:
		return dry(p, c.runInterrupt, Interrupt{Run: r.ID})
	case task.ActAnswer:
		return slices.ContainsFunc(r.Unanswered(), func(q agent.Request) bool { return dry(p, c.runAnswer, answerFor(r, q, "")) })
	case task.ActAllowRun:
		return slices.ContainsFunc(r.Unanswered(), func(q agent.Request) bool {
			return q.AllowRun && dry(p, c.runAnswer, answerFor(r, q, agent.DecisionAllowRun))
		})
	case task.ActStop:
		return dry(p, c.runStop, task.RunRef{ID: r.ID})
	case task.ActAbandon:
		return dry(p, c.runAbandon, task.RunRef{ID: r.ID})
	case task.ActContinue:
		return dry(p, c.runContinue, Continue{Run: r.ID, Text: "x"})
	case task.ActTakeover:
		return canRead(c.st, p, runTask(c.st, r)) && c.ownerOf(r.Machine) == p.User
	}
	return false
}

// answerFor is an answer to request q of run r that its method takes: every question answered, a permission decided.
func answerFor(r *task.Run, q agent.Request, decision string) Answer {
	a := agent.Answer{Request: q.ID, Allow: true, Decision: decision}
	if q.Kind == agent.RequestQuestion {
		a.Answers = map[string]string{}
		for _, x := range q.Questions {
			a.Answers[x.Question] = "x"
		}
	}
	return Answer{Run: r.ID, Answer: a}
}

// taskAllows: p may do act with task t, as the method behind it says; the caller holds mu.
func (c *Coord) taskAllows(p Principal, t *task.Task, act string) bool {
	status := func(s string) bool { return dry(p, c.taskStatus, task.TaskStatus{ID: t.ID, Status: s}) }
	switch act {
	case task.ActDispatch:
		return dry(p, c.runDispatch, Dispatch{Task: t.ID})
	case task.ActStart:
		return dry(p, c.taskStart, TaskRef{ID: t.ID})
	case task.ActStop:
		open := c.st.OpenRun(t.ID)
		return open != nil && dry(p, c.runStop, task.RunRef{ID: open.ID})
	case task.ActPass:
		return dry(p, c.taskGate, TaskGate{ID: t.ID, Pass: true})
	case task.ActRework:
		return dry(p, c.taskGate, TaskGate{ID: t.ID})
	case task.ActAck:
		return dry(p, c.taskSourceAck, task.SourceAck{ID: t.ID, Accept: true})
	case task.ActKeep:
		return dry(p, c.taskSourceAck, task.SourceAck{ID: t.ID})
	case task.ActMerge:
		return dry(p, c.taskMerge, TaskRef{ID: t.ID})
	case task.ActDone:
		return status(task.StatusDone)
	case task.ActBacklog:
		return status(task.StatusBacklog)
	case task.ActCancel:
		return status(task.StatusCanceled)
	case task.ActReopen:
		return status(task.StatusTodo)
	case task.ActPlan:
		return dry(p, c.taskPlan, TaskPlan{ID: t.ID})
	case task.ActReview:
		return dry(p, c.planSave, PlanSave{ID: t.ID, Plan: t.Draft.Plan})
	case task.ActEdit:
		return dry(p, c.taskEdit, task.TaskEdit{ID: t.ID, Title: &t.Title})
	case task.ActMove:
		return dry(p, c.taskMove, task.TaskMove{ID: t.ID})
	case task.ActChild:
		return dry(p, c.taskCreate, TaskCreate{Title: "x", Parent: t.ID})
	case task.ActMessage:
		return dry(p, c.taskMessage, TaskMessage{ID: t.ID, Text: "x"})
	}
	return false
}

// runAffordance is what p may do with run r; nil when nothing. The caller holds mu.
func (c *Coord) runAffordance(p Principal, r *task.Run) []string {
	var out []string
	for _, a := range c.st.RunActions(r) {
		if c.runAllows(p, r, a) {
			out = append(out, a)
		}
	}
	return out
}

// taskAffordance is what p may do with task t; nil when nothing. The caller holds mu.
func (c *Coord) taskAffordance(p Principal, t *task.Task) *TaskAffordance {
	var out []string
	for _, a := range c.st.TaskActions(t) {
		if c.taskAllows(p, t, a) {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &TaskAffordance{Actions: out, Route: c.st.Route(t)}
}

// affordKeys are the entries a recount of tasks ids (and their runs) covers; nil ids is everything p may see. The
// caller holds mu.
func (c *Coord) affordKeys(p Principal, ids []string) (tasks, runs []string) {
	seen := map[string]bool{}
	if ids == nil {
		for id, t := range c.st.Tasks {
			if canRead(c.st, p, t) {
				tasks, seen[id] = append(tasks, id), true
			}
		}
	} else {
		for _, id := range ids {
			if canRead(c.st, p, c.st.Tasks[id]) {
				tasks, seen[id] = append(tasks, id), true
			}
		}
	}
	for id, r := range c.st.Runs {
		if seen[r.Task] {
			runs = append(runs, id)
		}
	}
	return tasks, runs
}

// affordances is p's affordances of tasks ids and their runs (nil: all they may see), as JSON by key ("t:" / "r:" and
// the id; "" nothing to do). The caller holds mu.
func (c *Coord) affordances(p Principal, ids []string) map[string]string {
	tasks, runs := c.affordKeys(p, ids)
	out := make(map[string]string, len(tasks)+len(runs))
	for _, id := range tasks {
		out["t:"+id] = ""
		if a := c.taskAffordance(p, c.st.Tasks[id]); a != nil {
			b, _ := json.Marshal(a)
			out["t:"+id] = string(b)
		}
	}
	for _, id := range runs {
		out["r:"+id] = ""
		if a := c.runAffordance(p, c.st.Runs[id]); len(a) > 0 {
			b, _ := json.Marshal(a)
			out["r:"+id] = string(b)
		}
	}
	return out
}

// affordDiff is the push of what in now differs from what sent says was sent, and records it as sent; ok false when
// nothing does. Entries now does not cover stay as they are.
func affordDiff(sent, now map[string]string) (push map[string]map[string]json.RawMessage, ok bool) {
	push = map[string]map[string]json.RawMessage{"runs": {}, "tasks": {}}
	for _, k := range slices.Sorted(maps.Keys(now)) {
		v := now[k]
		if old, seen := sent[k]; seen && old == v {
			continue
		}
		sent[k] = v
		table := map[byte]string{'t': "tasks", 'r': "runs"}[k[0]]
		raw := json.RawMessage("null")
		if v != "" {
			raw = json.RawMessage(v)
		}
		push[table][k[2:]] = raw
		ok = true
	}
	return push, ok
}

// Reaffirm has every state stream count its viewer's affordances again: what they rest on changed outside the journal
// (who owns a machine, who is disabled, what a node can do).
func (c *Coord) Reaffirm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, sb := range c.subs {
		select {
		case sb.again <- struct{}{}:
		default:
		}
	}
}
