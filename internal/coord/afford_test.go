package coord

import (
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func unreachable(tend.Host, wire.Options) (Conn, error) {
	return nil, &wire.Error{Code: wire.CodeOffline, Detail: "no route"}
}

// stage is an env whose runs sit on machines far (shared with p1) and solo (shared with nobody), which never connect:
// what the test commits about them stays as it is.
func stage(t *testing.T, teamMode bool) *env {
	cfg := tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}, {Name: "solo", SSH: "solo"}}}
	var e *env
	if teamMode {
		e = team(t, cfg)
	} else {
		e = newEnv(t, cfg)
	}
	e.dial = unreachable
	e.start()
	if teamMode {
		e.project()
		if err := callAs(e.as(ann), MMachineShare, "s-solo-none", task.Share{Machine: "solo"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// scenes are the states the actions rest on, each made anew by the coordinator's own events: a task and its run.
var scenes = []string{"running", "running_solo", "ended", "gate", "backlog", "source", "draft"}

func (e *env) scene(kind, project, owner string, n int) (taskID, runID string) {
	e.t.Helper()
	id := kind + "-" + strconv.Itoa(n)
	taskID, runID = "t-"+id, "r-"+id
	ev := journal.NewEvent
	machine := "far"
	if kind == "running_solo" {
		machine = "solo"
	}
	tk := task.Task{ID: taskID, Title: id, Dir: e.t.TempDir(), Machine: machine, Agent: "quick", Project: project, Owner: owner, Status: task.StatusTodo}
	run := task.Run{ID: runID, Task: taskID, Machine: machine, Agent: "quick", Profile: tend.AgentProfile{Name: "quick", Provider: agent.ProviderFake},
		Dir: tk.Dir, Project: project, Dispatcher: owner}
	caps := &agent.RunCaps{Steer: true, After: true, Interrupt: true, AnswerScope: true, Questions: true, Continue: true}
	var events []journal.Event
	switch kind {
	case "running", "running_solo":
		events = []journal.Event{ev(task.ETaskCreated, tk), ev(task.ERunQueued, run), ev(task.ERunStarting, task.RunStarting{ID: runID}),
			ev(task.ERunObserved, task.Observation{ID: runID, State: task.Running, NodeRev: 1, Stream: true, Attention: task.AttentionPermission, Caps: caps, Turn: 1,
				Requests: []agent.Request{{ID: "q1", Kind: agent.RequestPermission, Tool: "Bash", AllowRun: true},
					{ID: "q2", Kind: agent.RequestQuestion, Questions: []agent.Question{{Question: "which?", Options: []string{"a", "b"}}}}}})}
	case "ended":
		zero := 0
		events = []journal.Event{ev(task.ETaskCreated, tk), ev(task.ERunQueued, run), ev(task.ERunObserved, task.Observation{ID: runID, State: task.Exited,
			NodeRev: 1, ExitCode: &zero, Session: "s-" + id, Provider: agent.ProviderFake, Caps: caps})}
	case "gate":
		tk.Flow, tk.Workflow, tk.Stage, tk.Approver = &task.Flow{Name: "g", Stages: []task.Stage{{Name: "accept", Gate: task.GateHuman}}}, "g", "accept", owner
		events = []journal.Event{ev(task.ETaskCreated, tk), ev(task.ETaskStarted, task.TaskStart{IDs: []string{taskID}})}
	case "backlog":
		tk.Status = task.StatusBacklog
		events = []journal.Event{ev(task.ETaskCreated, tk)}
	case "source":
		tk.Kind, tk.Source = task.KindRequirement, &task.Source{Kind: "gitea", Number: int64(n + 1), Rev: 1, Digest: "a", Seen: "a", SeenRev: 1}
		events = []journal.Event{ev(task.ETaskCreated, tk), ev(task.ETaskSourced, task.SourceUpdate{ID: taskID, Digest: "b", Title: "new", Text: "v2"})}
	case "draft":
		events = []journal.Event{ev(task.ETaskCreated, tk), ev(task.EPlanDrafted, task.PlanDraft{ID: taskID, Plan: &task.Plan{Tasks: []task.PlanTask{{Key: "a", Title: "A"}}}, By: owner})}
	}
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	if err := e.c.commit(journal.System, nil, events...); err != nil {
		e.t.Fatal(err)
	}
	if e.c.st.Runs[runID] == nil {
		runID = ""
	}
	return taskID, runID
}

// act does act with task taskID (or its run runID) as a client would; nil when the coordinator took it. Takeover has no
// method: nil when p owns the run's machine.
func (e *env) act(p Principal, act, taskID, runID string) error {
	cli := e.as(p)
	id := func(m string) string { return m + "-" + strconv.FormatInt(e.cmd.Add(1), 10) }
	call := func(method string, params any) error { return callAs(cli, method, id(method), params, nil) }
	switch act {
	case task.ActSteer:
		return call(MRunSend, SendMessage{Run: runID, Text: "hello"})
	case task.ActAfter:
		return call(MRunSend, SendMessage{Run: runID, Text: "then this", Mode: agent.SendAfter})
	case task.ActInterrupt:
		return call(MRunInterrupt, Interrupt{Run: runID})
	case task.ActAnswer: // one of its requests: the question, else the permission
		if err := call(MRunAnswer, Answer{Run: runID, Answer: agent.Answer{Request: "q2", Allow: true, Answers: map[string]string{"which?": "a"}}}); err == nil {
			return nil
		}
		return call(MRunAnswer, Answer{Run: runID, Answer: agent.Answer{Request: "q1", Decision: agent.DecisionAllow}})
	case task.ActAllowRun:
		return call(MRunAnswer, Answer{Run: runID, Answer: agent.Answer{Request: "q1", Decision: agent.DecisionAllowRun}})
	case task.ActStop:
		if runID == "" {
			e.c.mu.Lock()
			runID = e.c.st.OpenRun(taskID).ID
			e.c.mu.Unlock()
		}
		return call(MRunStop, task.RunRef{ID: runID})
	case task.ActAbandon:
		return call(MRunAbandon, task.RunRef{ID: runID})
	case task.ActContinue:
		return call(MRunContinue, Continue{Run: runID, Text: "go on"})
	case task.ActTakeover:
		e.c.mu.Lock()
		defer e.c.mu.Unlock()
		if e.c.ownerOf(e.c.st.Runs[runID].Machine) != p.User {
			return forbidden("takeover")
		}
		return nil
	case task.ActDispatch:
		return call(MRunDispatch, Dispatch{Task: taskID})
	case task.ActStart:
		return call(MTaskStart, TaskRef{ID: taskID})
	case task.ActPass, task.ActRework:
		return call(MTaskGate, TaskGate{ID: taskID, Pass: act == task.ActPass, Notes: "n"})
	case task.ActAck, task.ActKeep:
		return call(MTaskSourceAck, task.SourceAck{ID: taskID, Accept: act == task.ActAck})
	case task.ActMerge:
		return call(MTaskMerge, TaskRef{ID: taskID})
	case task.ActDone, task.ActBacklog, task.ActCancel, task.ActReopen:
		s := map[string]string{task.ActDone: task.StatusDone, task.ActBacklog: task.StatusBacklog, task.ActCancel: task.StatusCanceled,
			task.ActReopen: task.StatusTodo}[act]
		return call(MTaskStatus, task.TaskStatus{ID: taskID, Status: s})
	case task.ActPlan:
		return call(MTaskPlan, TaskPlan{ID: taskID})
	case task.ActReview:
		return call(MTaskPlanApply, PlanApply{ID: taskID})
	case task.ActEdit:
		return call(MTaskEdit, task.TaskEdit{ID: taskID, Title: ptr("renamed")})
	case task.ActMove:
		return call(MTaskMove, task.TaskMove{ID: taskID, After: &[]string{}})
	case task.ActChild:
		return call(MTaskCreate, TaskCreate{Title: "kid", Parent: taskID})
	case task.ActMessage:
		return call(MTaskMessage, TaskMessage{ID: taskID, Text: "note this"})
	}
	e.t.Fatalf("no way to do %s", act)
	return nil
}

// offered is what p's affordances give for task taskID and its run runID.
func (e *env) offered(p Principal, taskID, runID string) (tasks, runs []string) {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	now := e.c.affordances(p, []string{taskID})
	var ta TaskAffordance
	json.Unmarshal([]byte(now["t:"+taskID]), &ta)
	json.Unmarshal([]byte(now["r:"+runID]), &runs)
	return ta.Actions, runs
}

// TestEveryActionGivenIsTakenAndNoneWithheldIs: for each viewer, each state and each action that state allows, the
// coordinator takes the action exactly when the viewer's affordances give it; the viewers cover the machine's owner,
// the dispatcher, a reader, someone outside the project, an admin whose project the machine is not shared with, and
// mode 1.
func TestEveryActionGivenIsTakenAndNoneWithheldIs(t *testing.T) {
	for _, teamMode := range []bool{true, false} {
		e := stage(t, teamMode)
		viewers, project, owner := []Principal{root, ann, bob, cy, dee}, "p1", bob.User
		if !teamMode {
			viewers, project, owner = []Principal{Owner}, "", Owner.User
		}
		n, given, withheld := 0, 0, 0
		for _, p := range viewers {
			for _, kind := range scenes {
				n++
				tid, rid := e.scene(kind, project, owner, n)
				tasks, runs := e.offered(p, tid, rid)
				e.c.mu.Lock()
				tc := e.c.st.TaskActions(e.c.st.Tasks[tid])
				var rc []string
				if rid != "" {
					rc = e.c.st.RunActions(e.c.st.Runs[rid])
				}
				e.c.mu.Unlock()
				check := func(cands, offer []string, onRun bool) {
					for _, a := range cands {
						n++
						t2, r2 := e.scene(kind, project, owner, n) // a fresh one: acting changes it
						if !onRun && a != task.ActStop {
							r2 = ""
						}
						err := e.act(p, a, t2, r2)
						want := slices.Contains(offer, a)
						if want != (err == nil) {
							t.Errorf("team %v, %s, %s, %s: given %v, taken: %v", teamMode, p.User, kind, a, want, err)
						}
						if want {
							given++
						} else {
							withheld++
						}
					}
				}
				check(tc, tasks, false)
				check(rc, runs, true)
			}
		}
		t.Logf("team %v: %d given, %d withheld", teamMode, given, withheld)
		if given < 20 || teamMode && withheld < 20 {
			t.Fatalf("team %v: %d given, %d withheld: the states test too little", teamMode, given, withheld)
		}
	}
}

// TestAffordancesReachEachViewerAndFollowWhatTheyMayDo: the snapshot carries the viewer's own; a change to a task
// pushes only what changed; losing the project resets them; Reaffirm counts them again when what they rest on changed
// outside the journal.
func TestAffordancesReachEachViewerAndFollowWhatTheyMayDo(t *testing.T) {
	e := stage(t, true)
	tid, rid := e.scene("running", "p1", bob.User, 1)
	var bobs, dees StateFold
	wb, wd := watchState(t, e.as(bob), WatchParams{}), watchState(t, e.as(dee), WatchParams{})
	wb.fold(&bobs, e.c.State().Seq)
	wd.fold(&dees, e.c.State().Seq)
	if got := bobs.Aff.Runs[rid]; !slices.Contains(got, task.ActSteer) || !slices.Contains(got, task.ActAnswer) {
		t.Fatalf("bob dispatched it: %v", got)
	}
	if rt := bobs.Aff.Tasks[tid]; rt == nil || rt.Route.To != task.RouteRun || rt.Route.Run != rid {
		t.Fatalf("bob's route: %+v", rt)
	}
	if len(dees.Aff.Runs)+len(dees.Aff.Tasks) != 0 {
		t.Fatalf("dee only reads: %+v", dees.Aff)
	}

	e.c.mu.Lock()
	zero := 0
	e.c.commit(journal.System, nil, journal.NewEvent(task.ERunObserved, task.Observation{ID: rid, State: task.Exited, NodeRev: 2, ExitCode: &zero,
		Session: "s1", Provider: agent.ProviderFake, Caps: &agent.RunCaps{Continue: true}}))
	e.c.mu.Unlock()
	env := wb.journal()
	bobs.Apply(wire.Push{Method: PushJournal, Params: mustJSON(env)})
	p := wb.next()
	var push struct {
		Runs  map[string]json.RawMessage
		Tasks map[string]json.RawMessage
	}
	if p.Method != PushAffordances || p.Decode(&push) != nil || len(push.Runs) != 1 || len(push.Tasks) != 1 {
		t.Fatalf("after the run ended: %s %s", p.Method, p.Params)
	}
	bobs.Apply(p)
	if got := bobs.Aff.Runs[rid]; !slices.Equal(got, []string{task.ActContinue}) || bobs.Aff.Tasks[tid].Route.To != task.RouteReply {
		t.Fatalf("an ended run with a session: %v %+v", got, bobs.Aff.Tasks[tid])
	}

	var anns StateFold
	wa := watchState(t, e.as(ann), WatchParams{})
	tid2, rid2 := e.scene("running", "p1", bob.User, 2)
	wa.fold(&anns, e.c.State().Seq)
	for anns.Aff.Runs[rid2] == nil {
		if _, err := anns.Apply(wa.next()); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Contains(anns.Aff.Runs[rid2], task.ActAllowRun) {
		t.Fatalf("ann owns its machine: %v", anns.Aff.Runs[rid2])
	}
	e.c.mu.Lock()
	e.owner = func(string) string { return "u_nobody" }
	e.c.opt.MachineOwner = e.owner
	e.c.mu.Unlock()
	e.c.Reaffirm()
	for slices.Contains(anns.Aff.Runs[rid2], task.ActAllowRun) {
		if _, err := anns.Apply(wa.next()); err != nil {
			t.Fatal(err)
		}
	}

	if err := callAs(e.as(ann), MProjectMember, "m-bob-out", task.MemberSet{Project: "p1", User: bob.User}, nil); err != nil {
		t.Fatal(err)
	}
	for {
		p := wb.next()
		if _, err := bobs.Apply(p); err != nil {
			t.Fatal(err)
		}
		if p.Method == PushLive {
			break
		}
	}
	if len(bobs.Aff.Runs)+len(bobs.Aff.Tasks) != 0 || bobs.St.Tasks[tid2] != nil {
		t.Fatalf("out of the project, bob keeps %+v", bobs.Aff)
	}
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
