package task

import (
	"slices"
	"sort"

	"github.com/oxsean/fav/internal/agent"
)

// Conversation is the runs of the conversation run id is in: its root (the run no parent of which the state holds) and
// every run that goes on from it, by seq, then queue time, then id; nil when the state has no run id.
func (s *State) Conversation(id string) []*Run {
	r := s.Runs[id]
	if r == nil {
		return nil
	}
	root := s.rootOf(r)
	var out []*Run
	for _, x := range s.Runs {
		if x.Task == r.Task && s.rootOf(x) == root {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Seq != b.Seq:
			return a.Seq < b.Seq
		case !a.QueuedAt.Equal(b.QueuedAt):
			return a.QueuedAt.Before(b.QueuedAt)
		}
		return a.ID < b.ID
	})
	return out
}

func (s *State) rootOf(r *Run) string {
	seen := map[string]bool{}
	for r.Parent != "" && s.Runs[r.Parent] != nil && !seen[r.ID] {
		seen[r.ID] = true
		r = s.Runs[r.Parent]
	}
	return r.ID
}

// CapsNow is what r can do: as its node found it once it started, else as planned from its provider and runner (a
// node that reports no caps streams when it says Stream).
func (r *Run) CapsNow() agent.RunCaps {
	if r.Caps != nil {
		return *r.Caps
	}
	var pc agent.Caps
	if p, ok := agent.Get(r.Profile.Provider); ok {
		pc = p.Caps()
	}
	herdr := r.Runner == "herdr" || r.Pane != ""
	st := r.Stream
	if !Open(r.State) || r.State == Queued || r.State == Starting {
		st = pc.Stream && !herdr
	}
	return agent.RunCaps{Steer: st, After: st && pc.Continue, Interrupt: st, Questions: st, Continue: pc.Continue && !herdr, Takeover: pc.Resume}
}

// Run actions: what can be done with a run, before who may do it is known.
const (
	ActSteer     = "steer"     // run.send into the running turn
	ActAfter     = "after"     // run.send to go on from once the turn ended
	ActInterrupt = "interrupt" // run.interrupt of the turn it is at
	ActAnswer    = "answer"    // run.answer to one of its requests
	ActAllowRun  = "allow_run" // run.answer with DecisionAllowRun
	ActStop      = "stop"      // run.stop
	ActAbandon   = "abandon"   // run.abandon
	ActContinue  = "continue"  // run.continue in its session
	ActTakeover  = "takeover"  // its session resumed in a terminal; no method: the machine's owner copies the command
)

// Task actions, as the web client names them.
const (
	ActDispatch = "dispatch" // run.dispatch
	ActStart    = "start"    // task.start
	ActPass     = "pass"     // task.gate passing its human gate
	ActRework   = "rework"   // task.gate sending it back
	ActAck      = "ack"      // task.source_ack taking its issue's new revision
	ActKeep     = "keep"     // task.source_ack keeping this one
	ActMerge    = "merge"    // task.merge
	ActDone     = "done"     // task.set_status done
	ActBacklog  = "backlog"  // task.set_status backlog
	ActCancel   = "cancel"   // task.set_status canceled
	ActReopen   = "reopen"   // task.set_status todo
	ActPlan     = "plan"     // task.plan
	ActReview   = "review"   // task.plan_save / task.plan_apply of its draft
	ActEdit     = "edit"     // task.edit
	ActMove     = "move"     // task.move
	ActChild    = "child"    // task.create under it
	ActMessage  = "message"  // task.message where Route says
)

// Open requests of r that no answer is waiting on: those not answered, and those whose answer did not reach the agent.
func (r *Run) Unanswered() []agent.Request {
	if !Open(r.State) {
		return nil
	}
	var out []agent.Request
	for _, q := range r.Requests {
		if q.Failed || !slices.ContainsFunc(r.Answers, func(a agent.Answer) bool { return a.Request == q.ID }) {
			out = append(out, q)
		}
	}
	return out
}

