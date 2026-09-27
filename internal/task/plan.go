package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/journal"
)

// Plan is a planner's proposal of subtasks for a task; keys name its tasks within it.
type Plan struct {
	Tasks     []PlanTask `json:"tasks"`
	Questions []string   `json:"questions,omitempty"` // what the planner wants someone to decide
}

type PlanTask struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Brief    string   `json:"brief,omitempty"`
	Accept   []string `json:"acceptance,omitempty"`
	After    []string `json:"after,omitempty"`  // keys of tasks it comes after
	Parent   string   `json:"parent,omitempty"` // the key of the task it is part of: a plan is two levels at most
	Workflow string   `json:"workflow,omitempty"`
	Role     string   `json:"role_hint,omitempty"`
	Machine  string   `json:"machine_hint,omitempty"`
	Size     string   `json:"size,omitempty"` // S | M | L
}

// Limits of a plan.
const (
	maxPlanTasks = 50
	maxPlanBytes = 256 << 10
	maxPlanTitle = 1 << 10
)

var planKey = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// ParsePlan reads a plan and checks it.
func ParsePlan(b []byte) (*Plan, error) {
	if len(b) > maxPlanBytes {
		return nil, fmt.Errorf("a plan is %d KiB at most", maxPlanBytes>>10)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var p Plan
	if err := d.Decode(&p); err != nil {
		return nil, fmt.Errorf("not a plan: %w", err)
	}
	return &p, p.Check()
}

// Check says what is wrong with p.
func (p *Plan) Check() error {
	var errs []string
	switch n := len(p.Tasks); {
	case n == 0:
		return errors.New("a plan has tasks")
	case n > maxPlanTasks:
		return fmt.Errorf("a plan has %d tasks at most", maxPlanTasks)
	}
	byKey := map[string]*PlanTask{}
	for i := range p.Tasks {
		x := &p.Tasks[i]
		x.Title = strings.TrimSpace(x.Title)
		switch {
		case !planKey.MatchString(x.Key):
			errs = append(errs, fmt.Sprintf("key %q: lowercase letters, digits, - and _", x.Key))
		case byKey[x.Key] != nil:
			errs = append(errs, "key "+x.Key+" twice")
		case x.Title == "" || len(x.Title) > maxPlanTitle:
			errs = append(errs, x.Key+": a title of 1 to 1024 bytes")
		case x.Size != "" && !slices.Contains([]string{"S", "M", "L"}, x.Size):
			errs = append(errs, x.Key+": size is S, M or L")
		}
		byKey[x.Key] = x
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	under := func(x *PlanTask, key string) bool { // key is x or one of x's ancestors
		for ; x != nil; x = byKey[x.Parent] {
			if x.Key == key {
				return true
			}
		}
		return false
	}
	for i := range p.Tasks {
		x := &p.Tasks[i]
		if x.Parent != "" {
			switch parent := byKey[x.Parent]; {
			case parent == nil:
				errs = append(errs, x.Key+": no task "+x.Parent+" to be part of")
			case parent.Parent != "":
				errs = append(errs, x.Key+": a plan is two levels at most")
			}
		}
		for _, a := range x.After {
			switch other := byKey[a]; {
			case other == nil:
				errs = append(errs, x.Key+": no task "+a+" to come after")
			case under(x, a) || under(other, x.Key):
				errs = append(errs, x.Key+": it cannot come after "+a+", which it is part of or holds")
			}
		}
	}
	if len(errs) == 0 && len(p.Ordered()) < len(p.Tasks) {
		errs = append(errs, "the tasks' after make a cycle")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Ordered are p's tasks with each after its parent and what it comes after, otherwise in p's order; a cycle leaves
// its tasks out.
func (p *Plan) Ordered() []PlanTask {
	done := map[string]bool{}
	var out []PlanTask
	for progress := true; progress && len(out) < len(p.Tasks); {
		progress = false
		for _, x := range p.Tasks {
			if done[x.Key] || x.Parent != "" && !done[x.Parent] || slices.ContainsFunc(x.After, func(a string) bool { return !done[a] }) {
				continue
			}
			done[x.Key], progress = true, true
			out = append(out, x)
		}
	}
	return out
}

// Depth is how many levels p's tasks make.
func (p *Plan) Depth() int {
	for _, x := range p.Tasks {
		if x.Parent != "" {
			return 2
		}
	}
	return 1
}

// Plan events.
const (
	EPlanDrafted = "plan_drafted" // PlanDraft: someone saved or dropped a task's draft
	EPlanApplied = "plan_applied" // PlanApplied: its draft became subtasks (task_created before it in the same envelope)
)

// StagePlan is the stage of a planner's run: it hands in a plan (tend run plan) and changes nothing.
const StagePlan = "plan"

// Why a task being planned stands where it does.
const (
	WhyDraft  = "draft"   // waiting: a planner's draft waits for someone to confirm, change or drop it
	WhyNoPlan = "no_plan" // waiting: its planner ended without a plan
)

// Draft is a plan for a task that is not applied yet.
type Draft struct {
	Plan      *Plan  `json:"plan"`
	Run       string `json:"run,omitempty"`        // the planner's run it came from
	SourceRev int    `json:"source_rev,omitempty"` // the revision of the task's issue it was made for
	By        string `json:"by,omitempty"`         // who last changed it
}

type PlanDraft struct {
	ID   string `json:"id"`
	Plan *Plan  `json:"plan,omitempty"` // nil drops the draft
	By   string `json:"by,omitempty"`
}

type PlanApplied struct {
	ID string `json:"id"`
}

// drafted makes planner run r's plan its task's draft once the run ended.
func (s *State) drafted(r *Run) {
	t := s.Tasks[r.Task]
	if t == nil || !r.Planner || r.Plan == nil {
		return
	}
	t.Draft = &Draft{Plan: r.Plan, Run: r.ID}
	if t.Source != nil {
		t.Draft.SourceRev = t.Source.Rev
	}
}

// planSituation is where t stands when its latest run, last, was its planner's.
func planSituation(t *Task, last *Run) Situation {
	switch {
	case t.Draft != nil:
		return Situation{Kind: SitWaiting, Reason: WhyDraft, Run: last.ID}
	case last.Waiting():
		return Situation{Kind: SitWaiting, Reason: last.Attention, Run: last.ID}
	case last.Reason != "" && last.State != Exited:
		return Situation{Kind: SitWaiting, Reason: last.Reason, Run: last.ID}
	}
	return Situation{Kind: SitWaiting, Reason: WhyNoPlan, Run: last.ID}
}

func (s *State) applyPlan(e journal.Event, at time.Time) (bool, error) {
	switch e.Type {
	case EPlanDrafted:
		var d PlanDraft
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		t := s.Tasks[d.ID]
		if t == nil {
			return true, fmt.Errorf("no task %s", d.ID)
		}
		if d.Plan == nil {
			t.Draft = nil
		} else {
			t.Draft = &Draft{Plan: d.Plan, By: d.By}
			if t.Source != nil {
				t.Draft.SourceRev = t.Source.Rev
			}
		}
		t.Rev++
		t.UpdatedAt = at
	case EPlanApplied:
		var d PlanApplied
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		t := s.Tasks[d.ID]
		if t == nil {
			return true, fmt.Errorf("no task %s", d.ID)
		}
		t.Draft = nil
		t.Rev++
		t.UpdatedAt = at
	default:
		return false, nil
	}
	return true, nil
}
