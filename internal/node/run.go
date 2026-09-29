// Package node runs agents on this machine for a coordinator. Each run has a directory under <home>/node/runs: the
// directory itself is the right to start it once, spec.json freezes how, the supervisor process (`tend _run`) holds
// its lock for as long as it lives and writes state.json; a coordinator reads snapshots and converges on them.
package node

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Run states as the node sees them.
const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateExited   = "exited"
	StateStopped  = "stopped"
	StateFailed   = "failed"
	StateUnknown  = "unknown" // the supervisor is gone without writing how the run ended
)

// Terminal: the run will not change any more.
func Terminal(state string) bool {
	return state == StateExited || state == StateStopped || state == StateFailed
}

// Runners.
const (
	RunnerBackground = "background"
	RunnerHerdr      = "herdr"
)

// StartParams is what a coordinator sends to start a run; the node builds the command line itself (it knows its paths).
type StartParams struct {
	Run         string        `json:"run"`
	Task        string        `json:"task"`
	Coordinator string        `json:"coordinator"`
	Profile     agent.Profile `json:"profile"`
	Dir         string        `json:"dir"`
	Brief       string        `json:"brief"`
	Title       string        `json:"title,omitempty"`
	Runner      string        `json:"runner,omitempty"` // "" = herdr when it fits, else background
	Resume      string        `json:"resume,omitempty"` // run.resume: the session the brief continues
	Project     string        `json:"project,omitempty"`
	Dispatcher  *Person       `json:"dispatcher,omitempty"` // who started it: the author of its commits (feature dispatcher)
	Verdict     bool          `json:"verdict,omitempty"`    // it is to end with tend run verdict (feature verdict)
	Check       []string      `json:"check,omitempty"`      // argv run where it worked once it exits well (feature check)
	Work        *Workspace    `json:"work,omitempty"`       // where it works in git (feature worktree); Dir is then the checkout
	Planner     bool          `json:"planner,omitempty"`    // it is to hand in a plan with tend run plan (feature plan)
}

// Person is a user of the coordinator's team, as a node needs them.
type Person struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// FeatureDispatcher: run.start's project and dispatcher are kept, checked and used.
const FeatureDispatcher = "dispatcher"

// FeatureAgentDef: a profile's effort and denied tools are applied.
const FeatureAgentDef = "agentdef"

// FeatureVerdict: a run can be told to end with a verdict, and its verdict is reported.
const FeatureVerdict = "verdict"

// FeatureCheck: run.start's check hook runs after the agent, and how it went is reported.
const FeatureCheck = "check"

// FeatureFiles: a profile's hooks, MCP servers and skills are applied (claude).
const FeatureFiles = "files"

// FeaturePlan: a planner is told to hand in a plan, and the plan is reported.
const FeaturePlan = "plan"

// Spec is a run frozen at its start.
type Spec struct {
	Run         string        `json:"run"`
	Task        string        `json:"task"`
	Coordinator string        `json:"coordinator"`
	Argv        []string      `json:"argv"`
	Dir         string        `json:"dir"`
	Runner      string        `json:"runner"`
	Stdin       bool          `json:"stdin,omitempty"`    // prompt.md goes to the agent's stdin
	Stream      bool          `json:"stream,omitempty"`   // stream-json both ways: prompt.md is the first message
	Thread      bool          `json:"thread,omitempty"`   // the session id comes as codex's thread.started on stdout
	Provider    string        `json:"provider,omitempty"` // whose sessions it leaves
	Agent       string        `json:"agent,omitempty"`    // the profile's provider
	Session     string        `json:"session,omitempty"`
	Title       string        `json:"title,omitempty"`
	StallAfter  time.Duration `json:"stall_after,omitempty"` // no output this long marks it stalled; 0 never
	Created     time.Time     `json:"created"`
	Project     string        `json:"project,omitempty"`
	Dispatcher  *Person       `json:"dispatcher,omitempty"`
	Verdict     bool          `json:"verdict,omitempty"`
	Check       []string      `json:"check,omitempty"`
	Work        *Workspace    `json:"work,omitempty"`
	Planner     bool          `json:"planner,omitempty"`
}

// env is what the agent's environment gains: its commits are authored by the run's dispatcher, and committed by
// this machine's user as usual.
func (s Spec) env() []string {
	if d := s.Dispatcher; d != nil && d.Email != "" {
		return []string{"GIT_AUTHOR_NAME=" + cmp.Or(d.Name, d.ID), "GIT_AUTHOR_EMAIL=" + d.Email}
	}
	return nil
}

