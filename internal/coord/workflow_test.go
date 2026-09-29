package coord

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// stageRuns are the runs of task id in the order they were queued, with their stages.
func stageRuns(st *task.State, id string) []*task.Run {
	var out []*task.Run
	for _, r := range st.Runs {
		if r.Task == id {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b *task.Run) int { return int(a.Seq - b.Seq) })
	return out
}

func TestAFeatureIsReviewedSentBackAndWaitsForItsApprover(t *testing.T) {
	heard := filepath.Join(t.TempDir(), "events")
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{{Name: "reviewer", Provider: agent.ProviderFake,
		Args: []string{"--steps", "1", "--every", "50ms", "--verdicts", "rework,pass"}}},
		NotifyCommand: []string{os.Args[0], "_notify", heard}, NotifyEvents: []string{NotifyTaskStage, NotifyTaskRework}})
	e.start()
	e.must(MProjectCreate, ProjectCreate{ID: "p1", Name: "One"}, nil)
	e.must(MProjectEdit, task.ProjectEdit{ID: "p1", Defaults: &task.Defaults{Workflow: "feature",
		Roles: map[string]string{"implement": "quick", "review": "reviewer"}}}, nil)
	var x task.Task
	e.must(MTaskCreate, TaskCreate{Title: "export", Brief: "Export the list as CSV.", Dir: t.TempDir(), Project: "p1",
		Accept: []string{"commas are quoted"}}, &x)
	if x.Workflow != "feature" || x.Stage != "implement" {
		t.Fatalf("a project's default workflow: %+v", x)
	}
	e.must(MTaskStart, TaskRef{ID: x.ID}, nil)
	var st *task.State
	e.until("reviewed twice, it waits at the gate", func(s *task.State) bool {
		st = s
		return s.Situation(s.Tasks[x.ID]).Reason == task.WhyAccept
	})
	runs := stageRuns(st, x.ID)
	var stages []string
	for _, r := range runs {
		stages = append(stages, r.Stage)
	}
	if !slices.Equal(stages, []string{"implement", "review", "implement", "review"}) {
		t.Fatalf("stages %v", stages)
	}
	y := st.Tasks[x.ID]
	if y.Loops != 1 || y.Stage != "accept" || !slices.ContainsFunc(y.Notes, func(n task.Note) bool { return n.Kind == task.NoteRework }) {
		t.Fatalf("one rework: %+v", y)
	}
	if runs[2].Resume == "" || runs[2].Resume != runs[0].Session || !strings.Contains(runs[2].Brief, "fake rework 1") {
		t.Fatalf("the implementer goes on in its own session with what the review said: %+v", runs[2])
	}
	if !runs[1].Judge || !strings.Contains(runs[1].Brief, "commas are quoted") || !strings.Contains(runs[3].Brief, "Workpad") {
		t.Fatalf("a reviewer is told to judge against the criteria: %+v", runs[1])
	}
	var moves []string
	for deadline := time.Now().Add(10 * time.Second); len(moves) < 4 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		b, _ := os.ReadFile(heard)
		moves = nil
		for dec := json.NewDecoder(bytes.NewReader(b)); ; {
			var ev NotifyEvent
			if dec.Decode(&ev) != nil {
				break
			}
			moves = append(moves, ev.Event+" "+ev.Stage)
		}
	}
	if want := []string{"task.stage review", "task.rework implement", "task.stage review", "task.stage accept"}; !slices.Equal(moves, want) {
		t.Fatalf("the notify command hears each move: %q", moves)
	}
	var m MessageResult
	e.must(MTaskMessage, TaskMessage{ID: x.ID, Text: "also TSV next time"}, &m)
	if m.To != task.RouteWorkpad {
		t.Fatalf("no run: a message is kept: %+v", m)
	}
	if err := e.call(MTaskStatus, task.TaskStatus{ID: x.ID, Status: task.StatusDone}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("only its gate finishes a task in a workflow: %v", err)
	}
	if err := e.call(MTaskGate, TaskGate{ID: x.ID, Pass: true, ExpectedRev: y.Rev}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("a decision on an older task is refused: %v", err)
	}
	e.must(MTaskGate, TaskGate{ID: x.ID, Pass: true}, &x)
	if x.Status != task.StatusDone {
		t.Fatalf("passed: %+v", x)
	}
}

func TestAFailedCheckSendsTheWorkBackAndTooManyStop(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "checked")
	e := newEnv(t, tend.Config{Node: tend.NodeConfig{AllowHooks: true}})
	e.start()
	e.must(MProjectCreate, ProjectCreate{ID: "p1", Name: "One"}, nil)
	tiny := "---\nname: tiny\nstages:\n  - {name: build, role: implement, check: true}\n  - {name: review, role: review, output: verdict}\nmax_loops: 1\n---\n"
	e.must(MProjectEdit, task.ProjectEdit{ID: "p1", Workflows: &map[string]string{"tiny": tiny}, Hooks: &map[string][]string{"check": {os.Args[0], "_check", flag}},
		Defaults: &task.Defaults{Roles: map[string]string{"implement": "quick", "review": "quick"}}}, nil)
	var x task.Task
	e.must(MTaskCreate, TaskCreate{Title: "gate", Dir: t.TempDir(), Project: "p1", Workflow: "tiny"}, &x)
	e.must(MTaskStart, TaskRef{ID: x.ID}, nil)
	var st *task.State
	e.until("the review gives no verdict", func(s *task.State) bool {
		st = s
		return s.Situation(s.Tasks[x.ID]).Reason == task.WhyBlocked
	})
	runs := stageRuns(st, x.ID)
	if len(runs) != 3 || runs[0].Checked == nil || runs[0].Checked.Exit != 1 || !strings.Contains(runs[0].Checked.Tail, "TestExport") ||
		runs[1].Checked == nil || runs[1].Checked.Exit != 0 || !strings.Contains(runs[1].Brief, "TestExport") {
		t.Fatalf("a failed check is a rework, its output in the next brief: %+v", runs)
	}
	e.must(MTaskEdit, task.TaskEdit{ID: x.ID, Workflow: ptr("tiny")}, nil)
	os.Remove(flag)
	e.must(MTaskStart, TaskRef{ID: x.ID}, nil)
	e.until("the check fails at its limit", func(s *task.State) bool {
		x := s.Tasks[x.ID]
		return x.Loops == 1 && s.Situation(x).Reason == task.WhyMaxLoops || s.Situation(x).Reason == task.WhyBlocked && x.Loops == 1
	})
}
