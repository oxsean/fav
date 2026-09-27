package coord

import (
	"testing"

	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// needFeature makes every run need f for the length of the test.
func needFeature(t *testing.T, f string) {
	old := runFeatures
	runFeatures = func(*task.Run) []string { return []string{f} }
	t.Cleanup(func() { runFeatures = old })
}

func TestARunANodeCannotHonourFailsAsOutdated(t *testing.T) {
	needFeature(t, "worktree")
	e := newEnv(t, tend.Config{})
	e.start()
	tk := e.task("needs a worktree", "quick")
	var pv Preview
	e.must(MRunPreview, Dispatch{Task: tk.ID, Runner: node.RunnerBackground}, &pv)
	if !hasWhy(pv.Blockers, WhyOutdated) {
		t.Fatalf("the preview says the node is outdated: %+v", pv)
	}
	r := e.wait(e.dispatch(Dispatch{Task: tk.ID}).ID, func(r *task.Run) bool { return !task.Open(r.State) })
	if r.State != task.Failed || r.Reason != ReasonNodeOutdated || r.Detail != "worktree" {
		t.Fatalf("started without what it needs: %+v", r)
	}
}

func TestARunStartsWhereItsFeaturesAre(t *testing.T) {
	needFeature(t, "worktree")
	old := node.Features
	node.Features = []string{"worktree"}
	t.Cleanup(func() { node.Features = old })
	e := newEnv(t, tend.Config{})
	e.start()
	tk := e.task("has a worktree", "quick")
	r := e.wait(e.dispatch(Dispatch{Task: tk.ID}).ID, func(r *task.Run) bool { return !task.Open(r.State) })
	if r.State != task.Exited {
		t.Fatalf("%+v", r)
	}
}

func hasWhy(ws []Why, code string) bool {
	for _, w := range ws {
		if w.Code == code {
			return true
		}
	}
	return false
}
