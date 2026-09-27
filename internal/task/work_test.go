package task

import (
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
)

func TestAVerdictOnAnOlderHeadIsJudgedAgain(t *testing.T) {
	w := newWorld(t)
	w.do(journal.NewEvent(ETaskCreated, Task{ID: "t_p", Title: "p", Status: StatusTodo, Workflow: "feature", Flow: testFlow(), Stage: "implement"}))
	w.do(journal.NewEvent(ETaskStarted, TaskStart{IDs: []string{"t_p"}}))
	p := w.s.Tasks["t_p"]
	work := func(ro bool) *agent.Workspace {
		return &agent.Workspace{Checkout: "/src", Branch: "tend/t_p", ReadOnly: ro}
	}
	w.do(journal.NewEvent(ERunQueued, Run{ID: "r_i", Task: "t_p", Machine: "m", Stage: "implement", Work: work(false)}))
	if p.Branch != "tend/t_p" || p.WorkOn != "m" {
		t.Fatalf("a queued run gives its task a branch: %+v", p)
	}
	w.endWith("r_i", Observation{ID: "r_i", State: Exited, NodeRev: 9, Work: &agent.Work{Head: "h1"}}, 0)
	w.do(journal.NewEvent(ETaskStaged, TaskStage{ID: "t_p", Stage: "review"}))
	w.do(journal.NewEvent(ERunQueued, Run{ID: "r_r", Task: "t_p", Machine: "m", Stage: "review", Judge: true, Work: work(true)}))
	w.endWith("r_r", Observation{ID: "r_r", State: Exited, NodeRev: 9, Verdict: &agent.Verdict{Verdict: agent.VerdictPass},
		Work: &agent.Work{Head: "h1", Discarded: 2}}, 0)
	if p.Head != "h1" || w.s.Situation(p).Reason != WhyAdvance {
		t.Fatalf("a review of the head passes: %+v %+v", p, w.s.Situation(p))
	}

	w.do(journal.NewEvent(ETaskCreated, Task{ID: "t_c", Title: "c", Parent: "t_p", Status: StatusTodo, Branch: ""}))
	w.do(journal.NewEvent(ERunQueued, Run{ID: "r_c", Task: "t_c", Machine: "m",
		Work: &agent.Workspace{Checkout: "/src", Branch: "tend/t_c", Chain: []string{"tend/t_p"}}}))
	w.endWith("r_c", Observation{ID: "r_c", State: Exited, NodeRev: 9, Work: &agent.Work{Head: "c1"}}, 0)
	c := w.s.Tasks["t_c"]
	if !w.s.NeedsMerge(c) || c.Head != "c1" {
		t.Fatalf("its work waits to be merged: %+v", c)
	}
	w.do(journal.NewEvent(ERunQueued, Run{ID: "r_m", Task: "t_c", Machine: "m", Stage: StageMerge,
		Work: &agent.Workspace{Checkout: "/src", Branch: "tend/t_p", Merge: "tend/t_c"}}))
	w.endWith("r_m", Observation{ID: "r_m", State: Exited, NodeRev: 9, Reason: WhyMergeConflict, Work: &agent.Work{Conflict: []string{"a.go"}}}, 1)
	if sit := w.s.Situation(c); sit.Reason != WhyMergeConflict || w.s.Situation(p).Reason != WhyChildren {
		t.Fatalf("a conflict waits for someone: %+v", sit)
	}
	w.do(journal.NewEvent(ERunQueued, Run{ID: "r_m2", Task: "t_c", Machine: "m", Stage: StageMerge,
		Work: &agent.Workspace{Checkout: "/src", Branch: "tend/t_p", Merge: "tend/t_c"}}))
	w.endWith("r_m2", Observation{ID: "r_m2", State: Exited, NodeRev: 9, Work: &agent.Work{Merged: true, Head: "h2"}}, 0)
	if !c.Merged || p.Head != "h2" || w.s.Situation(c).Reason != WhyCompleting {
		t.Fatalf("merged, it is done next and its parent's head moved: %+v %+v", c, p)
	}
	w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: "t_c", Status: StatusDone}))
	if sit := w.s.Situation(p); sit.Reason != WhyStale || !slices.Contains(w.s.Ready(), p) {
		t.Fatalf("the review saw h1, the branch is at h2: it runs again: %+v", sit)
	}
}
