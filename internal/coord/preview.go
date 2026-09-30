package coord

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// ReasonNodeOutdated: the machine's tend is older than what the run needs (`tend hosts install` updates it).
const ReasonNodeOutdated = "node_outdated"

// runFeatures are the node features run needs: a field of run.start an older node would ignore.
var runFeatures = func(run *task.Run) []string {
	var out []string
	if run.Dispatcher != "" && run.Dispatcher != Owner.User {
		out = append(out, node.FeatureDispatcher)
	}
	if run.Profile.Effort != "" || len(run.Profile.Deny) > 0 {
		out = append(out, node.FeatureAgentDef)
	}
	if run.Work != nil {
		out = append(out, node.FeatureWorktree)
	}
	if run.Work != nil && len(run.Work.BeforeRun) > 0 {
		out = append(out, node.FeatureBeforeRun)
	}
	if run.Planner {
		out = append(out, node.FeaturePlan)
	}
	if len(run.Profile.Hooks) > 0 || len(run.Profile.MCP) > 0 || len(run.Profile.Skills) > 0 {
		out = append(out, node.FeatureFiles)
	}
	return append(out, stageFeatures(run)...)
}

// missingFeatures are those of need the node that said h lacks.
func missingFeatures(h remote.Hello, need []string) []string {
	var lack []string
	for _, f := range need {
		if !slices.Contains(h.Features, f) {
			lack = append(lack, f)
		}
	}
	return lack
}

// Why is one reason a run cannot start, or will start late or differently; Code is stable, clients word it.
type Why struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// What a preview says.
const (
	WhyCLIMissing   = agent.ReasonCLIMissing  // blocks: the agent's CLI is not on the machine
	WhyAuthMissing  = agent.ReasonAuthMissing // blocks: the CLI is not logged in there
	WhyOutdated     = ReasonNodeOutdated      // blocks: the machine's tend cannot do it
	WhyNoAccess     = "no_access"             // blocks: the machine is not open to the caller or the task's project
	WhyDefPending   = "def_pending"           // the definition's MCP servers or hooks do not apply to its provider
	WhyOffline      = "offline"               // it queues until the machine answers
	WhyConnecting   = "connecting"
	WhyDrain        = "drain"    // it queues until the machine takes new runs again (Detail: who stopped them)
	WhySlots        = "slots"    // it queues until a slot frees (Detail: active/slots)
	WhyDirBusy      = "dir_busy" // it queues until the run in the same directory ends (Detail: run id)
	WhyAuthUnknown  = "auth_unknown"
	WhyUnchecked    = "unchecked" // the machine was not reached, so its CLI is not checked
	WhyHerdr        = "herdr"     // it may open in a Herdr tab there
	WhyBackground   = "background"
	WhyContinuation = "continues" // it continues session Detail
)

// Preview is how a dispatch would go, asked before it is made.
type Preview struct {
	Machine  string            `json:"machine"`
	Agent    string            `json:"agent"`
	Provider string            `json:"provider"`
	Model    string            `json:"model,omitempty"`
	Dir      string            `json:"dir"`
	State    string            `json:"state"` // the machine's
	Version  string            `json:"version,omitempty"`
	Check    *agent.Check      `json:"check,omitempty"`
	Profile  tend.AgentProfile `json:"profile"`            // what the run is frozen with
	Blockers []Why             `json:"blockers,omitempty"` // the run would fail at once
	Notes    []Why             `json:"notes,omitempty"`    // how it will go
}

// Preview says how dispatching p would go: where, with what, and anything that would hold it back or fail it. It
// checks the machine's agent CLI when the machine answers.
func (c *Coord) Preview(ctx context.Context, p Dispatch) (Preview, error) {
	return c.PreviewFor(ctx, Owner, p)
}

// PreviewFor is Preview for who's dispatch.
func (c *Coord) PreviewFor(ctx context.Context, who Principal, p Dispatch) (Preview, error) {
	c.mu.Lock()
	run, err := c.plan(who, p)
	open := err == nil && c.canUse(who, run.Machine, run.Project)
	var pending []string
	if d := c.agentDefs()[run.Agent]; err == nil && d != nil && run.Profile.Provider != tend.ProviderClaude {
		for name, n := range map[string]int{"mcp": len(d.MCP), "hooks": len(d.Hooks)} {
			if n > 0 {
				pending = append(pending, name)
			}
		}
		slices.Sort(pending)
	}
	c.mu.Unlock()
	if err != nil {
		return Preview{}, err
	}
	pv := c.preview(ctx, run)
	pv.Profile = run.Profile
	if len(pending) > 0 {
		pv.Notes = append(pv.Notes, Why{WhyDefPending, strings.Join(pending, ", ")})
	}
	if !open {
		pv.Blockers = append([]Why{{WhyNoAccess, run.Machine}}, pv.Blockers...)
	}
	return pv, nil
}

