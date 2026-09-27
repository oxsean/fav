package coord

import (
	"encoding/json"
	"strings"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
	"github.com/oxsean/fav/internal/workflow"
)

// NoWorkflow names no workflow where a project's default would apply.
const NoWorkflow = "none"

// TaskGate is task.gate: a human gate's decision. Pass (its approver only) moves the task on; otherwise it goes back
// with Notes. ExpectedRev, when set, is the task's rev the decision was made on.
type TaskGate struct {
	ID          string `json:"id"`
	Pass        bool   `json:"pass,omitempty"`
	Notes       string `json:"notes,omitempty"`
	ExpectedRev int    `json:"expected_rev,omitempty"`
}

// TaskMessage is task.message: words for a task, which go where its state takes them.
type TaskMessage struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	ToRun bool   `json:"to_run,omitempty"` // into a reviewer's or tester's current turn too
}

// Where a task message went.
const (
	MessageToRun     = "run"     // into the running run's turn
	MessageToReply   = "reply"   // a reply that continues the run that waits
	MessageToWorkpad = "workpad" // kept for the next stage
)

// MessageResult says where a message went.
type MessageResult struct {
	To  string `json:"to"`
	Run string `json:"run,omitempty"`
}

// flowOf is the workflow a task of project gets: name ("" takes the project's default, NoWorkflow none). The caller
// holds mu.
func (c *Coord) flowOf(project, name string) (string, *task.Flow, error) {
	pr := c.st.Projects[project]
	if name == "" && pr != nil {
		name = pr.Defaults.Workflow
	}
	if name == "" || name == NoWorkflow {
		return "", nil, nil
	}
	var custom map[string]string
	if pr != nil {
		custom = pr.Workflows
	}
	f, err := workflow.Resolve(name, custom)
	if err != nil {
		return "", nil, bad(err.Error())
	}
	return name, &f, nil
}

// checkWorkflows: p's workflows read, and its default names one there is. The caller holds mu.
func (c *Coord) checkWorkflows(pr *task.Project, p task.ProjectEdit) error {
	custom := pr.Workflows
	if p.Workflows != nil {
		custom = *p.Workflows
		if len(custom) > 20 {
			return bad("workflows")
		}
		for name, text := range custom {
			if len(text) > maxBrief {
				return bad("workflow " + name)
			}
			if _, err := workflow.Resolve(name, custom); err != nil {
				return bad(err.Error())
			}
		}
	}
	if d := p.Defaults; d != nil && d.Workflow != "" && d.Workflow != NoWorkflow {
		if _, err := workflow.Resolve(d.Workflow, custom); err != nil {
			return bad(err.Error())
		}
	}
	return nil
}

// agentForStage is the agent stage runs with: its own, else the project's for its role, else the task's. The caller
// holds mu.
func (c *Coord) agentForStage(t *task.Task, st *task.Stage) string {
	pr := c.st.Projects[t.Project]
	if st.Agent != "" {
		return st.Agent
	}
	if pr != nil && pr.Defaults.Roles[st.Role] != "" {
		return pr.Defaults.Roles[st.Role]
	}
	if st.Role == "implement" || st.Role == "" {
		return firstOf(t.Agent, pr.Agent())
	}
	return t.Agent
}

// stagePlan is the run of t's current stage: its agent, machine and brief; the stage's check hook; a verdict when
// the stage wants one. An implementing stage sent back continues its last session when it can. The caller holds mu.
func (c *Coord) stagePlan(who Principal, t *task.Task) (task.Run, error) {
	st := t.Flow.StageOf(t.Stage)
	if st == nil {
		return task.Run{}, bad("stage " + t.Stage)
	}
	brief := workflow.Brief(c.st, t)
	run, err := c.plan(who, Dispatch{Task: t.ID, Agent: c.agentForStage(t, st), Machine: st.Machine, brief: brief})
	if err != nil {
		return task.Run{}, err
	}
	run.Stage, run.Judge = t.Stage, st.Output == task.OutputVerdict
	if run.Work != nil && run.Judge {
		run.Work.ReadOnly, run.Work.Setup = true, nil
	}
	if st.Role == "review" {
		readOnly(&run.Profile)
	}
	if pr := c.st.Projects[t.Project]; st.Check && pr != nil && len(pr.Hooks["check"]) > 0 {
		run.Check = pr.Hooks["check"]
	}
	if t.Loops > 0 && st.Role == "implement" {
		if prev := c.lastOfStage(t, st.Name); prev != nil && prev.Session != "" && prev.Profile.Provider == run.Profile.Provider &&
			prev.Machine == run.Machine && c.canContinue(run.Profile, run.Machine) == nil {
			run.Resume, run.Parent, run.Runner = prev.Session, prev.ID, node.RunnerBackground
			run.Brief = strings.TrimSpace(workflow.ReworkNotes(c.st, t)) + "\n\n" + workflow.Workpad(c.st, t)
		}
	}
	return run, nil
}

// lastOfStage is t's latest run of stage; nil when none. The caller holds mu.
func (c *Coord) lastOfStage(t *task.Task, stage string) *task.Run {
	var last *task.Run
	for _, r := range c.st.Runs {
		if r.Task == t.ID && r.Stage == stage && (last == nil || r.Seq > last.Seq) {
			last = r
		}
	}
	return last
}

