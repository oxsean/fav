package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tracker"
)

func (r *syncRig) settle(f func(*TrackerSettings)) {
	r.t.Helper()
	set := settingsOf(r.tracker())
	f(&set)
	b, _ := json.Marshal(set)
	if err := r.team.SetTrackerSettings(r.x.ID, string(b)); err != nil {
		r.t.Fatal(err)
	}
}

func (r *syncRig) get(id string) task.Task {
	var out task.Task
	r.c.Read(func(st *task.State) { out = *st.Tasks[id] })
	return out
}

func TestSubtasksBecomeSubIssuesOfTheirRequirement(t *testing.T) {
	for _, kind := range []string{tracker.KindGitea, tracker.KindGitHub, tracker.KindGitLab} {
		t.Run(kind, func(t *testing.T) {
			r := newSyncRigOf(t, kind)
			r.settle(func(s *TrackerSettings) { s.SubIssues = true })
			r.g.Open(1, "Export CSV", "rows as CSV", "tend")
			r.pass(0)
			x := r.task(1)
			var kid, kid2 task.Task
			r.call(coord.Owner, coord.MTaskCreate, coord.TaskCreate{Title: "the exporter", Brief: "write it", Parent: x.ID}, &kid)
			r.g.LoseIssue = true
			r.pass(5 * time.Second)
			if r.get(kid.ID).Issue != "" {
				t.Fatal("a lost answer records nothing yet")
			}
			r.pass(61 * time.Second)
			sub := r.get(kid.ID).Issue
			made := r.g.Titled("the exporter")
			if sub == "" || len(made) != 1 || !strings.Contains(made[0].Body, "write it") || !strings.Contains(made[0].Body, "#1") {
				t.Fatalf("one sub-issue, found again after the lost answer, naming its parent: %q %+v", sub, made)
			}
			if got, want := len(r.g.SubIssues(1)), map[bool]int{true: 1}[kind == tracker.KindGitHub]; got != want {
				t.Fatalf("GitHub links it as a sub-issue: %d", got)
			}
			n := made[0].Number
			if r.task(n) != nil {
				t.Fatal("a sub-issue is never a requirement of its own")
			}
			r.pass(61 * time.Second)
			if cs := r.g.CommentsOf(n); len(cs) != 1 || !strings.Contains(cs[0].Body, "tend:progress") {
				t.Fatalf("the sub-issue keeps its own progress comment: %+v", cs)
			}
			r.call(coord.Owner, coord.MTaskCreate, coord.TaskCreate{Title: "the docs", Parent: kid.ID}, &kid2)
			r.call(coord.Owner, coord.MTaskStatus, task.TaskStatus{ID: kid.ID, Status: task.StatusDone}, nil)
			r.pass(61 * time.Second)
			if !r.g.Get(n).Closed || r.get(kid2.ID).Issue == "" {
				t.Fatalf("a done subtask closes its sub-issue; a grandchild gets one too: %+v %q", r.g.Get(n), r.get(kid2.ID).Issue)
			}
		})
	}
}

func TestARequirementsPushedBranchBecomesOnePullRequest(t *testing.T) {
	for _, kind := range []string{tracker.KindGitea, tracker.KindGitHub, tracker.KindGitLab} {
		t.Run(kind, func(t *testing.T) {
			r := newSyncRigOf(t, kind)
			r.settle(func(s *TrackerSettings) { s.PR = true })
			r.g.Open(1, "Export CSV", "rows as CSV", "tend")
			r.pass(0)
			x := r.task(1)
			r.s.branch = func(st *task.State, t *task.Task) (string, string, bool) {
				return task.BranchOf(t.ID), "", t.ID == x.ID
			}
			r.pass(61 * time.Second)
			if row, _ := r.team.TrackerIssue(r.x.ID, 1); len(r.g.Pulls()) != 0 || row.LastError == "" || r.tracker().Stopped != "" {
				t.Fatalf("a branch the tracker lacks is this issue's error, not the binding's: %+v %+v", row, r.g.Pulls())
			}
			r.g.Branch(task.BranchOf(x.ID))
			r.pass(61 * time.Second)
			r.pass(61 * time.Second)
			pulls := r.g.Pulls()
			if len(pulls) != 1 {
				t.Fatalf("one pull request: %+v", pulls)
			}
			for _, p := range pulls {
				if p.Head != task.BranchOf(x.ID) || p.Base != "main" || p.Title != "Export CSV" || !strings.Contains(p.Body, "#1") {
					t.Fatalf("from the task's branch into the default branch, for the issue: %+v", p)
				}
			}
			pr := r.get(x.ID).PR
			if pr == "" || !strings.Contains(r.g.CommentsOf(1)[0].Body, pr) {
				t.Fatalf("the task and the progress comment link it: %q %+v", pr, r.g.CommentsOf(1))
			}
		})
	}
}

func TestAPullRequestWaitsForPushedWorkThatIsDoneOrAwaitsAcceptance(t *testing.T) {
	st := task.New()
	var seq int64
	apply := func(typ string, data any) {
		t.Helper()
		seq++
		env := journal.Envelope{V: journal.Version, Seq: seq, At: time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC), Events: []journal.Event{journal.NewEvent(typ, data)}}
		if err := st.Apply(env); err != nil {
			t.Fatal(err)
		}
	}
	exit0 := 0
	flow := &task.Flow{Name: "feature", Stages: []task.Stage{{Name: "implement", Role: "implement"}, {Name: "accept", Gate: task.GateHuman}}}
	apply(task.ETaskCreated, task.Task{ID: "t1", Title: "one", Status: task.StatusTodo, Workflow: "feature", Flow: flow, Stage: "implement"})
	apply(task.ETaskStarted, task.TaskStart{IDs: []string{"t1"}})
	ws := &agent.Workspace{Checkout: "/src", Branch: "tend/t1", Base: "develop", Remote: "origin"}
	apply(task.ERunQueued, task.Run{ID: "r1", Task: "t1", Machine: "m", Agent: "fake", Dir: "/src", Stage: "implement", Work: ws})
	if _, _, ok := pullBranch(st, st.Tasks["t1"]); ok {
		t.Fatal("not while it runs")
	}
	apply(task.ERunObserved, task.Observation{ID: "r1", State: task.Exited, NodeRev: 1, ExitCode: &exit0, Work: &agent.Work{Head: "a1", Commits: 2}})
	apply(task.ETaskStaged, task.TaskStage{ID: "t1", Stage: "accept"})
	head, base, ok := pullBranch(st, st.Tasks["t1"])
	if !ok || head != "tend/t1" || base != "develop" {
		t.Fatalf("waiting for acceptance with pushed commits: %q %q %v", head, base, ok)
	}
	apply(task.ETaskCreated, task.Task{ID: "t2", Title: "local", Status: task.StatusTodo})
	apply(task.ERunQueued, task.Run{ID: "r2", Task: "t2", Machine: "m", Agent: "fake", Dir: "/src", Work: &agent.Workspace{Checkout: "/src", Branch: "tend/t2"}})
	apply(task.ERunObserved, task.Observation{ID: "r2", State: task.Exited, NodeRev: 1, ExitCode: &exit0, Work: &agent.Work{Head: "b1", Commits: 1}})
	apply(task.ETaskStatus, task.TaskStatus{ID: "t2", Status: task.StatusDone})
	if _, _, ok := pullBranch(st, st.Tasks["t2"]); ok {
		t.Fatal("a branch never pushed has nothing to open from")
	}
}