// RunActions are what the state lets be done with r now, in a fixed order.
func (s *State) RunActions(r *Run) []string {
	caps := r.CapsNow()
	var out []string
	add := func(ok bool, a string) {
		if ok {
			out = append(out, a)
		}
	}
	add(r.State == Running && r.Stream && caps.Steer, ActSteer)
	add(r.State == Running && caps.After, ActAfter)
	add(r.State == Running && caps.Interrupt && r.Turn > 0, ActInterrupt)
	qs := r.Unanswered()
	add(len(qs) > 0, ActAnswer)
	add(caps.AnswerScope && slices.ContainsFunc(qs, func(q agent.Request) bool { return q.Kind == agent.RequestPermission && q.AllowRun }), ActAllowRun)
	add(Open(r.State) && (r.State == Queued || r.Want != "stop"), ActStop)
	add(r.State == Starting || r.State == Running || r.State == Unknown, ActAbandon)
	ended := !Open(r.State) && r.Session != ""
	add(ended && caps.Continue && s.OpenRun(r.Task) == nil, ActContinue)
	add(ended && caps.Takeover, ActTakeover)
	return out
}

// TaskActions are what the state lets be done with t now, in a fixed order.
func (s *State) TaskActions(t *Task) []string {
	var out []string
	add := func(ok bool, a string) {
		if ok {
			out = append(out, a)
		}
	}
	open := s.OpenRun(t.ID)
	kids := s.Children(t.ID)
	unfinished := slices.ContainsFunc(kids, func(k *Task) bool { return !Finished(k.Status) })
	finished := Finished(t.Status)
	gate := !finished && t.Flow.StageOf(t.Stage) != nil && t.Flow.StageOf(t.Stage).Gate == GateHuman
	add(t.Status == StatusTodo && open == nil && !unfinished, ActDispatch)
	add(!finished && (t.Status == StatusBacklog || !t.Auto), ActStart)
	add(open != nil && (open.State == Queued || open.Want != "stop"), ActStop)
	add(gate, ActPass)
	add(gate, ActRework)
	add(SourceWaits(t) == WhySourceChanged, ActAck)
	add(SourceWaits(t) != "", ActKeep)
	add(t.Status == StatusTodo && s.NeedsMerge(t) && open == nil, ActMerge)
	add(t.Status == StatusTodo && t.Flow == nil && SourceWaits(t) != WhySourceChanged && !(open != nil && s.NeedsMerge(t)), ActDone)
	add(t.Status == StatusTodo, ActBacklog)
	add(!finished, ActCancel)
	add(finished, ActReopen)
	add((t.Status == StatusTodo || t.Status == StatusBacklog) && open == nil && len(kids) == 0, ActPlan)
	add(!finished && t.Draft != nil && t.Draft.Plan != nil, ActReview)
	add(true, ActEdit)
	add(true, ActMove)
	add(!finished, ActChild)
	add(s.Route(t).To != RouteNone, ActMessage)
	return out
}

// Where a task's message goes (Route.To).
const (
	RouteRun     = "run"     // its running run takes it: into the turn (steer) or after it
	RouteReply   = "reply"   // its stage's last run ended with a session: a new run continues it
	RouteWorkpad = "workpad" // its workflow is between stages: the next stage's brief takes it
	RouteNone    = "none"    // nowhere: Why says why
)

// Why a task's message goes nowhere (Route.Why).
const (
	WhyFinished  = "finished"   // the task is done or canceled
	WhyStarting  = "starting"   // its run has not started yet
	WhyBusy      = "busy"       // its run runs and takes no messages
	WhyNoSession = "no_session" // its last run left no session to continue
)

