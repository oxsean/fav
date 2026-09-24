// Package node runs agents on this machine for a coordinator. Each run has a directory under <home>/node/runs: the
// directory itself is the right to start it once, spec.json freezes how, the supervisor process (`tend _run`) holds
// its lock for as long as it lives and writes state.json; a coordinator reads snapshots and converges on them.
package node

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/paths"
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
	// Launch starts the supervisor for a run directory; tests replace it.
	Launch func(dir string, spec Spec) (pane string, err error)
}

// Limits is what this machine lets a coordinator do (mode 2 sets them).
type Limits = fav.NodeConfig

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
	if fi, err := os.Stat(p.Dir); err != nil || !fi.IsDir() {
		return Snapshot{}, &wire.Error{Code: wire.CodeNotFound, Detail: "dir " + p.Dir}
	}
	if err := check(n.Limits, p); err != nil {
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
	spec, err := n.spec(p, dir)
	if err == nil {
		err = fileio.WriteFile(filepath.Join(dir, "prompt.md"), []byte(p.Brief), 0o600)
	}
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
		writeJSON(filepath.Join(dir, "state.json"), State{Rev: 1, State: StateFailed, Reason: err.Error(), EndedAt: &now})
	}
	return n.Snapshot(p.Run)
}

func check(l Limits, p StartParams) error {
	if len(l.AllowProfiles) > 0 && !contains(l.AllowProfiles, p.Profile.Name) {
		return &wire.Error{Code: wire.CodeUnauthorized, Detail: "profile " + p.Profile.Name}
	}
	if !l.AllowBypass && agent.Bypass(p.Profile) {
		return &wire.Error{Code: wire.CodeUnauthorized, Detail: "permission " + p.Profile.Permission}
	}
	if len(l.AllowDirs) > 0 && !underAny(p.Dir, l.AllowDirs) {
		return &wire.Error{Code: wire.CodeUnauthorized, Detail: "dir " + p.Dir}
	}
	return nil
}

// spec builds the frozen command line.
func (n *Node) spec(p StartParams, dir string) (Spec, error) {
	prov, ok := agent.Get(p.Profile.Provider)
	if !ok {
		return Spec{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "provider " + p.Profile.Provider}
	}
	runner := p.Runner
	if runner == "" {
		runner = RunnerBackground
		if p.Profile.Provider == fav.ProviderClaude && herdrFits(p.Dir) {
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
	return Spec{Run: p.Run, Task: p.Task, Coordinator: p.Coordinator, Argv: cmd.Argv(), Dir: p.Dir, Runner: runner,
		Stdin: stdin, Thread: p.Profile.Provider == fav.ProviderCodex, Provider: agent.SessionProvider(p.Profile.Provider),
		Session: ls.SessionID, Title: p.Title, Created: time.Now()}, nil
}

// readBrief is an interactive agent's first message; the brief itself stays in the file (argv shows in ps).
const readBrief = "Read the task brief in %s and do the task it describes."

// Stop asks run id to stop; the supervisor ends it. Stopping a finished run changes nothing.
func (n *Node) Stop(id string) (Snapshot, error) {
	if !runID.MatchString(id) {
		return Snapshot{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "run id"}
	}
	dir := n.runDir(id)
	if _, err := os.Stat(dir); err != nil {
		return Snapshot{}, &wire.Error{Code: wire.CodeNotFound, Detail: id}
	}
	if err := fileio.WriteFile(filepath.Join(dir, "stop"), nil, 0o600); err != nil {
		return Snapshot{}, err
	}
	return n.Snapshot(id)
}

// Snapshot reads run id as it stands.
func (n *Node) Snapshot(id string) (Snapshot, error) {
	dir := n.runDir(id)
	var spec Spec
	if err := readJSON(filepath.Join(dir, "spec.json"), &spec); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, err
	}
	s := Snapshot{Run: id, Task: spec.Task}
	_, err := os.Stat(filepath.Join(dir, "stop"))
	s.StopAsked = err == nil
	if err := readJSON(filepath.Join(dir, "state.json"), &s.State); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, err
		}
		fi, serr := os.Stat(dir)
		if serr != nil {
			return Snapshot{}, &wire.Error{Code: wire.CodeNotFound, Detail: id}
		}
		s.State = State{State: StateStarting, Provider: spec.Provider, Session: spec.Session}
		if time.Since(fi.ModTime()) > notLaunched && !filelock.Held(filepath.Join(dir, "lock")) {
			s.State.State, s.Reason = StateFailed, "not_launched"
		}
		return s, nil
	}
	if !Terminal(s.State.State) && !filelock.Held(filepath.Join(dir, "lock")) {
		s.State.State, s.Reason = StateUnknown, "supervisor_gone"
	}
	return s, nil
}

// List is every run coordinator started here, oldest first; finished runs it acknowledged long ago are removed.
func (n *Node) List(coordinator string, ack []string) ([]Snapshot, error) {
	for _, id := range ack {
		if runID.MatchString(id) {
			acked := filepath.Join(n.runDir(id), "acked")
			if s, err := n.Snapshot(id); err == nil && Terminal(s.State.State) && !paths.Exists(acked) {
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
			os.RemoveAll(dir)
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