// State is what the supervisor writes.
type State struct {
	Rev       int                `json:"rev,omitzero"`
	State     string             `json:"state"`
	Pid       int                `json:"pid,omitempty"`        // the agent
	PidStart  int64              `json:"pid_start,omitempty"`  // when the agent started (proc.StartTime): pid's identity
	Sup       int                `json:"supervisor,omitempty"` // the supervisor
	Pane      string             `json:"pane,omitempty"`
	Provider  string             `json:"provider,omitempty"`
	Session   string             `json:"session,omitempty"`
	ExitCode  *int               `json:"exit_code,omitempty"`
	Reason    string             `json:"reason,omitempty"`
	Detail    string             `json:"detail,omitempty"`    // what the agent or its CLI said about how it ended
	Attention string             `json:"attention,omitempty"` // asked | permission | stalled: someone should look
	Ask       string             `json:"ask,omitempty"`       // the question it asked
	Verdict   *agent.Verdict     `json:"verdict,omitempty"`   // what it concluded (tend run verdict)
	Check     *agent.CheckResult `json:"check,omitempty"`     // how the check hook went after it
	Work      *agent.Work        `json:"work,omitempty"`      // what it did to its task's branch
	Plan      json.RawMessage    `json:"plan,omitempty"`      // the plan it handed in (tend run plan)
	Note      string             `json:"note,omitempty"`      // its latest progress note
	Last      string             `json:"last,omitempty"`      // the newest thing it said
	Usage     *agent.Usage       `json:"usage,omitempty"`
	Stream    bool               `json:"stream,omitempty"`   // it takes answers and messages while it runs
	Requests  []agent.Request    `json:"requests,omitempty"` // what it waits on
	Sends     []agent.Send       `json:"sends,omitempty"`    // messages for it and how far they got
	Caps      *agent.RunCaps     `json:"caps,omitempty"`     // what it can do, once started
	Doing     string             `json:"doing,omitempty"`    // the tool call it is at in its turn
	Turn      int                `json:"turn,omitempty"`     // the turn its output is at (run.interrupt names it)
	StartedAt *time.Time         `json:"started_at,omitzero"`
	EndedAt   *time.Time         `json:"ended_at,omitzero"`
}

// Attentions.
const (
	AttentionAsked      = "asked"      // it asked a question and waits for the answer
	AttentionPermission = "permission" // a tool it needed was denied
	AttentionStalled    = "stalled"    // no output for a long time
)

// Snapshot is a run as a coordinator reads it.
type Snapshot struct {
	Run  string `json:"run"`
	Task string `json:"task"`
	State
	StopAsked bool `json:"stop_asked,omitempty"`
}

// notLaunched: a run directory without state this long after it was made never got its supervisor.
const notLaunched = 30 * time.Second

// keepDone is how long a finished run's directory stays after its coordinator has seen the end.
const keepDone = 7 * 24 * time.Hour

var runID = regexp.MustCompile(`^r_[0-9a-f]{8,32}$`)

// Node is this machine's run keeper.
type Node struct {
	Dir    string // <home>/node
	Limits Limits
	// Profiles are the agents this machine defines: with allow_profiles a run gets its profile from here by name.
	Profiles []agent.Profile
	// Launch starts the supervisor for a run directory; tests replace it.
	Launch func(dir string, spec Spec) (pane string, err error)
	// Probe checks an agent CLI; nil is agent.Probe.
	Probe   func(provider string) agent.Check
	checks  checks
	sweepMu sync.Mutex
	swept   time.Time
	trimmed time.Time // when TrimBlobs last ran

	changesMu sync.Mutex
	live      map[string]liveChanges // running runs' changes, by run

	findOnce sync.Once
	finds    chan struct{} // a slot per find running
}

// Limits is what this machine lets a coordinator do (mode 2 sets them).
type Limits = tend.NodeConfig

// New is the node living in home.
func New(home string) *Node {
	n := &Node{Dir: filepath.Join(home, "node")}
	n.Launch = n.launch
	return n
}

func (n *Node) runDir(id string) string { return filepath.Join(n.Dir, "runs", id) }