// Route is where a message for a task goes now; Version is the seq it holds since: the run's that takes it, or the
// stage's.
type Route struct {
	To      string `json:"to"`
	Run     string `json:"run,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Version int64  `json:"version,omitempty"`
	Why     string `json:"why,omitempty"`
}

// Route is where a message for t goes, by the first rule that holds: a running run (into its turn when it can steer,
// else after it), the last run of its stage when it ended with a session it can continue in, its workpad between
// stages, else nowhere.
func (s *State) Route(t *Task) Route {
	if Finished(t.Status) {
		return Route{To: RouteNone, Why: WhyFinished}
	}
	if open := s.OpenRun(t.ID); open != nil {
		caps := open.CapsNow()
		if open.State == Running && (caps.Steer && open.Stream || caps.After) {
			return Route{To: RouteRun, Run: open.ID, Stage: open.Stage, Version: open.Seq}
		}
		if open.State == Running {
			return Route{To: RouteNone, Run: open.ID, Why: WhyBusy}
		}
		return Route{To: RouteNone, Run: open.ID, Why: WhyStarting}
	}
	last := s.lastOfStage(t)
	if last != nil && last.Session != "" && last.CapsNow().Continue {
		return Route{To: RouteReply, Run: last.ID, Stage: last.Stage, Version: last.Seq}
	}
	if t.Flow != nil {
		return Route{To: RouteWorkpad, Stage: t.Stage, Version: t.StageSeq}
	}
	return Route{To: RouteNone, Why: WhyNoSession}
}

// lastOfStage is t's latest run, of its current stage when it has a workflow (a merge is no stage's); nil when none.
func (s *State) lastOfStage(t *Task) *Run {
	var last *Run
	for _, r := range s.Runs {
		if r.Task != t.ID || r.Stage == StageMerge || t.Flow != nil && (r.Stage != t.Stage || r.Seq <= t.StageSeq) {
			continue
		}
		if last == nil || r.Seq > last.Seq || r.Seq == last.Seq && r.QueuedAt.After(last.QueuedAt) {
			last = r
		}
	}
	return last
}

// Pending kinds: what someone is to do about a task.
const (
	PendPermission = "permission" // a running agent asks to use a tool
	PendQuestion   = "question"   // a running agent asks questions
	PendGate       = "gate"       // its work is to be accepted
	PendContinue   = "continue"   // its run ended on a question or a denied tool: a reply continues it
	PendEnded      = "ended"      // its run ended well: someone marks it done
	PendFailed     = "failed"     // its run failed, stopped or went unknown
	PendWaiting    = "waiting"    // anything else it waits on someone for: Reason says what
)

// Pending is one thing someone is to do about a task. ID is <run>/<request> for a request, <task>/<kind> otherwise;
// Version is the seq it holds since: the run's that asks, or the stage's for a gate. An action on it that names
// another version is about something gone.
type Pending struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason,omitempty"`
	Task    string `json:"task"`
	Run     string `json:"run,omitempty"`
	Request string `json:"request,omitempty"`
	Version int64  `json:"version,omitempty"`
}

// whyOfWork are the waiting reasons that are about the work, not about how its run ended.
var whyOfWork = []string{WhyMergeConflict, WhyMaxLoops, WhyBlocked, WhyBudget, WhyDraft, WhyNoPlan}

// Pending is what waits on someone about t: each unanswered request of its open run, else the one thing its situation
// waits for; nothing while it merely waits for a dispatch nobody asked for.
func (s *State) Pending(t *Task) []Pending {
	sit := s.Situation(t)
	r := s.Runs[sit.Run]
	if r != nil && Open(r.State) {
		var out []Pending
		for _, q := range r.Unanswered() {
			kind := PendPermission
			if q.Kind == agent.RequestQuestion {
				kind = PendQuestion
			}
			out = append(out, Pending{ID: r.ID + "/" + q.ID, Kind: kind, Task: t.ID, Run: r.ID, Request: q.ID, Version: r.Seq})
		}
		if len(out) > 0 || sit.Kind != SitWaiting {
			return out
		}
	}
	if sit.Kind != SitWaiting || sit.Reason == WhyDispatch {
		return nil
	}
	p := Pending{Kind: PendWaiting, Reason: sit.Reason, Task: t.ID}
	switch {
	case sit.Reason == WhyAccept:
		p.Kind, p.Reason, p.Version = PendGate, "", t.StageSeq
	case r == nil:
	case Open(r.State) && (sit.Reason == AttentionAsked || sit.Reason == AttentionPermission):
		p.Kind = PendQuestion
		if sit.Reason == AttentionPermission {
			p.Kind = PendPermission
		}
		p.Reason = ""
	case sit.Reason == AttentionAsked || sit.Reason == AttentionPermission:
		p.Kind = PendContinue
	case sit.Reason == WhyEnded:
		p.Kind, p.Reason = PendEnded, ""
	case !slices.Contains(whyOfWork, sit.Reason):
		p.Kind = PendFailed
	}
	if r != nil {
		p.Run, p.Version = r.ID, r.Seq
	}
	p.ID = t.ID + "/" + p.Kind
	return []Pending{p}
}
