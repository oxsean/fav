package coord

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

type TaskCreate struct {
	Title    string   `json:"title"`
	Brief    string   `json:"brief,omitempty"`
	Dir      string   `json:"dir,omitempty"` // in the form of the task's machine
	Machine  string   `json:"machine,omitempty"`
	Agent    string   `json:"agent,omitempty"`
	Project  string   `json:"project,omitempty"` // default: its parent's
	Parent   string   `json:"parent,omitempty"`
	After    []string `json:"after,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Accept   []string `json:"acceptance,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Approver string   `json:"approver,omitempty"`
	Status   string   `json:"status,omitempty"`   // todo (default) | backlog
	Workflow string   `json:"workflow,omitempty"` // default: its project's; "none" for none
}

type Dispatch struct {
	Task     string `json:"task"`
	Machine  string `json:"machine,omitempty"` // default: the task's, else this machine
	Agent    string `json:"agent,omitempty"`   // default: the task's, else claude
	Runner   string `json:"runner,omitempty"`
	brief    string // a workflow stage's, in place of the task's
	planning bool   // a planner's: a task in the backlog may be planned
}

type TailParams struct {
	Run    string `json:"run"`
	Before int64  `json:"before"`
	Max    int    `json:"max,omitempty"`
	File   string `json:"file,omitempty"`
}

