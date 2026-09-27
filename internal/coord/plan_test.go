package coord

import (
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

const testPlan = `{"tasks":[{"key":"model","title":"Model","brief":"add the model"},
	{"key":"api","title":"API","brief":"serve it","after":["model"]},
	{"key":"api-docs","title":"API docs","parent":"api","workflow":"docs"}],"questions":["CSV or TSV?"]}`

func TestAPlannerDraftsSubtasksThatBecomeABacklog(t *testing.T) {
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{{Name: "planner", Provider: agent.ProviderFake,
		Args: []string{"--steps", "1", "--every", "50ms", "--plan", testPlan}}}})
	e.start()
	e.must(MProjectCreate, ProjectCreate{ID: "p1", Name: "One"}, nil)
	e.must(MProjectEdit, task.ProjectEdit{ID: "p1", Defaults: &task.Defaults{Roles: map[string]string{"planner": "planner", "implement": "quick"}}}, nil)
	req := e.create(TaskCreate{Title: "export", Brief: "Export the list.", Project: "p1", Kind: task.KindRequirement, Status: task.StatusBacklog, Agent: "quick"})
	e.must(MTaskPlan, TaskPlan{ID: req.ID}, nil)
	st := e.until("a draft waits", func(s *task.State) bool { return s.Tasks[req.ID].Draft != nil })
	x := st.Tasks[req.ID]
	planner := st.Runs[x.Draft.Run]
	if x.Draft == nil || len(x.Draft.Plan.Tasks) != 3 || x.Draft.Plan.Questions[0] != "CSV or TSV?" || planner == nil || !planner.Planner ||
		planner.Stage != task.StagePlan || planner.Agent != "planner" {
		t.Fatalf("the planner's plan is the draft: %+v %+v", x.Draft, planner)
	}
	if x.Status != task.StatusBacklog || len(st.Children(req.ID)) != 0 {
		t.Fatal("a draft makes no task")
	}

	var answered task.Run
	e.must(MRunContinue, Continue{Run: planner.ID, Text: "TSV too"}, &answered)
	st = e.until("drafted again", func(s *task.State) bool { d := s.Tasks[req.ID].Draft; return d != nil && d.Run == answered.ID })
	if r := st.Runs[answered.ID]; !r.Planner || r.Stage != task.StagePlan || r.Resume == "" {
		t.Fatalf("an answer goes on in the planner's session: %+v", r)
	}
	x = st.Tasks[req.ID]
	edited := *x.Draft.Plan
	edited.Tasks = append([]task.PlanTask(nil), edited.Tasks...)
	edited.Tasks[0].Title = "Data model"
	bad := edited
	bad.Tasks = append([]task.PlanTask(nil), edited.Tasks...)
	bad.Tasks[0].After = []string{"api"}
	if err := e.call(MTaskPlanSave, PlanSave{ID: req.ID, Plan: &bad}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("a cycle is refused: %v", err)
	}
	e.must(MTaskPlanSave, PlanSave{ID: req.ID, Plan: &edited, ExpectedRev: x.Rev}, nil)
	if err := e.call(MTaskPlanApply, PlanApply{ID: req.ID, ExpectedRev: x.Rev}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("applying a draft someone changed since is refused: %v", err)
	}
	var root task.Task
	e.must(MTaskPlanApply, PlanApply{ID: req.ID}, &root)
	st = e.c.State()
	kids := map[string]*task.Task{}
	for _, k := range st.Subtree(req.ID)[1:] {
		kids[k.Title] = k
	}
	model, api, docs := kids["Data model"], kids["API"], kids["API docs"]
	if root.Draft != nil || len(kids) != 3 || model == nil || api == nil || docs == nil || model.Parent != req.ID || docs.Parent != api.ID ||
		len(api.After) != 1 || api.After[0] != model.ID || docs.Workflow != "docs" || model.Status != task.StatusBacklog || model.Owner != req.Owner {
		t.Fatalf("the draft became the subtree: %+v", kids)
	}
	if err := e.call(MTaskPlan, TaskPlan{ID: req.ID}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("a task with subtasks is not planned again: %v", err)
	}
	e.must(MTaskStart, TaskRef{ID: req.ID}, nil)
	e.until("the model is dispatched first", func(s *task.State) bool {
		return s.OpenRun(model.ID) != nil || s.Tasks[model.ID].Status == task.StatusDone
	})
}

func TestADraftIsBoundToItsIssuesRevision(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	sys, as := e.as(System), e.as(ann)
	issue := TaskSync{Project: "p1", Kind: "gitea", Tracker: "tr1", Base: "http://git", Repo: "o/r", RepoID: 9, Number: 4,
		Title: "Export", Text: "rows as CSV", Digest: "d1", Owner: ann.User}
	var req task.Task
	if err := callAs(sys, MTaskSync, "s1", issue, &req); err != nil {
		t.Fatal(err)
	}
	plan, _ := task.ParsePlan([]byte(testPlan))
	if err := callAs(as, MTaskPlanSave, "p1", PlanSave{ID: req.ID, Plan: plan}, nil); err != nil {
		t.Fatal(err)
	}
	issue.Digest, issue.Text = "d2", "rows as CSV or TSV"
	if err := callAs(sys, MTaskSync, "s2", issue, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(as, MTaskPlanApply, "a1", PlanApply{ID: req.ID}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("the issue changed: the draft waits: %v", err)
	}
	if err := callAs(as, MTaskSourceAck, "k1", task.SourceAck{ID: req.ID, Accept: true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(as, MTaskPlanApply, "a2", PlanApply{ID: req.ID}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("a draft for revision 1 is not applied to revision 2: %v", err)
	}
	if err := callAs(as, MTaskPlanSave, "p2", PlanSave{ID: req.ID, Plan: plan}, nil); err != nil {
		t.Fatal(err)
	}
	var root task.Task
	if err := callAs(as, MTaskPlanApply, "a3", PlanApply{ID: req.ID}, &root); err != nil || root.Draft != nil {
		t.Fatalf("saved again for revision 2, it applies: %v", err)
	}
}
