package coord

import (
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
	"github.com/oxsean/fav/internal/workflow"
)

// TaskPlan is task.plan: a planner drafts the task's subtasks.
type TaskPlan struct {
	ID      string `json:"id"`
	Agent   string `json:"agent,omitempty"` // default: its project's planner
	Machine string `json:"machine,omitempty"`
}

// PlanSave is task.plan_save: someone's version of the task's draft; no plan drops it.
type PlanSave struct {
	ID          string     `json:"id"`
	Plan        *task.Plan `json:"plan,omitempty"`
	ExpectedRev int        `json:"expected_rev,omitempty"`
}

// PlanApply is task.plan_apply: the task's draft becomes its subtasks, all in the backlog.
type PlanApply struct {
	ID          string `json:"id"`
	ExpectedRev int    `json:"expected_rev,omitempty"`
}

// taskPlan queues task's planner: in its directory, read only, told to hand in a plan.
func (c *Coord) taskPlan(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p TaskPlan
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	if len(c.st.Children(t.ID)) > 0 {
		return "", nil, conflict("subtasks")
	}
	pr := c.st.Projects[t.Project]
	var roles map[string]string
	if pr != nil {
		roles = pr.Defaults.Roles
	}
	run, err := c.plan(who, Dispatch{Task: t.ID, Agent: firstOf(p.Agent, roles["planner"]), Machine: p.Machine,
		brief: workflow.PlanBrief(c.st, t), planning: true})
	if err != nil {
		return "", nil, err
	}
	if !c.canUse(who, run.Machine, run.Project) {
		return "", nil, forbidden("machine " + run.Machine)
	}
	run.Stage, run.Planner, run.Work, run.Runner = task.StagePlan, true, nil, node.RunnerBackground
	readOnly(&run.Profile)
	return t.ID, []journal.Event{journal.NewEvent(task.ERunQueued, run)}, nil
}

func (c *Coord) planSave(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p PlanSave
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	if p.ExpectedRev != 0 && p.ExpectedRev != t.Rev {
		return "", nil, conflict("rev")
	}
	if p.Plan != nil {
		if err := p.Plan.Check(); err != nil {
			return "", nil, bad(err.Error())
		}
	}
	return t.ID, []journal.Event{journal.NewEvent(task.EPlanDrafted, task.PlanDraft{ID: t.ID, Plan: p.Plan, By: who.User})}, nil
}

// planApply makes the task's draft its subtasks: in the backlog, owned as it is, their workflows resolved in its
// project, parents before children.
func (c *Coord) planApply(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p PlanApply
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	d := t.Draft
	switch {
	case d == nil || d.Plan == nil:
		return "", nil, conflict("no draft")
	case p.ExpectedRev != 0 && p.ExpectedRev != t.Rev:
		return "", nil, conflict("rev")
	case task.Finished(t.Status):
		return "", nil, conflict("task " + t.Status)
	case len(c.st.Children(t.ID)) > 0:
		return "", nil, conflict("subtasks")
	case t.Source != nil && (task.SourceWaits(t) == task.WhySourceChanged || d.SourceRev != t.Source.Rev):
		return "", nil, conflict(task.WhySourceChanged)
	case c.st.Depth(t.ID)+d.Plan.Depth() > task.MaxDepth:
		return "", nil, bad("the plan is too deep for where the task is")
	}
	if err := d.Plan.Check(); err != nil {
		return "", nil, bad(err.Error())
	}
	pr := c.st.Projects[t.Project]
	ids := map[string]string{}
	var events []journal.Event
	for _, x := range d.Plan.Ordered() {
		wf, flow, err := c.flowOf(t.Project, x.Workflow)
		if err != nil {
			return "", nil, err
		}
		kid := task.Task{ID: newID("t_"), Title: x.Title, Brief: x.Brief, Accept: x.Accept, Project: t.Project, Owner: t.Owner,
			Approver: t.Approver, Parent: firstOf(ids[x.Parent], t.ID), Status: task.StatusBacklog, Workflow: wf, Flow: flow, Dir: t.Dir,
			Machine: t.Machine}
		if flow != nil {
			kid.Stage = flow.Stages[0].Name
		}
		if x.Machine != "" && c.checkMachine(x.Machine) == nil {
			kid.Machine = x.Machine
		}
		if pr != nil && x.Role != "" {
			kid.Agent = pr.Defaults.Roles[x.Role]
		}
		for _, a := range x.After {
			kid.After = append(kid.After, ids[a])
		}
		ids[x.Key] = kid.ID
		events = append(events, journal.NewEvent(task.ETaskCreated, kid))
	}
	return t.ID, append(events, journal.NewEvent(task.EPlanApplied, task.PlanApplied{ID: t.ID})), nil
}