// Start makes run p.Run happen once: a second Start of the same run answers how it goes. The run directory appears
// whole (made aside, then renamed into place); a directory or slot another run holds refuses it with conflict.
func (n *Node) Start(p StartParams) (Snapshot, error) {
	if !runID.MatchString(p.Run) {
		return Snapshot{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "run id"}
	}
	dir := n.runDir(p.Run)
	if _, err := os.Stat(dir); err == nil {
		return n.Snapshot(p.Run)
	}
	if w := p.Work; w != nil {
		if err := checkWork(w); err != nil {
			return Snapshot{}, err
		}
		if p.Runner == RunnerHerdr {
			return Snapshot{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "runner herdr has no worktree"}
		}
		p.Dir, p.Runner = w.Checkout, RunnerBackground
	}
	if err := n.admit(&p); err != nil {
		return Snapshot{}, err
	}
	if fi, err := os.Stat(p.Dir); err != nil || !fi.IsDir() {
		return Snapshot{}, &wire.Error{Code: wire.CodeNotFound, Detail: "dir " + p.Dir}
	}
	if p.Work != nil {
		p.Dir = workDir(p.Work, p.Run)
	}
	spec, err := n.spec(p, dir)
	if err != nil {
		return Snapshot{}, err
	}
	blocked := ""
	if !merging(p) {
		blocked = n.Check(p.Profile.Provider, false).Blocker()
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return Snapshot{}, err
	}
	unlock, err := filelock.Lock(filepath.Join(n.Dir, "admit.lock"))
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	if _, err := os.Stat(dir); err == nil {
		return n.Snapshot(p.Run)
	}
	if blocked == "" {
		if busy := n.busy(p.Dir); busy != "" {
			return Snapshot{}, &wire.Error{Code: wire.CodeConflict, Detail: busy}
		}
	}
	files, err := n.agentFiles(p.Profile)
	if err != nil {
		return Snapshot{}, err
	}
	err = n.publish(dir, func(tmp string) error {
		if err := fileio.WriteFile(filepath.Join(tmp, "prompt.md"), []byte(n.brief(p, spec, dir)), 0o600); err != nil {
			return err
		}
		for name, b := range files {
			if err := fileio.WriteFile(filepath.Join(tmp, name), b, 0o600); err != nil {
				return err
			}
		}
		return writeJSON(filepath.Join(tmp, "spec.json"), spec)
	})
	if errors.Is(err, os.ErrExist) { // another Start or a stop's tombstone got here first
		return n.Snapshot(p.Run)
	}
	if err != nil {
		return Snapshot{}, err
	}
	reason := blocked
	if reason == "" {
		var pane string
		pane, err = n.Launch(dir, spec)
		if err == nil && pane != "" {
			writeJSON(filepath.Join(dir, "pane.json"), map[string]string{"pane": pane})
		}
		if err != nil {
			reason = err.Error()
		}
	}
	if reason != "" {
		now := time.Now()
		decide(dir, State{Rev: 1, State: StateFailed, Reason: reason, Provider: spec.Provider, Session: spec.Session, EndedAt: &now})
	}
	return n.Snapshot(p.Run)
}

// publish makes run directory dir whole: fill writes it aside, and it is renamed into place unless dir exists
// (os.ErrExist).
func (n *Node) publish(dir string, fill func(tmp string) error) error {
	var b [4]byte
	rand.Read(b[:])
	tmp := filepath.Join(filepath.Dir(dir), newPrefix+filepath.Base(dir)+"-"+hex.EncodeToString(b[:]))
	if err := os.Mkdir(tmp, 0o700); err != nil {
		return err
	}
	err := fill(tmp)
	if err == nil {
		if _, serr := os.Stat(dir); serr == nil {
			err = os.ErrExist
		} else if err = fileio.Rename(tmp, dir); err != nil && paths.Exists(dir) {
			err = os.ErrExist
		}
	}
	if err != nil {
		os.RemoveAll(tmp)
	}
	return err
}

// newPrefix marks a run directory being made; one left by a crash is removed after staleNew.
const (
	newPrefix = ".new-"
	staleNew  = time.Hour
)

