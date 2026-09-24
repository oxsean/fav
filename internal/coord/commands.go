package coord

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

type TaskCreate struct {
	Title   string `json:"title"`
	Brief   string `json:"brief,omitempty"`
	Dir     string `json:"dir,omitempty"` // in the form of the task's machine
	Machine string `json:"machine,omitempty"`
	Agent   string `json:"agent,omitempty"`
}

type Dispatch struct {
	Task    string `json:"task"`
	Machine string `json:"machine,omitempty"` // default: the task's, else this machine
	Agent   string `json:"agent,omitempty"`   // default: the task's, else claude
	Runner  string `json:"runner,omitempty"`
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

type Machine struct {
	Name     string     `json:"name"`
	State    string     `json:"state"` // connected | connecting | offline | idle
	Error    string     `json:"error,omitempty"`
	Detail   string     `json:"detail,omitempty"`
	RetryAt  *time.Time `json:"retry_at,omitempty"`
	Slots    int        `json:"slots"`
	Active   int        `json:"active"` // starting, running or unknown runs
	Queued   int        `json:"queued"`
	OS       string     `json:"os,omitempty"`
	Hostname string     `json:"hostname,omitempty"`
	Version  string     `json:"version,omitempty"`
}

type Machines struct {
	Machines []Machine `json:"machines"`
}

type Agents struct {
	Agents []fav.AgentProfile `json:"agents"`
}

// maxBrief bounds a brief: the journal holds it on one line, and the state it is in goes in one frame.
const maxBrief = 256 << 10

const maxTitle = 1 << 10

// Machine states.
const (
	MachineConnected  = "connected"
	MachineConnecting = "connecting"
	MachineOffline    = "offline"
	MachineIdle       = "idle"
)

// readMethods are the session reads node.call forwards.
var readMethods = []string{remote.MHello, remote.MList, remote.MMessages, remote.MText, remote.MSteps, remote.MPulse,
	remote.MChecks, remote.MLive, remote.MEcho}

// Methods are the client methods.
var Methods = []string{MStateGet, MTaskGet, MTaskCreate, MTaskEdit, MTaskStatus, MRunDispatch, MRunStop, MRunAbandon, MRunTail,
	MAgentList, MMachineList, MSubscribe, MNodeCall}

// Handler answers clients.
func (c *Coord) Handler() wire.Handler {
	return func(ctx context.Context, r *wire.Request) (any, error) {
		switch r.Method {
		case wire.MPing:
			return nil, nil
		case remote.MHello:
			return remote.Hello{Proto: wire.Proto, Version: c.opt.Version, Role: "coordinator", Methods: Methods}, nil
		case MStateGet:
			var p StateParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return c.state(!p.NoBriefs), nil
		case MTaskGet:
			var p task.RunRef
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if t := taskView(c.st, p.ID); t != nil {
				return t, nil
			}
			return nil, notFound(p.ID)
		case MAgentList:
			return Agents{Agents: c.Profiles()}, nil
		case MMachineList:
			var p MachinesParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return c.Machines(ctx, p.Connect), nil
		case MSubscribe:
			return c.subscribe(r)
		case MRunTail:
			var p TailParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			c.mu.Lock()
			run := c.st.Runs[p.Run]
			c.mu.Unlock()
			if run == nil {
				return nil, notFound(p.Run)
			}
			var out node.Tail
			err := c.call(ctx, run.Machine, node.MRunTail, node.TailParams{Run: p.Run, Before: p.Before, Max: p.Max, File: p.File}, &out)
			return out, err
		case MNodeCall:
			var p NodeCall
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			if !slices.Contains(readMethods, p.Method) {
				return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: p.Method}
			}
			var out json.RawMessage
			err := c.call(ctx, p.Machine, p.Method, p.Params, &out)
			return out, err
		case MTaskCreate:
			return c.command(r, c.taskCreate, taskView)
		case MTaskEdit:
			return c.command(r, c.taskEdit, taskView)
		case MTaskStatus:
			return c.command(r, c.taskStatus, taskView)
		case MRunDispatch:
			return c.command(r, c.runDispatch, runView)
		case MRunStop:
			return c.command(r, c.runStop, runView)
		case MRunAbandon:
			return c.command(r, c.runAbandon, runView)
		}
		return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: r.Method}
	}
}

// command runs a write once per command id: a replay answers what the first run answered, the same id with other
// params is a conflict. do runs under mu and returns the id its answer is about.
func (c *Coord) command(r *wire.Request, do func(*wire.Request) (string, []journal.Event, error), view func(*task.State, string) any) (any, error) {
	if r.CommandID == "" {
		return nil, errNoCommand
	}
	digest := journal.Digest(r.Params)
	c.mu.Lock()
	defer c.mu.Unlock()
	if rc, ok := c.receipts[r.CommandID]; ok {
		if rc.Method != r.Method || rc.Digest != digest {
			return nil, conflict("command_id")
		}
		return rc.Result, nil
	}
	if err := c.log.ReadOnly(); err != nil {
		return nil, &wire.Error{Code: wire.CodeInternal, Detail: err.Error()}
	}
	id, events, err := do(r)
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
			st.Tasks[id] = &cp
		}
		if r := c.st.Runs[id]; r != nil {
			cp := *r
			st.Runs[id] = &cp
		}
		if st.Apply(env) != nil {
			return nil
		}
		b, _ := json.Marshal(view(st, id))
		return b
	}}
	if err := c.commit(rc, events...); err != nil {
		return nil, err
	}
	c.poke()
	return rc.Result, nil
}