type NodeCall struct {
	Machine string          `json:"machine"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type StateParams struct {
	NoBriefs bool `json:"no_briefs,omitempty"` // leave the briefs out (task.get has a task's)
}

type MachinesParams struct {
	Connect bool `json:"connect,omitempty"` // try every machine now and wait for the answers
}

// MachineCheck is machine.check: Machine's agent CLIs probed again; none: every connected machine the caller sees.
type MachineCheck struct {
	Machine string `json:"machine,omitempty"`
}

// MachineChecks answers machine.check: the machines checked, and the error code of each that could not be.
type MachineChecks struct {
	Machines []Machine         `json:"machines"`
	Failed   map[string]string `json:"failed,omitempty"`
}

type Machine struct {
	Name      string                 `json:"name"`
	Owner     string                 `json:"owner,omitempty"` // team mode: the user who added it
	State     string                 `json:"state"`           // connected | connecting | offline | idle
	Error     string                 `json:"error,omitempty"`
	Detail    string                 `json:"detail,omitempty"`
	RetryAt   *time.Time             `json:"retry_at,omitzero"`
	Slots     int                    `json:"slots"`
	Active    int                    `json:"active"` // starting, running or unknown runs
	Queued    int                    `json:"queued"`
	OS        string                 `json:"os,omitempty"`
	Hostname  string                 `json:"hostname,omitempty"`
	Version   string                 `json:"version,omitempty"`
	Agents    map[string]agent.Check `json:"agents,omitempty"`    // how each agent CLI stood when last checked
	CheckedAt *time.Time             `json:"checked_at,omitzero"` // when its node last probed them afresh
	Via       string                 `json:"via,omitempty"`       // local | ssh | dial (the node dialed in)
	Missing   []string               `json:"missing,omitempty"`   // node features its build lacks
	Retired   bool                   `json:"retired,omitempty"`   // its owner was disabled: nobody runs anything there again
}

// How a machine is reached, in Machine.Via.
const (
	ViaLocal = "local"
	ViaSSH   = "ssh"
	ViaDial  = "dial"
)

type Machines struct {
	Machines []Machine `json:"machines"`
}

type Agents struct {
	Agents []tend.AgentProfile `json:"agents"`
}

// maxBrief bounds a brief: the journal holds it on one line, and the state it is in goes in one frame.
const maxBrief = 256 << 10

const maxTitle = 1 << 10

const maxProject = 64

// Machine states.
const (
	MachineConnected  = "connected"
	MachineConnecting = "connecting"
	MachineOffline    = "offline"
	MachineIdle       = "idle"
)

// readMethods are the session reads node.call forwards.
var readMethods = []string{remote.MHello, remote.MList, remote.MMessages, remote.MText, remote.MSteps, remote.MPulse,
	remote.MChecks, remote.MLive, remote.MEcho, node.MDirs}

// Methods are the client methods.
var Methods = []string{MStateGet, MTaskGet, MTaskCreate, MTaskEdit, MTaskStatus, MTaskUndo, MRunDispatch, MRunStop, MRunAbandon, MRunTail, MRunOutputPage, MRunOutputWatch,
	MAgentList, MMachineList, MMachineCheck, MStateWatch, MNodeCall, MRunPreview, MRunContinue, MRunAnswer, MRunSend, MRunMessages,
	MProjectCreate, MProjectEdit, MProjectMember, MMachineShare, MMachineDrain, MTaskStart, MTaskMove,
	MAgentDefList, MAgentDefGet, MAgentDefSave, MAgentDefCheck, MAgentDefRemove, MAgentDefShare, MInboxList, MUserOffboard, MTaskSync, MTaskLink, MTaskSourceAck, MTaskGate, MTaskMerge, MTaskPlan, MTaskPlanSave, MTaskPlanApply, MTaskMessage, MTaskMessagePreview, MRunInterrupt, MMachinesWatch, MInboxWatch,
	MRunOutputItem, MRunOutputFind, MRunChanges, MRunDiff, MRunBlob, MProjectDirs}

// Bulk marks the methods whose answers are large pieces fetched on demand: a connection writes them after everything
// else (wire.Options.Bulk).
func Bulk(method string) bool {
	switch method {
	case MRunOutputPage, MRunOutputItem, MRunDiff, MRunBlob:
		return true
	}
	return false
}

// Handler answers this machine's user.
func (c *Coord) Handler() wire.Handler { return c.HandlerFor(Owner) }

// HandlerFor answers p: each method first checks what p may do, and answers only about what p may see.
func (c *Coord) HandlerFor(p Principal) wire.Handler {
	return func(ctx context.Context, r *wire.Request) (any, error) {
		if err := p.may(r.Method); err != nil {
			return nil, err
		}
		switch r.Method {
		case wire.MPing:
			return nil, nil
		case remote.MHello:
			return remote.Hello{Proto: wire.Proto, Version: c.opt.Version, Build: c.opt.Build, Role: "coordinator", Methods: Methods}, nil
		case MStateGet:
			var sp StateParams
			if err := r.Decode(&sp); err != nil {
				return nil, err
			}
			return c.visibleState(p, c.state(!sp.NoBriefs)), nil
		case MTaskGet:
			var rp task.RunRef
			if err := r.Decode(&rp); err != nil {
				return nil, err
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if canRead(c.st, p, c.st.Tasks[rp.ID]) {
				return taskView(c.st, rp.ID), nil
			}
			return nil, notFound(rp.ID)
		case MAgentList:
			return Agents{Agents: c.usableProfiles(p)}, nil
		case MAgentDefList:
			return c.agentDefList(p), nil
		case MInboxList:
			return c.inbox(p), nil
		case MUserOffboard:
			return c.command(p, r, c.userOffboard, func(*task.State, string) any { return nil })
		case MAgentDefGet:
			var ref task.AgentDefRef
			if err := r.Decode(&ref); err != nil {
				return nil, err
			}
			return c.agentDefGet(p, ref.Name)
		case MAgentDefSave:
			return c.command(p, r, c.agentDefSave, c.defAnswer(p))
		case MAgentDefCheck:
			return c.agentDefCheck(p, r)
		case MAgentDefRemove:
			return c.command(p, r, c.agentDefRemove, func(*task.State, string) any { return nil })
		case MAgentDefShare:
			return c.command(p, r, c.agentDefShare, c.defAnswer(p))
		case MMachineList:
			var mp MachinesParams
			if err := r.Decode(&mp); err != nil {
				return nil, err
			}
			ms := c.Machines(ctx, mp.Connect)
			c.mu.Lock()
			ms.Machines = slices.DeleteFunc(ms.Machines, func(m Machine) bool { return !c.canSee(p, m.Name) })
			c.mu.Unlock()
			return ms, nil
		case MMachineCheck:
			var mp MachineCheck
			if err := r.Decode(&mp); err != nil {
				return nil, err
			}
			return c.checkMachines(ctx, p, mp.Machine)
		case MStateWatch:
			return c.watchState(p, r)
		case MMachinesWatch:
			return c.watchTopic(p, r, PushMachines)
		case MInboxWatch:
			return c.watchTopic(p, r, PushInbox)
		case MRunTail:
			var tp TailParams
			if err := r.Decode(&tp); err != nil {
				return nil, err
			}
			run, err := c.readableRun(p, tp.Run)
			if err != nil {
				return nil, err
			}
			var out node.Tail
			err = c.call(ctx, run.Machine, node.MRunTail, node.TailParams{Run: tp.Run, Before: tp.Before, Max: tp.Max, File: tp.File}, &out)
			return out, err
		case MRunOutputWatch:
			return c.watchOutput(p, r)
		case MRunOutputPage:
			return c.outputPage(ctx, p, r)
		case MRunMessages:
			return c.runMessages(ctx, p, r)
		case MRunOutputItem:
			return c.outputItem(ctx, p, r)
		case MRunOutputFind:
			return c.outputFind(ctx, p, r)
		case MRunChanges:
			return c.runChanges(ctx, p, r)
		case MRunDiff:
			return c.runDiff(ctx, p, r)
		case MRunBlob:
			return c.runBlob(ctx, p, r)
		case MProjectDirs:
			return c.projectDirs(ctx, p, r)
		case MNodeCall:
			var np NodeCall
			if err := r.Decode(&np); err != nil {
				return nil, err
			}
			if !slices.Contains(readMethods, np.Method) {
				return nil, forbidden(np.Method)
			}
			c.mu.Lock()
			mine := p.Admin || c.ownerOf(np.Machine) == p.User
			c.mu.Unlock()
			if !mine {
				return nil, forbidden(MNodeCall)
			}
			var out json.RawMessage
			err := c.call(ctx, np.Machine, np.Method, np.Params, &out)
			return out, err
		case MTaskCreate:
			return c.command(p, r, c.taskCreate, taskView)
		case MTaskEdit:
			return c.command(p, r, c.taskEdit, taskView)
		case MTaskStatus:
			return c.command(p, r, c.taskStatus, taskView)
		case MTaskUndo:
			return c.command(p, r, c.taskUndo, taskView)
		case MTaskStart:
			return c.command(p, r, c.taskStart, taskView)
		case MTaskMove:
			return c.command(p, r, c.taskMove, taskView)
		case MTaskSync:
			return c.command(p, r, c.taskSync, taskView)
		case MTaskLink:
			return c.command(p, r, c.taskLink, taskView)
		case MTaskSourceAck:
			return c.command(p, r, c.taskSourceAck, taskView)
		case MTaskGate:
			return c.command(p, r, c.taskGate, taskView)
		case MTaskMerge:
			return c.command(p, r, c.taskMerge, taskView)
		case MTaskPlan:
			return c.command(p, r, c.taskPlan, taskView)
		case MTaskPlanSave:
			return c.command(p, r, c.planSave, taskView)
		case MTaskPlanApply:
			return c.command(p, r, c.planApply, taskView)
		case MTaskMessage:
			return c.command(p, r, c.taskMessage, messageView)
		case MTaskMessagePreview:
			return c.messagePreview(p, r)
		case MRunDispatch:
			return c.command(p, r, c.runDispatch, runView)
		case MRunContinue:
			return c.command(p, r, c.runContinue, runView)
		case MRunPreview:
			var dp Dispatch
			if err := r.Decode(&dp); err != nil {
				return nil, err
			}
			return c.PreviewFor(ctx, p, dp)
		case MRunStop:
			return c.command(p, r, c.runStop, runView)
		case MRunAbandon:
			return c.command(p, r, c.runAbandon, runView)
		case MRunAnswer:
			return c.command(p, r, c.runAnswer, runView)
		case MRunSend:
			return c.command(p, r, c.runSend, runView)
		case MRunInterrupt:
			return c.command(p, r, c.runInterrupt, runView)
		case MProjectCreate:
			return c.command(p, r, c.projectCreate, projectView)
		case MProjectEdit:
			return c.command(p, r, c.projectEdit, projectView)
		case MProjectMember:
			return c.command(p, r, c.projectMember, projectView)
		case MMachineShare:
			return c.command(p, r, c.machineShare, shareView)
		case MMachineDrain:
			return c.command(p, r, c.machineDrain, drainView)
		}
		return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: r.Method}
	}
}

// readableRun is a copy of run id when p may read it.
func (c *Coord) readableRun(p Principal, id string) (task.Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	run := c.st.Runs[id]
	if !canRead(c.st, p, runTask(c.st, run)) {
		return task.Run{}, notFound(id)
	}
	return *run, nil
}

// command runs p's write once per command id: a replay answers what the first run answered, the same id with other
// params is a conflict. Command ids are p's own: another caller's id is another command. do runs under mu and returns
// the id its answer is about.
func (c *Coord) command(p Principal, r *wire.Request, do func(Principal, *wire.Request) (string, []journal.Event, error), view func(*task.State, string) any) (any, error) {
	if r.CommandID == "" {
		return nil, errNoCommand
	}
	digest := journal.Digest(r.Params)
	c.mu.Lock()
	defer c.mu.Unlock()
	if rc, ok := c.receipts[receiptKey(p.User, r.CommandID)]; ok {
		if rc.Method != r.Method || rc.Digest != digest {
			return nil, conflict("command_id")
		}
		if !c.seesResult(p, rc.Result) {
			return nil, notFound("command " + r.CommandID)
		}
		return rc.Result, nil
	}
	if err := c.log.ReadOnly(); err != nil {
		return nil, &wire.Error{Code: wire.CodeInternal, Detail: err.Error()}
	}
	id, events, err := do(p, r)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return view(c.st, id), nil
	}
	rc := &journal.Receipt{ID: r.CommandID, Method: r.Method, Digest: digest, Answer: func(env journal.Envelope) json.RawMessage {
		st := task.New() // what id is once env applies: its copy with env applied
		if t := c.st.Tasks[id]; t != nil {
			cp := *t
			cp.Source = t.Source.Clone()
			st.Tasks[id] = &cp
		}
		if r := c.st.Runs[id]; r != nil {
			cp := *r
			st.Runs[id] = &cp
		}
		if pr := c.st.Projects[id]; pr != nil {
			cp := *pr
			cp.Members = maps.Clone(pr.Members)
			st.Projects[id] = &cp
		}
		if d := c.st.AgentDefs[id]; d != nil {
			cp := *d
			st.AgentDefs[id] = &cp
		}
		if st.Apply(env) != nil { // env touches what the copy lacks: views that need no state still answer
			st = task.New()
		}
		b, _ := json.Marshal(view(st, id))
		return b
	}}
	if err := c.commit(p.actor(), rc, events...); err != nil {
		return nil, err
	}
	c.poke()
	return rc.Result, nil
}

// receiptKey is where a receipt is kept: per user, so one caller's command id never answers another's.
func receiptKey(user, commandID string) string { return user + "\x00" + commandID }

func taskView(st *task.State, id string) any {
	if t := st.Tasks[id]; t != nil {
		cp := *t
		cp.Source = t.Source.Clone()
		return &cp
	}
	return nil
}

func runView(st *task.State, id string) any {
	if r := st.Runs[id]; r != nil {
		cp := *r
		return &cp
	}
	return nil
}

func (c *Coord) checkMachine(name string) error {
	if name != "" && c.ms[name] == nil {
		return notFound("machine " + name)
	}
	return nil
}

func (c *Coord) checkAgent(name string) error {
	if _, ok := c.profile(name); name != "" && !ok && c.agentDefs()[name] == nil {
		return notFound("agent " + name)
	}
	return nil
}

func (c *Coord) taskCreate(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p TaskCreate
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" || len(p.Title) > maxTitle {
		return "", nil, bad("title")
	}
	if len(p.Brief) > maxBrief {
		return "", nil, bad("brief")
	}
	if err := c.checkMachine(p.Machine); err != nil {
		return "", nil, err
	}
	if err := c.checkAgent(p.Agent); err != nil {
		return "", nil, err
	}
	p.Project = strings.TrimSpace(p.Project)
	if parent := c.st.Tasks[p.Parent]; parent != nil && p.Project == "" {
		p.Project = parent.Project
	}
	if err := c.checkProject(who, p.Project); err != nil {
		return "", nil, err
	}
	switch p.Status {
	case "":
		p.Status = task.StatusTodo
	case task.StatusTodo, task.StatusBacklog:
	default:
		return "", nil, bad("status " + p.Status)
	}
	if err := c.checkFields(who, p.Project, p.Kind, p.Approver, p.Accept, p.Tags); err != nil {
		return "", nil, err
	}
	wf, flow, err := c.flowOf(p.Project, p.Workflow)
	if err != nil {
		return "", nil, err
	}
	t := task.Task{ID: newID("t_"), Title: p.Title, Brief: p.Brief, Dir: p.Dir, Machine: p.Machine, Agent: p.Agent,
		Project: p.Project, Owner: who.User, Approver: p.Approver, Kind: p.Kind, Accept: p.Accept, Tags: p.Tags, Status: p.Status,
		Workflow: wf, Flow: flow}
	if flow != nil {
		t.Stage = flow.Stages[0].Name
	}
	if err := c.checkPlace(who, &t, p.Parent, p.After); err != nil {
		return "", nil, err
	}
	t.Parent, t.After = p.Parent, p.After
	return t.ID, []journal.Event{journal.NewEvent(task.ETaskCreated, t)}, nil
}

func (c *Coord) taskEdit(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.TaskEdit
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	if p.Title != nil {
		if *p.Title = strings.TrimSpace(*p.Title); *p.Title == "" || len(*p.Title) > maxTitle {
			return "", nil, bad("title")
		}
	}
	if p.Brief != nil && len(*p.Brief) > maxBrief {
		return "", nil, bad("brief")
	}
	if p.Machine != nil {
		if err := c.checkMachine(*p.Machine); err != nil {
			return "", nil, err
		}
	}
	if p.Agent != nil {
		if err := c.checkAgent(*p.Agent); err != nil {
			return "", nil, err
		}
	}
	if p.Project != nil {
		if *p.Project = strings.TrimSpace(*p.Project); *p.Project != t.Project {
			if err := c.checkProject(who, *p.Project); err != nil {
				return "", nil, err
			}
			if t.Parent != "" || len(c.st.Children(t.ID)) > 0 {
				return "", nil, conflict("a task in a tree moves with its tree")
			}
		}
	}
	project := t.Project
	if p.Project != nil {
		project = *p.Project
	}
	if err := c.checkFields(who, project, deref(p.Kind), deref(p.Approver), derefs(p.Accept), derefs(p.Tags)); err != nil {
		return "", nil, err
	}
	if p.Owner != nil || p.Approver != nil {
		if err := c.checkHandOver(who, t, p.Owner); err != nil {
			return "", nil, err
		}
	}
	if p.Workflow != nil {
		if open := c.st.OpenRun(t.ID); open != nil {
			return "", nil, conflict("open run " + open.ID)
		}
		wf, flow, err := c.flowOf(project, firstOf(*p.Workflow, NoWorkflow))
		if err != nil {
			return "", nil, err
		}
		if wf == t.Workflow {
			p.Workflow = nil
		} else {
			p.Workflow, p.Flow = &wf, flow
		}
	}
	changed := p.Accept != nil && !slices.Equal(*p.Accept, t.Accept) || p.Tags != nil && !slices.Equal(*p.Tags, t.Tags) || p.Workflow != nil
	for _, f := range []struct {
		v   *string
		now string
	}{{p.Title, t.Title}, {p.Brief, t.Brief}, {p.Dir, t.Dir}, {p.Machine, t.Machine}, {p.Agent, t.Agent},
		{p.Project, t.Project}, {p.Owner, t.Owner}, {p.Approver, t.Approver}, {p.Kind, t.Kind}} {
		changed = changed || f.v != nil && *f.v != f.now
	}
	if !changed {
		return t.ID, nil, nil
	}
	return t.ID, []journal.Event{journal.NewEvent(task.ETaskEdited, p)}, nil
}

func (c *Coord) taskStatus(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.TaskStatus
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	if !slices.Contains([]string{task.StatusBacklog, task.StatusTodo, task.StatusDone, task.StatusCanceled}, p.Status) {
		return "", nil, bad("status " + p.Status)
	}
	if t.Status == p.Status {
		return t.ID, nil, nil
	}
	if p.Status == task.StatusDone && task.SourceWaits(t) == task.WhySourceChanged {
		return "", nil, conflict(task.WhySourceChanged)
	}
	if p.Status == task.StatusDone && t.Flow != nil {
		return "", nil, conflict("workflow: its last stage finishes it")
	}
	if p.Status == task.StatusCanceled {
		return t.ID, c.cancelTree(t), nil
	}
	if p.Status == task.StatusDone {
		if c.st.OpenRun(t.ID) != nil && c.st.NeedsMerge(t) {
			return "", nil, conflict("open run")
		}
		return t.ID, c.finish(t), nil
	}
	events := []journal.Event{journal.NewEvent(task.ETaskStatus, p)}
	if task.Finished(t.Status) && t.Flow.StageOf(t.Stage) != nil {
		events = append(events, journal.NewEvent(task.ETaskStaged, task.TaskStage{ID: t.ID, Stage: t.Flow.Back(t.Stage), Loops: t.Loops, Back: true}))
	}
	return t.ID, events, nil
}

func (c *Coord) runDispatch(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p Dispatch
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	run, err := c.plan(who, p)
	if err != nil {
		return "", nil, err
	}
	if !c.canUse(who, run.Machine, run.Project) {
		return "", nil, forbidden("machine " + run.Machine)
	}
	return run.ID, []journal.Event{journal.NewEvent(task.ERunQueued, run)}, nil
}

// plan is the run who's dispatch would queue; the caller holds mu.
func (c *Coord) plan(who Principal, p Dispatch) (task.Run, error) {
	t, err := c.writableTask(who, p.Task)
	if err != nil {
		return task.Run{}, err
	}
	if t.Status != task.StatusTodo && !(p.planning && t.Status == task.StatusBacklog) {
		return task.Run{}, conflict("task " + t.Status)
	}
	if open := c.st.OpenRun(t.ID); open != nil {
		return task.Run{}, conflict("open run " + open.ID)
	}
	if slices.ContainsFunc(c.st.Children(t.ID), func(k *task.Task) bool { return !task.Finished(k.Status) }) {
		return task.Run{}, conflict("subtasks")
	}
	if !slices.Contains([]string{"", node.RunnerBackground, node.RunnerHerdr}, p.Runner) {
		return task.Run{}, bad("runner " + p.Runner)
	}
	pr := c.st.Projects[t.Project]
	name := firstOf(p.Agent, t.Agent, pr.Agent(), tend.ProviderClaude)
	prof, def, err := c.agentFor(who, name, t.Project)
	if err != nil {
		return task.Run{}, err
	}
	var defaultMachine string
	if pr != nil {
		defaultMachine = pr.Defaults.Machine
	}
	machine := firstOf(p.Machine, prof.Machine, t.Machine, defaultMachine, c.preferred(def))
	if machine == "" && c.opt.Remote {
		return task.Run{}, bad("machine")
	}
	machine = firstOf(machine, Local)
	if machine, err = c.workMachine(t, pr, machine, p.Machine); err != nil {
		return task.Run{}, err
	}
	if p.Runner == node.RunnerHerdr && prof.Provider != tend.ProviderClaude {
		return task.Run{}, bad("runner herdr runs claude only")
	}
	if prof.Machine != "" && machine != prof.Machine {
		return task.Run{}, bad("agent " + name + " runs on " + prof.Machine)
	}
	if def != nil && len(def.Machines.Require) > 0 && !slices.Contains(def.Machines.Require, machine) {
		return task.Run{}, bad("agent " + name + " runs on " + strings.Join(def.Machines.Require, ", "))
	}
	if err := c.checkMachine(machine); err != nil {
		return task.Run{}, err
	}
	brief := firstOf(p.brief, t.Brief)
	if strings.TrimSpace(brief) == "" {
		brief = t.Title
	}
	if def != nil && strings.TrimSpace(def.Body) != "" {
		brief = def.Body + "\n\n---\n\n" + brief
	}
	if pr != nil && strings.TrimSpace(pr.Context) != "" {
		brief = "# " + pr.Name + "\n\n" + pr.Context + "\n\n---\n\n" + brief
	}
	dir, from := t.Dir, t.Machine
	if from == "" && !c.opt.Remote {
		from = Local
	}
	var work *agent.Workspace
	if dir == "" {
		repo, ok := pr.RepoOn(machine)
		if !ok {
			return task.Run{}, bad("dir")
		}
		dir, from = repo.Dirs[machine], machine
		if repo.Worktrees {
			if p.Runner == node.RunnerHerdr {
				return task.Run{}, bad("runner herdr has no worktree")
			}
			work = c.workspace(t, pr, repo, dir)
		}
	}
	return task.Run{ID: node.NewRunID(), Task: t.ID, Machine: machine, Agent: name, Profile: prof, Dir: dir, From: from,
		Brief: brief, Title: t.Title, Runner: p.Runner, Project: t.Project, Dispatcher: who.User, Work: work}, nil
}

// writableTask is task id when who may change it: not found when they may not even see it. The caller holds mu.
func (c *Coord) writableTask(who Principal, id string) (*task.Task, error) {
	t := c.st.Tasks[id]
	switch {
	case !canRead(c.st, who, t):
		return nil, notFound(id)
	case !canWrite(c.st, who, t):
		return nil, forbidden(id)
	}
	return t, nil
}

// writableRun is run id when who may act on it (its task's participants). The caller holds mu.
func (c *Coord) writableRun(who Principal, id string) (*task.Run, error) {
	run := c.st.Runs[id]
	t := runTask(c.st, run)
	switch {
	case !canRead(c.st, who, t):
		return nil, notFound(id)
	case !canWrite(c.st, who, t):
		return nil, forbidden(id)
	}
	return run, nil
}

// checkProject: who may put a task in project ("" is none: the task is theirs). The caller holds mu.
func (c *Coord) checkProject(who Principal, project string) error {
	switch {
	case len(project) > maxProject:
		return bad("project")
	case project == "":
		return nil
	case c.st.Projects[project] == nil && c.team():
		return notFound("project " + project)
	case c.st.Projects[project] != nil && roleIn(c.st, who, project) != task.RoleParticipant:
		return forbidden("project " + project)
	case c.st.Projects[project] == nil && !who.Admin:
		return forbidden("project " + project)
	}
	return nil
}

func (c *Coord) run(who Principal, r *wire.Request) (*task.Run, error) {
	var p task.RunRef
	if err := r.Decode(&p); err != nil {
		return nil, err
	}
	return c.writableRun(who, p.ID)
}

func (c *Coord) runStop(who Principal, r *wire.Request) (string, []journal.Event, error) {
	run, err := c.run(who, r)
	if err != nil {
		return "", nil, err
	}
	ref := task.RunRef{ID: run.ID}
	switch {
	case run.State == task.Queued:
		return run.ID, []journal.Event{journal.NewEvent(task.ERunCanceled, ref)}, nil
	case task.Open(run.State) && run.Want != "stop":
		return run.ID, []journal.Event{journal.NewEvent(task.ERunStopAsked, ref)}, nil
	}
	return run.ID, nil, nil
}

func (c *Coord) runAbandon(who Principal, r *wire.Request) (string, []journal.Event, error) {
	run, err := c.run(who, r)
	if err != nil {
		return "", nil, err
	}
	switch run.State {
	case task.Starting, task.Running, task.Unknown: // the node is asked to stop it, if it still runs
		return run.ID, []journal.Event{journal.NewEvent(task.ERunAbandoned, task.RunRef{ID: run.ID})}, nil
	case task.Queued:
		return "", nil, conflict("run " + run.State)
	}
	return run.ID, nil, nil
}

// Machines is every machine and how it stands; connect tries each now and waits for the answers.
func (c *Coord) Machines(ctx context.Context, connect bool) Machines {
	if connect {
		c.mu.Lock()
		for _, m := range c.ms {
			m.retryAt = time.Time{}
			m.busyAt = time.Now()
			c.ensure(m)
		}
		c.mu.Unlock()
		c.waitDials(ctx)
		c.refreshChecks(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Machines{Machines: c.machineList()}
}

// machineList is every machine and how it stands, this machine first; the caller holds mu.
func (c *Coord) machineList() []Machine {
	out := []Machine{}
	for _, m := range c.ms {
		out = append(out, c.machineView(m))
	}
	slices.SortFunc(out, func(a, b Machine) int {
		if (a.Name == Local) != (b.Name == Local) {
			if a.Name == Local {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// machineView is how m stands; the caller holds mu.
func (c *Coord) machineView(m *machine) Machine {
	x := Machine{Name: m.name, Slots: c.slots(m.name), OS: m.hello.OS, Hostname: m.hello.Hostname, Version: m.hello.Version,
		Agents: m.checks, Via: ViaLocal}
	if !m.checkedAt.IsZero() {
		at := m.checkedAt
		x.CheckedAt = &at
	}
	switch {
	case m.attached:
		x.Via = ViaDial
	case m.host != nil:
		x.Via = ViaSSH
	}
	if m.conn != nil && m.hello.Version != "" {
		x.Missing = missingFeatures(m.hello, node.Features)
	}
	if c.team() {
		x.Owner, x.Retired = c.ownerOf(m.name), c.retired(m.name)
	}
	for _, r := range c.st.Runs {
		if r.Machine == m.name && r.State == task.Queued {
			x.Queued++
		} else if r.Machine == m.name && task.Open(r.State) {
			x.Active++
		}
	}
	switch {
	case m.conn != nil:
		x.State = MachineConnected
	case m.dialing:
		x.State = MachineConnecting
	case m.err != nil || m.attached:
		x.State = MachineOffline
		if !m.retryAt.IsZero() && !m.attached {
			at := m.retryAt
			x.RetryAt = &at
		}
	default:
		x.State = MachineIdle
	}
	if m.err != nil {
		x.Error, x.Detail = wire.Code(m.err), m.err.Error()
		if x.Error == "" {
			x.Error = wire.CodeInternal
		}
	}
	return x
}

func firstOf(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