// busy says why this node takes no new run in dir now: another run holds the directory, or every slot is taken; ""
// when it may start. The caller holds the admission lock.
func (n *Node) busy(dir string) string {
	real, _ := realPath(dir)
	ents, _ := os.ReadDir(filepath.Join(n.Dir, "runs"))
	used := 0
	for _, e := range ents {
		if !runID.MatchString(e.Name()) {
			continue
		}
		s, err := n.Snapshot(e.Name())
		if err != nil || !holds(s, n.runDir(e.Name())) {
			continue
		}
		used++
		var spec Spec
		if readJSON(filepath.Join(n.runDir(e.Name()), "spec.json"), &spec) != nil || spec.Dir == "" {
			continue
		}
		if other, err := realPath(spec.Dir); err == nil && real != "" && paths.Same(other, real) {
			return "dir_busy " + e.Name()
		}
	}
	if n.Limits.Slots > 0 && used >= n.Limits.Slots {
		return fmt.Sprintf("slots %d/%d", used, n.Limits.Slots)
	}
	return ""
}

// holds: the run may still be writing its directory: it has not ended, or its supervisor is gone while its agent may
// live on.
func holds(s Snapshot, dir string) bool {
	switch {
	case Terminal(s.State.State):
		return false
	case s.State.State == StateUnknown:
		return s.Pid > 0 && proc.Alive(s.Pid)
	}
	return true
}

// admit applies this machine's limits to p: the profile it runs (with allow_profiles, this machine's own of that
// name), its directory, and no command profile it does not define itself unless bypass is allowed.
func (n *Node) admit(p *StartParams) error {
	l := n.Limits
	if (len(p.Check) > 0 || p.Work != nil && workHooks(p.Work) || len(p.Profile.Hooks) > 0) && !l.AllowBypass && !l.AllowHooks {
		return &wire.Error{Code: wire.CodeUnauthorized, Detail: "hooks (node.allow_hooks)"}
	}
	if len(l.AllowDirs) > 0 && !underAny(p.Dir, l.AllowDirs) {
		return &wire.Error{Code: wire.CodeUnauthorized, Detail: "dir " + p.Dir}
	}
	if pr, ok := l.Projects[p.Project]; ok && p.Project != "" && len(pr.Dirs) > 0 && !underAny(p.Dir, pr.Dirs) {
		return &wire.Error{Code: wire.CodeUnauthorized, Detail: "dir " + p.Dir + " for project " + p.Project}
	}
	if merging(*p) {
		return nil
	}
	own, defined := n.profile(p.Profile.Name)
	if len(l.AllowProfiles) > 0 {
		if !contains(l.AllowProfiles, p.Profile.Name) || !defined {
			return &wire.Error{Code: wire.CodeUnauthorized, Detail: "profile " + p.Profile.Name}
		}
		p.Profile = own
	}
	if !l.AllowBypass && !(defined && sameRun(own, p.Profile)) {
		switch p.Profile.Provider {
		case agent.ProviderCommand:
			return &wire.Error{Code: wire.CodeUnauthorized, Detail: "profile " + p.Profile.Name}
		case tend.ProviderClaude, tend.ProviderCodex: // only the node's own profiles carry args; permissions from a short list
			if len(p.Profile.Args) > 0 || !contains(safePermissions[p.Profile.Provider], p.Profile.Permission) {
				return &wire.Error{Code: wire.CodeUnauthorized, Detail: "profile " + p.Profile.Name}
			}
		}
	}
	if !contains(efforts, p.Profile.Effort) || slices.ContainsFunc(p.Profile.Deny, func(t string) bool { return !toolName.MatchString(t) }) {
		return &wire.Error{Code: wire.CodeBadRequest, Detail: "profile " + p.Profile.Name}
	}
	if err := n.admitFiles(p.Profile); err != nil {
		return err
	}
	return nil
}

// merging: p merges branches and runs no agent.
func merging(p StartParams) bool { return p.Work != nil && p.Work.Merge != "" }

// safePermissions are the permission modes a coordinator may ask for on a node that allows no bypass.
var safePermissions = map[string][]string{
	tend.ProviderClaude: {"", "default", "manual", "acceptEdits", "plan", "dontAsk"}, // not auto: it acts without asking
	tend.ProviderCodex:  {"", "read-only", "workspace-write"},
}

// sameRun: a and b start the same command line (their names and the machine they are pinned to aside).
func sameRun(a, b agent.Profile) bool {
	return a.Provider == b.Provider && a.Model == b.Model && a.Permission == b.Permission && a.Stdin == b.Stdin &&
		a.Effort == b.Effort && slices.Equal(a.Deny, b.Deny) && slices.Equal(a.Command, b.Command) && slices.Equal(a.Args, b.Args)
}