func (c *Coord) preview(ctx context.Context, run task.Run) Preview {
	c.mu.Lock()
	m := c.ms[run.Machine]
	pv := Preview{Machine: run.Machine, Agent: run.Agent, Provider: run.Profile.Provider, Model: run.Profile.Model, Dir: run.Dir}
	if m == nil {
		c.mu.Unlock()
		return pv
	}
	m.busyAt = time.Now()
	c.ensure(m)
	c.mu.Unlock()
	c.waitMachine(ctx, m)

	c.mu.Lock()
	x := c.machineView(m)
	pv.State, pv.Version = x.State, x.Version
	switch x.State {
	case MachineOffline:
		pv.Notes = append(pv.Notes, Why{WhyOffline, x.Detail})
	case MachineConnecting, MachineIdle:
		pv.Notes = append(pv.Notes, Why{WhyConnecting, ""})
	}
	if d := c.st.Drains[m.name]; d != nil {
		pv.Notes = append(pv.Notes, Why{WhyDrain, d.By})
	}
	if x.Active >= x.Slots {
		pv.Notes = append(pv.Notes, Why{WhySlots, strconv.Itoa(x.Active) + "/" + strconv.Itoa(x.Slots)})
	}
	if dir, ok := c.mapDir(&run, m); ok {
		pv.Dir = dir
		for _, r := range c.st.Runs {
			if r.Machine == m.name && task.Open(r.State) && r.State != task.Queued && runKey(r, r.Dir, m.hello.OS) == runKey(&run, dir, m.hello.OS) {
				pv.Notes = append(pv.Notes, Why{WhyDirBusy, r.ID})
				break
			}
		}
	}
	resumes := slices.Contains(m.hello.Methods, node.MRunResume)
	connected := m.conn != nil
	lack := missingFeatures(m.hello, runFeatures(&run))
	c.mu.Unlock()

	if connected && len(lack) > 0 {
		pv.Blockers = append(pv.Blockers, Why{WhyOutdated, pv.Version})
	}

	if run.Resume != "" {
		pv.Notes = append(pv.Notes, Why{WhyContinuation, run.Resume})
		if connected && !resumes {
			pv.Blockers = append(pv.Blockers, Why{WhyOutdated, pv.Version})
		}
	}
	switch {
	case run.Resume != "", run.Runner == node.RunnerBackground, run.Profile.Provider != tend.ProviderClaude:
		pv.Notes = append(pv.Notes, Why{WhyBackground, ""})
	default:
		pv.Notes = append(pv.Notes, Why{WhyHerdr, ""})
	}
	if !slices.Contains(agent.Sessions(), run.Profile.Provider) {
		return pv
	}
	checks := c.checksOf(ctx, m)
	ck, ok := checks[run.Profile.Provider]
	if !ok {
		pv.Notes = append(pv.Notes, Why{WhyUnchecked, ""})
		return pv
	}
	pv.Check = &ck
	switch ck.Blocker() {
	case agent.ReasonCLIMissing:
		pv.Blockers = append(pv.Blockers, Why{WhyCLIMissing, run.Profile.Provider})
	case agent.ReasonAuthMissing:
		pv.Blockers = append(pv.Blockers, Why{WhyAuthMissing, run.Profile.Provider})
	default:
		if ck.Auth == agent.AuthUnknown {
			pv.Notes = append(pv.Notes, Why{WhyAuthUnknown, run.Profile.Provider})
		}
	}
	return pv
}

