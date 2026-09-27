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
	exit0, started := 0, at.Add(time.Minute)
	add(ev(task.EProjectCreated, task.Project{ID: "p1", Name: "One", Owner: "u_a"}))
	add(ev(task.EMemberSet, task.MemberSet{Project: "p1", User: "u_b", Role: task.RoleParticipant}),
		ev(task.EMemberSet, task.MemberSet{Project: "p1", User: "u_c", Role: task.RoleReader}))
	add(ev(task.EMemberSet, task.MemberSet{Project: "p1", User: "u_c"}))
	add(ev(task.EProjectEdited, task.ProjectEdit{ID: "p1", Name: ptr("Uno")}))
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
	add(ev(task.ERunObserved, task.Observation{ID: "r1", State: task.Running, NodeRev: 3, Last: "working",
		Sends: []agent.Send{{ID: "m1", Text: "hi", State: agent.SendSent}}}))
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
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
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
process.stdout.write(JSON.stringify(s));`
	out, err := exec.Command(nodeBin, "-e", script, fold, filepath.Join(dir, "envs.json")).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var page, goState any
	json.Unmarshal(out, &page)
	gb, _ := json.Marshal(st)
	json.Unmarshal(gb, &goState)
	if runs, _ := page.(map[string]any)["runs"].(map[string]any); len(runs) != 3 || len(st.Runs) != 3 {
		t.Fatalf("the page folded %d runs: %s", len(runs), out)
	}
	if !reflect.DeepEqual(normalized(page), normalized(goState)) {
		p, _ := json.MarshalIndent(normalized(page), "", " ")
		g, _ := json.MarshalIndent(normalized(goState), "", " ")
		t.Fatalf("the page's fold:\n%s\nthe coordinator's:\n%s", p, g)
	}
}