// Efforts a run may ask for; Deny only takes tools away.
var (
	efforts  = []string{"", "low", "medium", "high", "xhigh", "max"}
	toolName = regexp.MustCompile(`^[A-Za-z0-9_*:().-]{1,128}$`)
)

func (n *Node) profile(name string) (agent.Profile, bool) {
	profiles := n.Profiles
	if profiles == nil {
		profiles = agent.Profiles(nil)
	}
	for _, p := range profiles {
		if p.Name == name {
			return p, true
		}
	}
	return agent.Profile{}, false
}

// spec builds the frozen command line.
func (n *Node) spec(p StartParams, dir string) (Spec, error) {
	if merging(p) {
		return Spec{Run: p.Run, Task: p.Task, Coordinator: p.Coordinator, Dir: p.Dir, Runner: RunnerBackground, Title: p.Title,
			Created: time.Now(), Project: p.Project, Dispatcher: p.Dispatcher, Work: p.Work}, nil
	}
	prov, ok := agent.Get(p.Profile.Provider)
	if !ok {
		return Spec{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "provider " + p.Profile.Provider}
	}
	runner := p.Runner
	if runner == RunnerHerdr && p.Profile.Provider != tend.ProviderClaude {
		return Spec{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "runner herdr runs claude only"}
	}
	if p.Resume != "" {
		if !prov.Caps().Continue {
			return Spec{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "provider " + p.Profile.Provider + " cannot continue a session"}
		}
		if runner == RunnerHerdr {
			return Spec{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "runner herdr cannot continue a session"}
		}
		runner = RunnerBackground
	}
	if runner == "" {
		runner = RunnerBackground
		if p.Profile.Provider == tend.ProviderClaude && herdrFits(p.Dir) {
			runner = RunnerHerdr
		}
	}
	promptFile := filepath.Join(dir, "prompt.md")
	ls := agent.LaunchSpec{Profile: p.Profile, Dir: p.Dir, PromptFile: promptFile, Headless: runner == RunnerBackground, Name: p.Title,
		Resume: p.Resume}
	if prov.Caps().PresetSession && p.Resume == "" {
		ls.SessionID = newUUID()
	}
	stdin, stream := false, false
	switch {
	case runner == RunnerBackground && prov.Caps().Stream:
		stream, ls.Stream = true, true
	case runner == RunnerHerdr:
		ls.Prompt = fmt.Sprintf(readBrief, promptFile)
		ls.Profile.Args = append(append([]string(nil), ls.Profile.Args...), "--add-dir", dir)
	case p.Profile.Provider == agent.ProviderCommand:
		stdin = p.Profile.Stdin
	default:
		stdin = true
	}
	cmd, err := prov.Launch(ls)
	if err != nil {
		return Spec{}, err
	}
	if !n.Limits.AllowBypass && agent.BypassArgv(cmd.Argv()) {
		return Spec{}, &wire.Error{Code: wire.CodeUnauthorized, Detail: "permission"}
	}
	if files, _ := n.agentFiles(p.Profile); len(files) > 0 { // the node's own files, made after the check that refuses the caller's
		if files[settingsFile] != nil {
			ls.Settings = filepath.Join(dir, settingsFile)
		}
		if files[mcpFile] != nil {
			ls.MCPConfig = filepath.Join(dir, mcpFile)
		}
		if cmd, err = prov.Launch(ls); err != nil {
			return Spec{}, err
		}
	}
	if err := proc.CheckArgs(cmd.Argv()); err != nil {
		return Spec{}, &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
	}
	return Spec{Run: p.Run, Task: p.Task, Coordinator: p.Coordinator, Argv: cmd.Argv(), Dir: p.Dir, Runner: runner,
		Stdin: stdin, Stream: stream, Thread: p.Profile.Provider == tend.ProviderCodex, Provider: agent.SessionProvider(p.Profile.Provider),
		Agent: p.Profile.Provider, Session: cmp.Or(ls.SessionID, p.Resume), Title: p.Title, StallAfter: n.stallAfter(),
		Created: time.Now(), Project: p.Project, Dispatcher: p.Dispatcher, Verdict: p.Verdict, Check: p.Check, Work: p.Work, Planner: p.Planner}, nil
}

// defaultStall is how long a background run may say nothing before it is marked stalled.
const defaultStall = 15 * time.Minute