// stageMoves are what the coordinator writes for the tasks whose stage is done (on to the next, or done after the
// last) and those a stage sent back (to where it sends them, with why). The caller holds mu.
func (c *Coord) stageMoves() []journal.Event {
	var events []journal.Event
	for _, t := range c.st.Advancing() {
		if next := t.Flow.Next(t.Stage); next != "" {
			events = append(events, journal.NewEvent(task.ETaskStaged, task.TaskStage{ID: t.ID, Stage: next, Loops: t.Loops}))
		} else {
			events = append(events, c.finish(t)...)
		}
	}
	for _, t := range c.st.Reworking() {
		why := "rework"
		if r := c.st.Runs[c.st.Situation(t).Run]; r != nil {
			switch {
			case r.Checked != nil && r.Checked.Exit != 0:
				why = "check `" + strings.Join(r.Checked.Argv, " ") + "` failed:\n" + r.Checked.Tail
			case r.Verdict != nil:
				why = r.Verdict.Summary
			}
		}
		events = append(events,
			journal.NewEvent(task.ETaskNoted, task.TaskNote{ID: t.ID, Note: task.Note{Stage: t.Stage, Kind: task.NoteRework, Text: clip(why, 4000)}}),
			journal.NewEvent(task.ETaskStaged, task.TaskStage{ID: t.ID, Stage: t.Flow.Back(t.Stage), Loops: t.Loops + 1, Back: true}))
	}
	return events
}

func (c *Coord) taskGate(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p TaskGate
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	st := t.Flow.StageOf(t.Stage)
	switch {
	case st == nil || st.Gate != task.GateHuman || task.Finished(t.Status):
		return "", nil, conflict("no gate")
	case p.ExpectedRev != 0 && p.ExpectedRev != t.Rev:
		return "", nil, conflict("rev")
	case p.Pass && who.User != firstOf(t.Approver, t.Owner) && !who.Admin && c.team():
		return "", nil, forbidden("only its approver passes it")
	case len(p.Notes) > maxMessage:
		return "", nil, bad("notes")
	}
	events := []journal.Event{}
	if strings.TrimSpace(p.Notes) != "" || !p.Pass {
		verdict := "pass"
		if !p.Pass {
			verdict = "rework"
		}
		events = append(events, journal.NewEvent(task.ETaskNoted, task.TaskNote{ID: t.ID, Note: task.Note{Stage: t.Stage, Kind: task.NoteGate,
			Text: strings.TrimSpace(verdict + ": " + p.Notes), By: who.User}}))
	}
	switch next := t.Flow.Next(t.Stage); {
	case !p.Pass:
		events = append(events, journal.NewEvent(task.ETaskStaged, task.TaskStage{ID: t.ID, Stage: t.Flow.Back(t.Stage), Loops: t.Loops + 1, Back: true}))
	case next != "":
		events = append(events, journal.NewEvent(task.ETaskStaged, task.TaskStage{ID: t.ID, Stage: next, Loops: t.Loops}))
	default:
		if task.SourceWaits(t) == task.WhySourceChanged {
			return "", nil, conflict(task.WhySourceChanged)
		}
		events = append(events, c.finish(t)...)
	}
	return t.ID, events, nil
}

// taskMessage sends words to a task where its state takes them: into an implementing run's turn (or any run's, with
// ToRun), as the reply a waiting run needs, else onto the workpad for the next stage. The caller holds mu.
//
// Its answer is "<where>|<run>" (messageView reads it back), as command wants an id.
func (c *Coord) taskMessage(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p TaskMessage
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	p.Text = strings.TrimSpace(p.Text)
	if p.Text == "" || len(p.Text) > maxMessage {
		return "", nil, bad("text")
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	params := func(v any) *wire.Request { b, _ := json.Marshal(v); return &wire.Request{Method: r.Method, Params: b} }
	if open := c.st.OpenRun(t.ID); open != nil && open.State == task.Running && open.Stream {
		st := t.Flow.StageOf(open.Stage)
		if st == nil || st.Role == "implement" || p.ToRun {
			id, events, err := c.runSend(who, params(SendMessage{Run: open.ID, Text: p.Text}))
			return MessageToRun + "|" + id, events, err
		}
	}
	sit := c.st.Situation(t)
	if last := c.st.Runs[sit.Run]; last != nil && !task.Open(last.State) && last.Session != "" &&
		(sit.Reason == task.AttentionAsked || sit.Reason == task.AttentionPermission) {
		id, events, err := c.runContinue(who, params(Continue{Run: last.ID, Text: p.Text}))
		return MessageToReply + "|" + id, events, err
	}
	return MessageToWorkpad + "|", []journal.Event{journal.NewEvent(task.ETaskNoted,
		task.TaskNote{ID: t.ID, Note: task.Note{Stage: t.Stage, Kind: task.NoteMessage, Text: p.Text, By: who.User}})}, nil
}

func messageView(_ *task.State, id string) any {
	to, run, _ := strings.Cut(id, "|")
	return MessageResult{To: to, Run: run}
}

// stageFeatures are the node features run needs for its stage: a verdict, a check hook.
func stageFeatures(run *task.Run) []string {
	var out []string
	if run.Judge {
		out = append(out, node.FeatureVerdict)
	}
	if len(run.Check) > 0 {
		out = append(out, node.FeatureCheck)
	}
	return out
}
