package task

import (
	"slices"
	"strings"
)

// StageMerge is the stage of a run that merges a task's branch into its parent's; it runs no agent.
const StageMerge = "merge"

// Why a task that works on a branch stands where it does.
const (
	WhyMergeConflict = "merge_conflict" // waiting: merging its branch into its parent's stopped on conflicts
	WhyStale         = "stale"          // queued: its stage's verdict is about an older commit; the stage runs again
	WhyUndone        = "undone"         // run_canceled: an undo took back the done that queued it
)

// BranchOf is the branch task id works on.
func BranchOf(id string) string { return "tend/" + id }

// NeedsMerge: t's work is on a branch that is not in its parent's yet.
func (s *State) NeedsMerge(t *Task) bool {
	return t.Branch != "" && t.Parent != "" && !t.Merged && s.Tasks[t.Parent] != nil
}

// WorkOn is the machine that holds t's tree's branches: its own, else its nearest ancestor's; "" before any.
func (s *State) WorkOn(t *Task) string {
	for i := 0; t != nil && i < 8; i++ {
		if t.WorkOn != "" {
			return t.WorkOn
		}
		t = s.Tasks[t.Parent]
	}
	return ""
}

// queuedWork records what queued run r's worktree means: its task and those whose branches it makes have branches
// now, kept on its machine.
func (s *State) queuedWork(r *Run) {
	w := r.Work
	if w == nil || w.ReadOnly || w.Merge != "" {
		return
	}
	for _, b := range append(slices.Clone(w.Chain), w.Branch) {
		if t := s.Tasks[strings.TrimPrefix(b, "tend/")]; t != nil {
			if t.Branch == "" {
				t.Branch = b
			}
			if t.WorkOn == "" {
				t.WorkOn = r.Machine
			}
		}
	}
}

// worked records what run r did to its task's branch once it ended: the branch's head, or that it was merged.
func (s *State) worked(r *Run) {
	t, w := s.Tasks[r.Task], r.Worked
	if t == nil || w == nil || r.Work == nil {
		return
	}
	switch {
	case r.Work.Merge != "":
		if w.Merged {
			t.Merged = true
			if p := s.Tasks[t.Parent]; p != nil && w.Head != "" {
				p.Head = w.Head
			}
		}
	case !r.Work.ReadOnly && w.Head != "":
		t.Head = w.Head
	}
}

// mergeSituation is where t stands when its latest run, last, was a merge that ended.
func mergeSituation(last *Run) Situation {
	switch {
	case last.Worked != nil && last.Worked.Merged:
		return Situation{Kind: SitQueued, Reason: WhyCompleting, Run: last.ID}
	case last.Reason == WhyMergeConflict:
		return Situation{Kind: SitWaiting, Reason: WhyMergeConflict, Run: last.ID}
	case last.Reason != "":
		return Situation{Kind: SitWaiting, Reason: last.Reason, Run: last.ID}
	}
	return Situation{Kind: SitWaiting, Reason: last.State, Run: last.ID}
}

// stale: verdict run r judged a commit its task's branch has moved on from.
func stale(t *Task, r *Run) bool {
	return r.Worked != nil && r.Worked.Head != "" && t.Head != "" && r.Worked.Head != t.Head
}