func (n *Node) stallAfter() time.Duration {
	switch s := n.Limits.StallAfter; s {
	case "":
		return defaultStall
	case "off", "0":
		return 0
	default:
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
		return defaultStall
	}
}

// brief is what prompt.md holds: the task brief and, for a run no one watches, how to ask and report.
func (n *Node) brief(p StartParams, spec Spec, dir string) string {
	if spec.Runner != RunnerBackground {
		return p.Brief
	}
	self, err := os.Executable()
	if err != nil {
		self = "tend"
	}
	tendCmd := shell.POSIX.Join([]string{self, "run"})
	c := convention
	if spec.Stream {
		c = streamConvention
	}
	out := strings.TrimRight(p.Brief, "\n") + "\n\n" + fmt.Sprintf(c, p.Run, agent.AskMark, tendCmd, tendCmd)
	if spec.Verdict {
		out += fmt.Sprintf(verdictConvention, tendCmd)
	}
	if spec.Planner {
		out += fmt.Sprintf(planConvention, tendCmd)
	}
	switch w := spec.Work; {
	case w == nil:
	case w.ReadOnly:
		out += fmt.Sprintf(copyConvention, w.Branch)
	default:
		out += fmt.Sprintf(workConvention, w.Branch)
	}
	return out
}

// convention tells a background agent how to ask and report: nobody answers a prompt while it runs.
const convention = `---
This task runs unattended under tend (run %[1]s); nobody watches it live and nobody can answer a prompt.
- If you need a decision or information from the user to go on, stop working and make your final message start with
  "%[2]s" followed by the question. The answer comes back as a new message in this session.
- To report progress, you may run: %[3]s note "<one line>"
- To ask while you keep working on something else, you may run: %[4]s ask "<question>"
`

// streamConvention tells a background agent whose prompts are answered remotely how to ask and report.
const streamConvention = `---
This task runs under tend (run %[1]s); nobody watches it live. A permission prompt or a question you ask with
AskUserQuestion waits until the user answers it remotely, which may take a while.
- If you need a decision or information from the user to go on, ask with AskUserQuestion, or stop working and make
  your final message start with "%[2]s" followed by the question.
- To report progress, you may run: %[3]s note "<one line>"
- To ask while you keep working on something else, you may run: %[4]s ask "<question>"
`

// verdictConvention tells a run that judges work how to report its conclusion.
const verdictConvention = `
- You are judging this work. Before you stop, report your conclusion with exactly one of:
  %[1]s verdict pass "<one line>"      (it meets the criteria)
  %[1]s verdict rework "<what to change>"  (it must change; say what)
  %[1]s verdict blocked "<why>"         (you cannot judge it)
`

// planConvention tells a planner how to hand in its plan.
const planConvention = `
- You are planning, not doing: change no file. Hand the plan in as JSON on standard input with
  %[1]s plan - <<'PLAN'
  followed by the JSON and a last line holding only PLAN, unindented. It checks the plan and says what is wrong; fix
  it and hand it in again.
  {"tasks":[{"key":"short-id","title":"…","brief":"what to do, enough to do it without asking","acceptance":["…"],
   "after":["key it comes after"],"parent":"key of the task it is part of (two levels at most)",
   "workflow":"feature|fix|docs|none","size":"S|M|L"}],"questions":["what someone should decide"]}
`

// readBrief is an interactive agent's first message; the brief itself stays in the file (argv shows in ps).
const readBrief = "Read the task brief in %s and do the task it describes."

// Stop asks run r.Run to stop; the supervisor ends it. Stopping a finished run changes nothing; stopping a run whose
// start never arrived leaves it stopped, so a start arriving later starts nothing.
func (n *Node) Stop(r RunRef) (Snapshot, error) {
	if !runID.MatchString(r.Run) {
		return Snapshot{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "run id"}
	}
	dir := n.runDir(r.Run)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return Snapshot{}, err
	}
	if !paths.Exists(dir) {
		now := time.Now()
		err := n.publish(dir, func(tmp string) error { // a tombstone: a start arriving later finds the run stopped
			if err := writeJSON(filepath.Join(tmp, "spec.json"), Spec{Run: r.Run, Coordinator: r.Coordinator, Created: now}); err != nil {
				return err
			}
			if !claim(tmp) {
				return os.ErrExist
			}
			return writeJSON(filepath.Join(tmp, "state.json"), State{Rev: 1, State: StateStopped, Reason: "never_started", EndedAt: &now})
		})
		if err != nil && !errors.Is(err, os.ErrExist) {
			return Snapshot{}, err
		}
	}
	if err := fileio.WriteFile(filepath.Join(dir, "stop"), nil, 0o600); err != nil {
		return Snapshot{}, err
	}
	settle(dir)
	return n.Snapshot(r.Run)
}

