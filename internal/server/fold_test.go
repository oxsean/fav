package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// foldScenario is envelopes that touch every event and the edges of a run's observations: a late observation, an
// abandoned run whose end arrives, answers taken, sends failed at the end.
func foldScenario() []journal.Envelope {
	at := time.Date(2026, 9, 27, 10, 0, 0, 123456789, time.UTC)
	seq := int64(0)
	var envs []journal.Envelope
	add := func(events ...journal.Event) {
		seq++
		envs = append(envs, journal.Envelope{V: journal.Version, Seq: seq, At: at.Add(time.Duration(seq) * time.Second), Events: events})
	}
	ev := journal.NewEvent
	exit0, exit1, started := 0, 1, at.Add(time.Minute)
	add(ev(task.EProjectCreated, task.Project{ID: "p1", Name: "One", Owner: "u_a"}))
	add(ev(task.EMemberSet, task.MemberSet{Project: "p1", User: "u_b", Role: task.RoleParticipant}),
		ev(task.EMemberSet, task.MemberSet{Project: "p1", User: "u_c", Role: task.RoleReader}))
	add(ev(task.EMemberSet, task.MemberSet{Project: "p1", User: "u_c"}))
	add(ev(task.EProjectEdited, task.ProjectEdit{ID: "p1", Name: ptr("Uno")}))
	add(ev(task.EProjectEdited, task.ProjectEdit{ID: "p1", Context: ptr("ctx"), Repos: &[]task.Repo{{Name: "app", Dirs: map[string]string{"mba": "/w"}}},
		Defaults: &task.Defaults{Machine: "mba", Roles: map[string]string{"implement": "fake"}}, Hooks: &map[string][]string{"check": {"gate"}}}))
	add(ev(task.EMachineShared, task.Share{Machine: "mba", Projects: []string{"p1"}, Approve: true}))
	add(ev(task.EMachineShared, task.Share{Machine: "old", Users: []string{"u_b"}}))
	add(ev(task.EMachineShared, task.Share{Machine: "old"}))
	add(ev(task.ETaskCreated, task.Task{ID: "t1", Title: "x", Dir: "/w", Project: "p1", Owner: "u_b", Status: task.StatusTodo}))
	add(ev(task.ETaskEdited, task.TaskEdit{ID: "t1", Title: ptr("y"), Brief: ptr("b")}))
	add(ev(task.ERunQueued, task.Run{ID: "r1", Task: "t1", Machine: "mba", Agent: "fake", Profile: tend.AgentProfile{Name: "fake", Provider: "fake"},
		Dir: "/w", Project: "p1", Dispatcher: "u_b"}))
	add(ev(task.ERunStarting, task.RunStarting{ID: "r1", Dir: "/w2"}))
	add(ev(task.ERunObserved, task.Observation{ID: "r1", State: task.Running, NodeRev: 2, StartedAt: &started, Session: "s1", Provider: "claude",
		Stream: true, Attention: task.AttentionPermission, Requests: []agent.Request{{ID: "q1", Kind: agent.RequestPermission, Tool: "Bash"}}}))
	add(ev(task.ERunAnswered, task.RunAnswer{ID: "r1", Answer: agent.Answer{Request: "q1", Allow: true}}))
	add(ev(task.ERunSent, task.RunSend{ID: "r1", Send: agent.Send{ID: "m1", Text: "hi", State: agent.SendQueued}}))
	add(ev(task.ERunSent, task.RunSend{ID: "r1", Send: agent.Send{ID: "m2", Text: "late", State: agent.SendQueued}}))
	add(ev(task.ERunObserved, task.Observation{ID: "r1", State: task.Starting, NodeRev: 1})) // late: ignored
	add(ev(task.ERunObserved, task.Observation{ID: "r1", State: task.Running, NodeRev: 3, Last: "working", Doing: "go test ./...",
		Caps: &agent.RunCaps{Steer: true, Interrupt: true, AnswerScope: true}, Sends: []agent.Send{{ID: "m1", Text: "hi", State: agent.SendSeen}}}))
	add(ev(task.ERunStopAsked, task.RunRef{ID: "r1"}))
	add(ev(task.ERunObserved, task.Observation{ID: "r1", State: task.Stopped, NodeRev: 4, ExitCode: &exit0, EndedAt: &started}))
	add(ev(task.ERunQueued, task.Run{ID: "r2", Task: "t1", Machine: "mba", Agent: "fake", Dir: "/w"}))
	add(ev(task.ERunCanceled, task.RunRef{ID: "r2", Reason: "access_revoked"}))
	add(ev(task.ERunQueued, task.Run{ID: "r3", Task: "t1", Machine: "mba", Agent: "fake", Dir: "/w"}))
	add(ev(task.ERunStarting, task.RunStarting{ID: "r3"}))
	add(ev(task.ERunAbandoned, task.RunRef{ID: "r3"}))
	add(ev(task.ERunObserved, task.Observation{ID: "r3", State: task.Running, NodeRev: 1})) // abandoned: only an end counts
	add(ev(task.ERunObserved, task.Observation{ID: "r3", State: task.Failed, NodeRev: 2, Reason: "quota", Detail: "limit"}))
	add(ev(task.ETaskStatus, task.TaskStatus{ID: "t1", Status: task.StatusDone}))
	add(ev(task.ETaskCreated, task.Task{ID: "t2", Title: "tree", Status: task.StatusBacklog, Owner: "u_a"}),
		ev(task.ETaskCreated, task.Task{ID: "t3", Title: "leaf 1", Parent: "t2", Status: task.StatusBacklog}),
		ev(task.ETaskCreated, task.Task{ID: "t4", Title: "leaf 2", Parent: "t2", After: []string{"t3"}, Status: task.StatusBacklog}),
		ev(task.ETaskCreated, task.Task{ID: "t5", Title: "by hand", Status: task.StatusTodo}))
	add(ev(task.ETaskStarted, task.TaskStart{IDs: []string{"t2", "t3", "t4"}}))
	add(ev(task.ETaskHeld, task.TaskHold{ID: "t3", Reason: "no_agent", Detail: "quick"}))
	add(ev(task.ETaskEdited, task.TaskEdit{ID: "t3", Owner: ptr("u_b"), Approver: ptr("u_a"), Kind: ptr("requirement"),
		Accept: ptr([]string{"works"}), Tags: ptr([]string{"api"})}))
	add(ev(task.ERunQueued, task.Run{ID: "r4", Task: "t3", Machine: "mba", Agent: "fake", Dir: "/w"}))
	add(ev(task.ERunStarting, task.RunStarting{ID: "r4"}))
	add(ev(task.ERunObserved, task.Observation{ID: "r4", State: task.Exited, NodeRev: 1, ExitCode: &exit0}))
	add(ev(task.ETaskMoved, task.TaskMove{ID: "t4", After: ptr([]string{})}))
	add(ev(task.ETaskCreated, task.Task{ID: "t6", Title: "issue", Brief: "v1", Project: "p1", Kind: task.KindRequirement, Status: task.StatusTodo,
		Source: &task.Source{Kind: "gitea", Tracker: "tr", Base: "http://git", Repo: "o/r", RepoID: 3, Number: 8, Rev: 1, Digest: "a", Seen: "a", SeenRev: 1}}))
	add(ev(task.ETaskSourced, task.SourceUpdate{ID: "t6", Digest: "b", Title: "issue 2", Text: "v2", URL: "http://git/o/r/issues/8"}))
	add(ev(task.ETaskSourceAcked, task.SourceAck{ID: "t6", Accept: true}))
	add(ev(task.ETaskSourced, task.SourceUpdate{ID: "t6", Digest: "c", Title: "issue 3", Text: "v3", Closed: true}))
	add(ev(task.ETaskSourceAcked, task.SourceAck{ID: "t6"}))
	add(ev(task.ETaskCreated, task.Task{ID: "t7", Title: "closed", Project: "p1", Kind: task.KindRequirement, Status: task.StatusTodo,
		Source: &task.Source{Kind: "gitea", Number: 9, Rev: 1, Digest: "a", Seen: "a", SeenRev: 1}}))
	add(ev(task.ETaskSourced, task.SourceUpdate{ID: "t7", Digest: "a", Closed: true}))
	flow := &task.Flow{Name: "feature", MaxLoops: 1, Budget: &task.Budget{Minutes: 600}, Stages: []task.Stage{{Name: "implement", Role: "implement", Check: true},
		{Name: "review", Role: "review", Output: task.OutputVerdict, OnRework: "implement"}, {Name: "accept", Gate: task.GateHuman}}}
	add(ev(task.ETaskCreated, task.Task{ID: "t8", Title: "flow", Dir: "/w", Status: task.StatusTodo, Workflow: "feature", Flow: flow, Stage: "implement"}))
	add(ev(task.ETaskStarted, task.TaskStart{IDs: []string{"t8"}}))
	add(ev(task.ERunQueued, task.Run{ID: "r5", Task: "t8", Machine: "mba", Agent: "fake", Dir: "/w", Stage: "implement", Check: []string{"gate"}}))
	add(ev(task.ERunStarting, task.RunStarting{ID: "r5"}))
	add(ev(task.ERunObserved, task.Observation{ID: "r5", State: task.Exited, NodeRev: 1, ExitCode: &exit0, StartedAt: &started, EndedAt: &started,
		Check: &agent.CheckResult{Argv: []string{"gate"}, Exit: 1, Tail: "FAIL"}}))
	add(ev(task.ETaskNoted, task.TaskNote{ID: "t8", Note: task.Note{Stage: "implement", Kind: task.NoteRework, Text: "check failed"}}),
		ev(task.ETaskStaged, task.TaskStage{ID: "t8", Stage: "implement", Loops: 1, Back: true}))
	add(ev(task.ERunQueued, task.Run{ID: "r6", Task: "t8", Machine: "mba", Agent: "fake", Dir: "/w", Stage: "implement"}))
	add(ev(task.ERunObserved, task.Observation{ID: "r6", State: task.Exited, NodeRev: 1, ExitCode: &exit0}))
	add(ev(task.ETaskStaged, task.TaskStage{ID: "t8", Stage: "review", Loops: 1}))
	add(ev(task.ERunQueued, task.Run{ID: "r7", Task: "t8", Machine: "mba", Agent: "fake", Dir: "/w", Stage: "review", Judge: true}))
	add(ev(task.ERunObserved, task.Observation{ID: "r7", State: task.Exited, NodeRev: 1, ExitCode: &exit0,
		Verdict: &agent.Verdict{Verdict: agent.VerdictRework, Summary: "quote commas", At: started}}))
	add(ev(task.ETaskCreated, task.Task{ID: "t9", Title: "flow 2", Dir: "/w", Status: task.StatusTodo, Workflow: "feature", Flow: flow, Stage: "implement"}))
	add(ev(task.ETaskEdited, task.TaskEdit{ID: "t9", Workflow: ptr("")}))
	add(ev(task.ETaskStaged, task.TaskStage{ID: "t8", Stage: "accept", Loops: 1}))
	ws := func(branch string, chain ...string) *agent.Workspace {
		return &agent.Workspace{Checkout: "/src", Branch: branch, Chain: chain, Base: "main"}
	}
	add(ev(task.ETaskCreated, task.Task{ID: "t10", Title: "tree", Status: task.StatusTodo, Workflow: "feature", Flow: flow, Stage: "implement"}),
		ev(task.ETaskCreated, task.Task{ID: "t11", Title: "leaf", Parent: "t10", Status: task.StatusTodo}),
		ev(task.ETaskCreated, task.Task{ID: "t12", Title: "leaf 2", Parent: "t10", Status: task.StatusTodo}))
	add(ev(task.ETaskStarted, task.TaskStart{IDs: []string{"t10", "t11", "t12"}}))
	add(ev(task.ERunQueued, task.Run{ID: "r8", Task: "t11", Machine: "mba", Agent: "fake", Dir: "/src", Work: ws("tend/t11", "tend/t10")}),
		ev(task.ERunQueued, task.Run{ID: "r9", Task: "t12", Machine: "mba", Agent: "fake", Dir: "/src", Work: ws("tend/t12", "tend/t10")}))
	add(ev(task.ERunObserved, task.Observation{ID: "r8", State: task.Exited, NodeRev: 1, ExitCode: &exit0, Work: &agent.Work{Head: "a1", Commits: 1}}),
		ev(task.ERunObserved, task.Observation{ID: "r9", State: task.Exited, NodeRev: 1, ExitCode: &exit0, Work: &agent.Work{Head: "b1"}}))
	merge := func(id, run, branch string) *task.Run {
		w := ws("tend/t10")
		w.Merge = branch
		return &task.Run{ID: run, Task: id, Machine: "mba", Agent: "git", Dir: "/src", Stage: task.StageMerge, Work: w}
	}
	add(ev(task.ERunQueued, *merge("t11", "r10", "tend/t11")), ev(task.ERunQueued, *merge("t12", "r11", "tend/t12")))
	add(ev(task.ERunObserved, task.Observation{ID: "r10", State: task.Exited, NodeRev: 1, ExitCode: &exit0, Work: &agent.Work{Merged: true, Head: "m1"}}),
		ev(task.ERunObserved, task.Observation{ID: "r11", State: task.Exited, NodeRev: 1, ExitCode: &exit1, Reason: task.WhyMergeConflict,
			Work: &agent.Work{Conflict: []string{"a.go"}}}))
	add(ev(task.ETaskStatus, task.TaskStatus{ID: "t12", Status: task.StatusCanceled}), ev(task.ETaskStatus, task.TaskStatus{ID: "t11", Status: task.StatusDone}))
	add(ev(task.ETaskStaged, task.TaskStage{ID: "t10", Stage: "review"}))
	add(ev(task.ETaskLinked, task.Linked{ID: "t11", Issue: "https://git.example/a/b/issues/9"}), ev(task.ETaskLinked, task.Linked{ID: "t10", PR: "https://git.example/a/b/pulls/10"}))
	add(ev(task.ERunQueued, task.Run{ID: "r12", Task: "t10", Machine: "mba", Agent: "fake", Dir: "/src", Stage: "review", Judge: true,
		Work: &agent.Workspace{Checkout: "/src", Branch: "tend/t10", ReadOnly: true}}))
	add(ev(task.ERunObserved, task.Observation{ID: "r12", State: task.Exited, NodeRev: 1, ExitCode: &exit0,
		Verdict: &agent.Verdict{Verdict: agent.VerdictPass, At: started}, Work: &agent.Work{Head: "m0", Discarded: 1}}))
	plan := &task.Plan{Tasks: []task.PlanTask{{Key: "a", Title: "A"}, {Key: "b", Title: "B", After: []string{"a"}}}, Questions: []string{"why?"}}
	add(ev(task.ETaskCreated, task.Task{ID: "t13", Title: "requirement", Dir: "/w", Kind: task.KindRequirement, Status: task.StatusTodo}))
	add(ev(task.ERunQueued, task.Run{ID: "r13", Task: "t13", Machine: "mba", Agent: "fake", Dir: "/w", Stage: task.StagePlan, Planner: true}))
	add(ev(task.ERunObserved, task.Observation{ID: "r13", State: task.Exited, NodeRev: 1, ExitCode: &exit0, Plan: plan}))
	add(ev(task.ETaskCreated, task.Task{ID: "t14", Title: "requirement 2", Dir: "/w", Status: task.StatusTodo}))
	add(ev(task.ERunQueued, task.Run{ID: "r14", Task: "t14", Machine: "mba", Agent: "fake", Dir: "/w", Stage: task.StagePlan, Planner: true}))
	add(ev(task.ERunObserved, task.Observation{ID: "r14", State: task.Exited, NodeRev: 1, ExitCode: &exit0, Plan: plan}))
	add(ev(task.EPlanDrafted, task.PlanDraft{ID: "t14", Plan: &task.Plan{Tasks: plan.Tasks[:1]}, By: "u1"}))
	add(ev(task.ETaskCreated, task.Task{ID: "t15", Title: "A", Parent: "t14", Status: task.StatusBacklog}),
		ev(task.EPlanApplied, task.PlanApplied{ID: "t14"}))
	add(ev(task.ETaskCreated, task.Task{ID: "td1", Title: "d1", Dir: "/d", Status: task.StatusTodo}),
		ev(task.ETaskCreated, task.Task{ID: "td2", Title: "d2", Dir: "/d", Status: task.StatusTodo}),
		ev(task.ERunQueued, task.Run{ID: "rd1", Task: "td1", Machine: "mba", Agent: "fake", Dir: "/d"}),
		ev(task.ERunQueued, task.Run{ID: "rd2", Task: "td2", Machine: "mba", Agent: "fake", Dir: "/d"}))
	add(ev(task.ERunObserved, task.Observation{ID: "rd1", State: task.Running, NodeRev: 1}))
	add(ev("some_future_event", map[string]string{"id": "t1"}))
	return envs
}