func taskView(st *task.State, id string) any {
	if t := st.Tasks[id]; t != nil {
		cp := *t
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
	if _, ok := c.profile(name); name != "" && !ok {
		return notFound("agent " + name)
	}
	return nil
}

func (c *Coord) taskCreate(r *wire.Request) (string, []journal.Event, error) {
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
	t := task.Task{ID: newID("t_"), Title: p.Title, Brief: p.Brief, Dir: p.Dir, Machine: p.Machine, Agent: p.Agent,
		Status: task.StatusTodo}
	return t.ID, []journal.Event{journal.NewEvent(task.ETaskCreated, t)}, nil
}

func (c *Coord) taskEdit(r *wire.Request) (string, []journal.Event, error) {
	var p task.TaskEdit
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t := c.st.Tasks[p.ID]
	if t == nil {
		return "", nil, notFound(p.ID)
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
	changed := false
	for _, f := range []struct {
		v   *string
		now string
	}{{p.Title, t.Title}, {p.Brief, t.Brief}, {p.Dir, t.Dir},
		{p.Machine, t.Machine}, {p.Agent, t.Agent}} {
		changed = changed || f.v != nil && *f.v != f.now
	}
	if !changed {
		return t.ID, nil, nil
	}
	return t.ID, []journal.Event{journal.NewEvent(task.ETaskEdited, p)}, nil
}

func (c *Coord) taskStatus(r *wire.Request) (string, []journal.Event, error) {
	var p task.TaskStatus
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t := c.st.Tasks[p.ID]
	if t == nil {
		return "", nil, notFound(p.ID)
	}
	if !slices.Contains([]string{task.StatusTodo, task.StatusDone, task.StatusCanceled}, p.Status) {
		return "", nil, bad("status " + p.Status)
	}
	if t.Status == p.Status {
		return t.ID, nil, nil
	}
	return t.ID, []journal.Event{journal.NewEvent(task.ETaskStatus, p)}, nil
}

func (c *Coord) runDispatch(r *wire.Request) (string, []journal.Event, error) {
	var p Dispatch
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t := c.st.Tasks[p.Task]
	if t == nil {
		return "", nil, notFound(p.Task)
	}
	if t.Status != task.StatusTodo {
		return "", nil, conflict("task " + t.Status)
	}
	if open := c.st.OpenRun(t.ID); open != nil {
		return "", nil, conflict("open run " + open.ID)
	}
	if t.Dir == "" {
		return "", nil, bad("dir")
	}
	if !slices.Contains([]string{"", node.RunnerBackground, node.RunnerHerdr}, p.Runner) {
		return "", nil, bad("runner " + p.Runner)
	}
	name := firstOf(p.Agent, t.Agent, fav.ProviderClaude)
	prof, ok := c.profile(name)
	if !ok {
		return "", nil, notFound("agent " + name)
	}
	machine := firstOf(p.Machine, prof.Machine, t.Machine)
	if machine == "" && c.opt.Remote {
		return "", nil, bad("machine")
	}
	machine = firstOf(machine, Local)
	if p.Runner == node.RunnerHerdr && prof.Provider != fav.ProviderClaude {
		return "", nil, bad("runner herdr runs claude only")
	}
	if prof.Machine != "" && machine != prof.Machine {
		return "", nil, bad("agent " + name + " runs on " + prof.Machine)
	}
	if err := c.checkMachine(machine); err != nil {
		return "", nil, err
	}
	brief := t.Brief
	if strings.TrimSpace(brief) == "" {
		brief = t.Title
	}
	from := t.Machine
	if from == "" && !c.opt.Remote {
		from = Local
	}
	run := task.Run{ID: node.NewRunID(), Task: t.ID, Machine: machine, Agent: name, Profile: prof, Dir: t.Dir, From: from,
		Brief: brief, Title: t.Title, Runner: p.Runner}
	return run.ID, []journal.Event{journal.NewEvent(task.ERunQueued, run)}, nil
}

func (c *Coord) run(r *wire.Request) (*task.Run, error) {
	var p task.RunRef
	if err := r.Decode(&p); err != nil {
		return nil, err
	}
	run := c.st.Runs[p.ID]
	if run == nil {
		return nil, notFound(p.ID)
	}
	return run, nil
}

func (c *Coord) runStop(r *wire.Request) (string, []journal.Event, error) {
	run, err := c.run(r)
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

func (c *Coord) runAbandon(r *wire.Request) (string, []journal.Event, error) {
	run, err := c.run(r)
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
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out Machines
	for _, m := range c.ms {
		x := Machine{Name: m.name, Slots: c.slots(m.name), OS: m.hello.OS, Hostname: m.hello.Hostname, Version: m.hello.Version}
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
			if !m.retryAt.IsZero() {
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
		out.Machines = append(out.Machines, x)
	}
	slices.SortFunc(out.Machines, func(a, b Machine) int {
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

func firstOf(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