// settle ends a run whose supervisor is gone: its agent, if it still runs and is provably the same process (pid and
// start time), is ended, and the run is recorded stopped. An agent whose identity cannot be proved is left running and
// the run unknown. Taking the run's lock is taking the dead supervisor's right to write its state.
func settle(dir string) {
	unlock, err := filelock.TryLock(filepath.Join(dir, "lock"))
	if err != nil {
		return
	}
	defer unlock()
	var st State
	if readState(dir, &st) != nil || Terminal(st.State) {
		return
	}
	reason := "supervisor_gone"
	if st.Pid > 0 && proc.Alive(st.Pid) {
		if now := proc.StartTime(st.Pid); st.PidStart == 0 || now == 0 {
			return
		} else if now == st.PidStart {
			proc.KillTree(st.Pid)
			for deadline := time.Now().Add(2 * time.Second); proc.Alive(st.Pid) && time.Now().Before(deadline); {
				time.Sleep(20 * time.Millisecond)
			}
			if proc.Alive(st.Pid) {
				return
			}
			reason = "orphan_stopped"
		}
	} else if st.State == StateStarting && st.Pid == 0 && st.Sup > 0 {
		return // the supervisor died starting it: whether an agent runs is unknown
	}
	now := time.Now()
	st.Rev++
	st.State, st.Reason, st.EndedAt, st.Attention, st.Ask = StateStopped, reason, &now, "", ""
	writeJSON(filepath.Join(dir, "state.json"), st)
}