func ptr[T any](v T) *T { return &v }

// normalized drops what JSON leaves out either way (empty, zero, null), so Go's omitempty and the page's undefined
// compare equal.
func normalized(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if n := normalized(e); n != nil {
				out[k] = n
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		if len(x) == 0 {
			return nil
		}
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalized(e)
		}
		return out
	case string:
		if x == "" {
			return nil
		}
	case float64:
		if x == 0 {
			return nil
		}
	case bool:
		if !x {
			return nil
		}
	}
	return v
}

func TestThePageFoldsEnvelopesAsTheCoordinatorDoes(t *testing.T) {
	nodeBin := nodeJS(t)
	envs := foldScenario()
	st := task.New()
	for _, env := range envs {
		if env.Events[0].Type == "some_future_event" {
			st.Seq = env.Seq // the coordinator refuses what it does not know; a client skips it
			continue
		}
		if err := st.Apply(env); err != nil {
			t.Fatalf("seq %d: %v", env.Seq, err)
		}
	}
	dir := t.TempDir()
	b, _ := json.Marshal(envs)
	os.WriteFile(filepath.Join(dir, "envs.json"), b, 0o600)
	fold, _ := filepath.Abs(filepath.Join("web", "fold.js"))
	script := `const fs=require('fs'),vm=require('vm');vm.runInThisContext(fs.readFileSync(process.argv[1],'utf8'));
let s={seq:0,tasks:{},runs:{},projects:{},shares:{}};for(const e of JSON.parse(fs.readFileSync(process.argv[2],'utf8')))s=Fold.apply(s,e);
const sits={};for(const t of Object.values(s.tasks))sits[t.id]=Fold.situation(s,t);
process.stdout.write(JSON.stringify({state:s,sits}));`
	out, err := exec.Command(nodeBin, "-e", script, fold, filepath.Join(dir, "envs.json")).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var both struct {
		State any
		Sits  map[string]task.Situation
	}
	json.Unmarshal(out, &both)
	page := both.State
	var goState any
	gb, _ := json.Marshal(st)
	json.Unmarshal(gb, &goState)
	if runs, _ := page.(map[string]any)["runs"].(map[string]any); len(runs) != 16 || len(st.Runs) != 16 {
		t.Fatalf("the page folded %d runs: %s", len(runs), out)
	}
	for id, x := range st.Tasks {
		if want := st.Situation(x); both.Sits[id] != want {
			t.Errorf("%s stands %+v on the page, %+v in Go", id, both.Sits[id], want)
		}
	}
	if !reflect.DeepEqual(normalized(page), normalized(goState)) {
		p, _ := json.MarshalIndent(normalized(page), "", " ")
		g, _ := json.MarshalIndent(normalized(goState), "", " ")
		t.Fatalf("the page's fold:\n%s\nthe coordinator's:\n%s", p, g)
	}
}