// waitMachine waits while m is being dialed.
func (c *Coord) waitMachine(ctx context.Context, m *machine) {
	for {
		c.mu.Lock()
		dialing := m.dialing
		c.mu.Unlock()
		if !dialing {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// checksOf asks m's node how its agent CLIs stand (a node that cannot say answers what was known).
func (c *Coord) checksOf(ctx context.Context, m *machine) map[string]agent.Check {
	c.mu.Lock()
	conn, can, known := m.conn, slices.Contains(m.hello.Methods, node.MAgents), m.checks
	c.mu.Unlock()
	if conn == nil || !can {
		return known
	}
	var out node.Checks
	if ok, err := c.callNode(ctx, m, conn, node.MAgents, node.ChecksParams{}, &out); !ok || err != nil {
		return known
	}
	c.mu.Lock()
	m.checks = out.Agents
	c.machinesMoved()
	c.mu.Unlock()
	return out.Agents
}

// refreshChecks has every connected machine probe its agent CLIs afresh.
func (c *Coord) refreshChecks(ctx context.Context) {
	c.mu.Lock()
	var ms []*machine
	for _, m := range c.ms {
		if m.conn != nil {
			ms = append(ms, m)
		}
	}
	c.mu.Unlock()
	var wg sync.WaitGroup
	for _, m := range ms {
		wg.Add(1)
		go func() { defer wg.Done(); c.probe(ctx, m) }()
	}
	wg.Wait()
}

// Continue answers a waiting run, or any indexed session, with a new run in the same session.
type Continue struct {
	Run  string `json:"run,omitempty"` // the run whose session goes on
	Text string `json:"text"`
	// A session no run of this coordinator left: where it is and whose it is.
	Machine  string `json:"machine,omitempty"`
	Provider string `json:"provider,omitempty"`
	Session  string `json:"session,omitempty"`
	Dir      string `json:"dir,omitempty"` // as its machine names it
	Title    string `json:"title,omitempty"`
	Agent    string `json:"agent,omitempty"` // default: the run's profile, else the provider's
}

func (c *Coord) runContinue(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p Continue
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(p.Text) == "" || len(p.Text) > maxBrief {
		return "", nil, bad("text")
	}
	if p.Run != "" {
		prev, err := c.writableRun(who, p.Run)
		if err != nil {
			return "", nil, err
		}
		run, err := c.continuation(who, prev, p.Text, p.Agent)
		if err != nil {
			return "", nil, err
		}
		return run.ID, []journal.Event{journal.NewEvent(task.ERunQueued, run)}, nil
	}
	if p.Session == "" || p.Dir == "" || p.Provider == "" {
		return "", nil, bad("session")
	}
	machine := firstOf(p.Machine, Local)
	if err := c.checkMachine(machine); err != nil {
		return "", nil, err
	}
	if c.ownerOf(machine) != who.User { // any session of a machine, not one a run made: its owner's alone
		return "", nil, forbidden(MRunContinue)
	}
	name := firstOf(p.Agent, p.Provider)
	prof, ok := c.profile(name)
	if !ok {
		return "", nil, notFound("agent " + name)
	}
	if agent.SessionProvider(prof.Provider) != p.Provider {
		return "", nil, bad("agent " + name + " is not " + p.Provider)
	}
	if prof.Machine != "" && prof.Machine != machine {
		return "", nil, bad("agent " + name + " runs on " + prof.Machine)
	}
	if err := c.canContinue(prof, machine); err != nil {
		return "", nil, err
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title, _, _ = strings.Cut(strings.TrimSpace(p.Text), "\n")
	}
	if len(title) > maxTitle {
		title = title[:maxTitle]
	}
	t := task.Task{ID: newID("t_"), Title: title, Dir: p.Dir, Machine: machine, Agent: name, Owner: who.User, Status: task.StatusTodo}
	run := task.Run{ID: node.NewRunID(), Task: t.ID, Machine: machine, Agent: name, Profile: prof, Dir: p.Dir, From: machine,
		Brief: p.Text, Title: title, Runner: node.RunnerBackground, Resume: p.Session, Dispatcher: who.User}
	return run.ID, []journal.Event{journal.NewEvent(task.ETaskCreated, t), journal.NewEvent(task.ERunQueued, run)}, nil
}

// continuation is the run that goes on with prev's session with text, as who; agentName "" keeps prev's agent. The
// caller holds mu.
func (c *Coord) continuation(who Principal, prev *task.Run, text, agentName string) (task.Run, error) {
	if !c.canUse(who, prev.Machine, prev.Project) {
		return task.Run{}, forbidden("machine " + prev.Machine)
	}
	if prev.Session == "" {
		return task.Run{}, bad("run " + prev.ID + " has no session")
	}
	if open := c.st.OpenRun(prev.Task); open != nil {
		return task.Run{}, conflict("open run " + open.ID)
	}
	prof := prev.Profile
	if agentName != "" {
		var ok bool
		if prof, ok = c.profile(agentName); !ok {
			return task.Run{}, notFound("agent " + agentName)
		}
		if prof.Provider != prev.Profile.Provider {
			return task.Run{}, bad("agent " + agentName + " is not " + prev.Profile.Provider)
		}
	}
	if err := c.canContinue(prof, prev.Machine); err != nil {
		return task.Run{}, err
	}
	run := task.Run{ID: node.NewRunID(), Task: prev.Task, Machine: prev.Machine, Agent: firstOf(agentName, prev.Agent), Profile: prof,
		Dir: prev.Dir, From: prev.Machine, Brief: text, Title: prev.Title, Runner: node.RunnerBackground, Resume: prev.Session,
		Parent: prev.ID, Project: runTask(c.st, prev).Project, Dispatcher: who.User}
	if w := prev.Work; w != nil && w.Merge == "" { // in the same worktree (a read-only copy is made anew)
		cp := *w
		cp.Setup = nil
		run.Work = &cp
	}
	if t := runTask(c.st, prev); t.Flow != nil { // it goes on in the task's stage
		run.Stage, run.Judge, run.Check = t.Stage, prev.Judge, prev.Check
		if st := t.Flow.StageOf(t.Stage); st != nil && st.Role == "review" {
			reviewer(&run.Profile, run.Work != nil && run.Work.ReadOnly)
		}
	}
	if prev.Planner {
		run.Stage, run.Planner = task.StagePlan, true
		readOnly(&run.Profile)
	}
	return run, nil
}

// canContinue: prof's agent can go on with a session in the background, and machine's tend can start that, as far as
// is known.
func (c *Coord) canContinue(prof tend.AgentProfile, machine string) error {
	if pr, ok := agent.Get(prof.Provider); !ok || !pr.Caps().Continue {
		return bad("agent " + prof.Name + " cannot continue a session")
	}
	if m := c.ms[machine]; m != nil && m.conn != nil && !slices.Contains(m.hello.Methods, node.MRunResume) {
		return &wire.Error{Code: wire.CodeProto, Detail: ReasonNodeOutdated}
	}
	return nil
}
