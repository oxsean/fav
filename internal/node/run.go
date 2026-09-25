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
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/proc"
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
}

// Spec is a run frozen at its start.
type Spec struct {
	Run         string    `json:"run"`
	Task        string    `json:"task"`
	Coordinator string    `json:"coordinator"`
	Argv        []string  `json:"argv"`
	Dir         string    `json:"dir"`
	Runner      string    `json:"runner"`
	Stdin       bool      `json:"stdin,omitempty"`  // prompt.md goes to the agent's stdin
	Thread      bool      `json:"thread,omitempty"` // the session id comes as codex's thread.started on stdout
	Provider    string    `json:"provider,omitempty"`
	Session     string    `json:"session,omitempty"`
	Title       string    `json:"title,omitempty"`
	Created     time.Time `json:"created"`
}

// State is what the supervisor writes.
type State struct {
	Rev       int        `json:"rev"`
	State     string     `json:"state"`
	Pid       int        `json:"pid,omitempty"`        // the agent
	Sup       int        `json:"supervisor,omitempty"` // the supervisor
	Pane      string     `json:"pane,omitempty"`
	Provider  string     `json:"provider,omitempty"`
	Session   string     `json:"session,omitempty"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

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

// Start makes run p.Run happen once: a second Start of the same run answers how it goes.
func (n *Node) Start(p StartParams) (Snapshot, error) {
	if !runID.MatchString(p.Run) {
		return Snapshot{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "run id"}
	}
	dir := n.runDir(p.Run)
	if _, err := os.Stat(dir); err == nil {
		return n.Snapshot(p.Run)
	}
	if err := n.admit(&p); err != nil {
		return Snapshot{}, err
	}
	if fi, err := os.Stat(p.Dir); err != nil || !fi.IsDir() {
		return Snapshot{}, &wire.Error{Code: wire.CodeNotFound, Detail: "dir " + p.Dir}
	}
	spec, err := n.spec(p, dir)
	if err != nil {
		return Snapshot{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return Snapshot{}, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) { // another Start of the same run got here first
			return n.Snapshot(p.Run)
		}
		return Snapshot{}, err
	}
	err = fileio.WriteFile(filepath.Join(dir, "prompt.md"), []byte(p.Brief), 0o600)
	if err == nil {
		err = writeJSON(filepath.Join(dir, "spec.json"), spec)
	}
	if err == nil {
		var pane string
		pane, err = n.Launch(dir, spec)
		if err == nil && pane != "" {
			writeJSON(filepath.Join(dir, "pane.json"), map[string]string{"pane": pane})
		}
	}
	if err != nil {
		now := time.Now()
		decide(dir, State{Rev: 1, State: StateFailed, Reason: err.Error(), EndedAt: &now})
	}
	return n.Snapshot(p.Run)
}

// admit applies this machine's limits to p: the profile it runs (with allow_profiles, this machine's own of that
// name), its directory, and no command profile it does not define itself unless bypass is allowed.
func (n *Node) admit(p *StartParams) error {
	l := n.Limits
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
	if len(l.AllowDirs) > 0 && !underAny(p.Dir, l.AllowDirs) {
		return &wire.Error{Code: wire.CodeUnauthorized, Detail: "dir " + p.Dir}
	}
	return nil
}

// safePermissions are the permission modes a coordinator may ask for on a node that allows no bypass.
var safePermissions = map[string][]string{
	tend.ProviderClaude: {"", "default", "acceptEdits", "plan"},
	tend.ProviderCodex:  {"", "read-only", "workspace-write"},
}

// sameRun: a and b start the same command line (their names and the machine they are pinned to aside).
func sameRun(a, b agent.Profile) bool {
	return a.Provider == b.Provider && a.Model == b.Model && a.Permission == b.Permission && a.Stdin == b.Stdin &&
		slices.Equal(a.Command, b.Command) && slices.Equal(a.Args, b.Args)
}

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
	prov, ok := agent.Get(p.Profile.Provider)
	if !ok {
		return Spec{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "provider " + p.Profile.Provider}
	}
	runner := p.Runner
	if runner == RunnerHerdr && p.Profile.Provider != tend.ProviderClaude {
		return Spec{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "runner herdr runs claude only"}
	}
	if runner == "" {
		runner = RunnerBackground
		if p.Profile.Provider == tend.ProviderClaude && herdrFits(p.Dir) {
			runner = RunnerHerdr
		}
	}
	promptFile := filepath.Join(dir, "prompt.md")
	ls := agent.LaunchSpec{Profile: p.Profile, Dir: p.Dir, PromptFile: promptFile, Headless: runner == RunnerBackground, Name: p.Title}
	if prov.Caps().PresetSession {
		ls.SessionID = newUUID()
	}
	stdin := false
	switch {
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
	if err := proc.CheckArgs(cmd.Argv()); err != nil {
		return Spec{}, &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
	}
	return Spec{Run: p.Run, Task: p.Task, Coordinator: p.Coordinator, Argv: cmd.Argv(), Dir: p.Dir, Runner: runner,
		Stdin: stdin, Thread: p.Profile.Provider == tend.ProviderCodex, Provider: agent.SessionProvider(p.Profile.Provider),
		Session: ls.SessionID, Title: p.Title, Created: time.Now()}, nil
}

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
	if err := os.Mkdir(dir, 0o700); err == nil {
		if err := writeJSON(filepath.Join(dir, "spec.json"), Spec{Run: r.Run, Coordinator: r.Coordinator, Created: time.Now()}); err != nil {
			return Snapshot{}, err
		}
		now := time.Now()
		if _, err := decide(dir, State{Rev: 1, State: StateStopped, Reason: "never_started", EndedAt: &now}); err != nil {
			return Snapshot{}, err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return Snapshot{}, err
	}
	if err := fileio.WriteFile(filepath.Join(dir, "stop"), nil, 0o600); err != nil {
		return Snapshot{}, err
	}
	return n.Snapshot(r.Run)
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
	err := readJSON(filepath.Join(dir, "state.json"), &s.State)
	held := filelock.Held(filepath.Join(dir, "lock"))
	if !held && (err != nil || !Terminal(s.State.State)) { // the supervisor may have written and left in between
		err = readJSON(filepath.Join(dir, "state.json"), &s.State)
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
	default: // the agent starts only after its first state is written: it never started
		now := time.Now()
		st, err := decide(dir, State{Rev: 1, State: StateFailed, Reason: "not_launched", Provider: spec.Provider,
			Session: spec.Session, EndedAt: &now})
		if errors.Is(err, os.ErrNotExist) {
			st = State{State: StateStarting, Provider: spec.Provider, Session: spec.Session}
		} else if err != nil {
			return Snapshot{}, err
		}
		s.State = st
	}
	return s, nil
}

// List is every run coordinator started here, oldest first; finished runs it acknowledged long ago are removed.
func (n *Node) List(coordinator string, ack []string) ([]Snapshot, error) {
	for _, id := range ack {
		if runID.MatchString(id) {
			acked := filepath.Join(n.runDir(id), "acked")
			if s, err := n.Snapshot(id); err == nil && (Terminal(s.State.State) || s.State.State == StateUnknown) && !paths.Exists(acked) {
				fileio.WriteFile(acked, nil, 0o600) // its mtime starts the keepDone clock
			}
		}
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
		s, err := n.Snapshot(id)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Run < out[j].Run })
	return out, nil
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
	os.RemoveAll(dir)
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