// claim takes the one right to decide how run directory dir goes: its supervisor claims it before it writes a state,
// and so does the node when it records that no supervisor came. The first to claim wins.
func claim(dir string) bool {
	f, err := os.OpenFile(filepath.Join(dir, "claim"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// decide records st as how a run no supervisor runs ended, unless a live supervisor claimed it first; it answers the
// state the run has.
func decide(dir string, st State) (State, error) {
	state := filepath.Join(dir, "state.json")
	if claim(dir) || !paths.Exists(state) && !filelock.Held(filepath.Join(dir, "lock")) { // a claimant that died unwritten
		if err := writeJSON(state, st); err != nil {
			return State{}, err
		}
	}
	var got State
	err := readJSON(filepath.Join(dir, "state.json"), &got)
	return got, err
}

// Snapshot reads run id as it stands.
func (n *Node) Snapshot(id string) (Snapshot, error) {
	dir := n.runDir(id)
	if _, err := os.Stat(dir); err != nil {
		return Snapshot{}, &wire.Error{Code: wire.CodeNotFound, Detail: id}
	}
	var spec Spec
	created := time.Time{}
	switch err := readJSON(filepath.Join(dir, "spec.json"), &spec); {
	case err == nil:
		created = spec.Created
	case errors.Is(err, os.ErrNotExist): // Start is still writing it, or died before it did
		if fi, err := os.Stat(dir); err == nil {
			created = fi.ModTime()
		}
	default:
		return Snapshot{}, err
	}
	s := Snapshot{Run: id, Task: spec.Task, StopAsked: paths.Exists(filepath.Join(dir, "stop"))}
	err := readState(dir, &s.State)
	held := filelock.Held(filepath.Join(dir, "lock"))
	if !held && (err != nil || !Terminal(s.State.State)) { // the supervisor may have written and left in between
		err = readState(dir, &s.State)
	}
	switch {
	case err == nil:
		if !Terminal(s.State.State) && !held {
			s.State.State, s.Reason = StateUnknown, "supervisor_gone"
		}
	case !errors.Is(err, os.ErrNotExist):
		return Snapshot{}, err
	case held || !paths.Exists(filepath.Join(dir, "claim")) && time.Since(created) <= notLaunched:
		s.State = State{State: StateStarting, Provider: spec.Provider, Session: spec.Session}
	default: // the agent starts only after its first state is written, and a supervisor this late starts none
		ended := created.Add(notLaunched)
		s.State = State{State: StateFailed, Reason: "not_launched", Provider: spec.Provider, Session: spec.Session, EndedAt: &ended}
	}
	if spec.Stream {
		s.Stream, s.Sends = true, withQueued(dir, s.State)
	}
	return s, nil
}

// List is every run coordinator started here (only, when given), oldest first; finished runs it acknowledged long ago
// are removed.
func (n *Node) List(coordinator string, ack []string, only ...string) ([]Snapshot, error) {
	for _, id := range ack {
		if runID.MatchString(id) {
			acked := filepath.Join(n.runDir(id), "acked")
			if s, err := n.Snapshot(id); err == nil && (Terminal(s.State.State) || s.State.State == StateUnknown) && !paths.Exists(acked) {
				fileio.WriteFile(acked, nil, 0o600) // its mtime starts the keepDone clock
			}
		}
	}
	if len(only) > 0 && !n.sweepDue() {
		var out []Snapshot
		for _, id := range only {
			var spec Spec
			if !runID.MatchString(id) || readJSON(filepath.Join(n.runDir(id), "spec.json"), &spec) == nil && spec.Coordinator != coordinator {
				continue
			}
			if s, err := n.Snapshot(id); err == nil {
				out = append(out, s)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Run < out[j].Run })
		return out, nil
	}
	if n.trimDue() {
		n.TrimBlobs()
	}
	ents, err := os.ReadDir(filepath.Join(n.Dir, "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	for _, e := range ents {
		id := e.Name()
		if strings.HasPrefix(id, newPrefix) {
			if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > staleNew {
				os.RemoveAll(filepath.Join(n.Dir, "runs", id))
			}
			continue
		}
		if !runID.MatchString(id) {
			continue
		}
		dir := n.runDir(id)
		if fi, err := os.Stat(filepath.Join(dir, "acked")); err == nil && time.Since(fi.ModTime()) > keepDone {
			n.forget(id)
			continue
		}
		var spec Spec
		if readJSON(filepath.Join(dir, "spec.json"), &spec) == nil && spec.Coordinator != coordinator {
			continue
		}
		if len(only) > 0 && !slices.Contains(only, id) {
			continue
		}
		s, err := n.Snapshot(id)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Run < out[j].Run })
	return out, nil
}

// sweepEvery is how often a list reads every run directory (removing the ones acknowledged long ago) even when it
// is asked for a few runs.
const sweepEvery = 10 * time.Minute

func (n *Node) sweepDue() bool {
	n.sweepMu.Lock()
	defer n.sweepMu.Unlock()
	if time.Since(n.swept) < sweepEvery {
		return false
	}
	n.swept = time.Now()
	return true
}

func (n *Node) trimDue() bool {
	n.sweepMu.Lock()
	defer n.sweepMu.Unlock()
	if time.Since(n.trimmed) < sweepEvery {
		return false
	}
	n.trimmed = time.Now()
	return true
}

// forget removes run id's directory; its session stays listed as a run's session.
func (n *Node) forget(id string) {
	dir := n.runDir(id)
	var spec Spec
	var st State
	readJSON(filepath.Join(dir, "spec.json"), &spec)
	readJSON(filepath.Join(dir, "state.json"), &st)
	if !Terminal(st.State) && proc.Alive(st.Pid) {
		return // its agent outlived the supervisor: its session stays guarded while it runs
	}
	if sid := cmp.Or(st.Session, spec.Session); sid != "" {
		if err := capture.KeepRunSession(n.Dir, sid, capture.RunSession{Run: id, Provider: spec.Provider, Dir: spec.Dir, Title: spec.Title}); err != nil {
			return
		}
	}
	var tr trees
	if readJSON(filepath.Join(dir, treesFile), &tr) == nil {
		tr.unref(id)
	}
	os.RemoveAll(dir)
}

// readState reads dir's state.json; a read that fails other than for a missing file is tried again for a moment
// (Windows refuses reads while the file is being replaced).
func readState(dir string, st *State) error {
	var err error
	for i := 0; i < 5; i++ {
		if err = readJSON(filepath.Join(dir, "state.json"), st); err == nil || errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return err
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return fileio.WriteFile(path, append(b, '\n'), 0o600)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// NewRunID is a fresh run id.
func NewRunID() string {
	var b [6]byte
	rand.Read(b[:])
	return "r_" + hex.EncodeToString(b[:])
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
