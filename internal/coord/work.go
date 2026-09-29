package coord

import (
	"slices"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// workspace is the worktree task t works in from checkout of repo: its own branch, made from its ancestors'. The
// caller holds mu.
func (c *Coord) workspace(t *task.Task, pr *task.Project, repo task.Repo, checkout string) *agent.Workspace {
	return &agent.Workspace{Checkout: checkout, Branch: task.BranchOf(t.ID), Chain: c.chain(t), Base: repo.Base, Remote: repo.Remote,
		Setup: pr.Hooks["setup"], BeforeRun: pr.Hooks["before_run"]}
}

// chain are the branches of t's ancestors, outermost first. The caller holds mu.
func (c *Coord) chain(t *task.Task) []string {
	var out []string
	for p := c.st.Tasks[t.Parent]; p != nil && len(out) < 8; p = c.st.Tasks[p.Parent] {
		out = append([]string{task.BranchOf(p.ID)}, out...)
	}
	return out
}

// workMachine is where t's run goes when its tree's branches live on one machine only (a repository with worktrees
// and no remote): that machine; otherwise machine. asked is the machine the caller named. The caller holds mu.
func (c *Coord) workMachine(t *task.Task, pr *task.Project, machine, asked string) (string, error) {
	on := c.st.WorkOn(t)
	if t.Dir != "" || on == "" || on == machine {
		return machine, nil
	}
	repo, ok := pr.RepoOn(on)
	switch {
	case !ok || !repo.Worktrees || repo.Remote != "":
		return machine, nil
	case asked != "":
		return "", bad("its branch is on " + on + " and repository " + repo.Name + " has no remote")
	}
	return on, nil
}

// readOnly takes the ways to change files from a reviewer's profile.
func readOnly(p *tend.AgentProfile) {
	switch p.Provider {
	case tend.ProviderClaude:
		for _, tool := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit"} {
			if !slices.Contains(p.Deny, tool) {
				p.Deny = append(slices.Clone(p.Deny), tool)
			}
		}
	case tend.ProviderCodex:
		p.Permission = "read-only"
	}
}

// reviewer is readOnly for a review run; on a throwaway copy (copy) a codex reviewer may write it, so it can build and
// run the tests, which its read-only sandbox refuses.
func reviewer(p *tend.AgentProfile, copy bool) {
	readOnly(p)
	if copy && p.Provider == tend.ProviderCodex {
		p.Permission = "workspace-write"
	}
}

// finish is what ends t's work: done, or first a merge of its branch into its parent's. The caller holds mu.
func (c *Coord) finish(t *task.Task) []journal.Event {
	if !c.st.NeedsMerge(t) {
		return []journal.Event{journal.NewEvent(task.ETaskStatus, task.TaskStatus{ID: t.ID, Status: task.StatusDone})}
	}
	run, err := c.mergeRun(t)
	if err != nil {
		return []journal.Event{journal.NewEvent(task.ETaskHeld, holdOf(t.ID, err))}
	}
	return []journal.Event{journal.NewEvent(task.ERunQueued, run)}
}

// mergeRun is the run that merges t's branch into its parent's, on the machine that made it, as t's owner. The
// caller holds mu.
func (c *Coord) mergeRun(t *task.Task) (task.Run, error) {
	parent, pr := c.st.Tasks[t.Parent], c.st.Projects[t.Project]
	machine := firstOf(t.WorkOn, c.st.WorkOn(t))
	repo, ok := pr.RepoOn(machine)
	if !ok || parent == nil {
		return task.Run{}, bad("dir")
	}
	who, ok := c.principal(t.Owner)
	if !ok {
		return task.Run{}, forbidden("owner " + t.Owner)
	}
	if err := c.checkMachine(machine); err != nil {
		return task.Run{}, err
	}
	if !c.canUse(who, machine, t.Project) {
		return task.Run{}, forbidden("machine " + machine)
	}
	dir := repo.Dirs[machine]
	return task.Run{ID: node.NewRunID(), Task: t.ID, Machine: machine, Agent: "git", Dir: dir, From: machine, Title: t.Title,
		Runner: node.RunnerBackground, Project: t.Project, Dispatcher: who.User, Stage: task.StageMerge,
		Work: &agent.Workspace{Checkout: dir, Branch: task.BranchOf(parent.ID), Chain: c.chain(parent), Base: repo.Base,
			Remote: repo.Remote, Merge: t.Branch, Cleanup: pr.Hooks["cleanup"]}}, nil
}

// taskMerge is task.merge: merge the task's branch into its parent's again, after a conflict was resolved.
func (c *Coord) taskMerge(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p TaskRef
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	switch {
	case t.Status != task.StatusTodo || !c.st.NeedsMerge(t):
		return "", nil, conflict("nothing to merge")
	case c.st.OpenRun(t.ID) != nil:
		return "", nil, conflict("open run")
	}
	run, err := c.mergeRun(t)
	if err != nil {
		return "", nil, err
	}
	return t.ID, []journal.Event{journal.NewEvent(task.ERunQueued, run)}, nil
}
