package node

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
)

// stopGrace is how long an agent asked to stop may take before it is killed.
const stopGrace = 10 * time.Second

// logCap is where output.log rolls over to output.log.1.
const logCap = 16 << 20

// launch starts the supervisor for dir: in a new Herdr tab, or detached in the background.
func (n *Node) launch(dir string, spec Spec) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if spec.Runner == RunnerHerdr {
		ws, _ := herdr.WorkspacesFor("", spec.Dir)
		if len(ws) == 1 {
			pane, err := herdr.CreateTab(ws[0].WorkspaceID, spec.Dir, spec.Title)
			if err == nil {
				if err = herdr.Run(pane.PaneID, shell.POSIX.Join([]string{self, "_run", dir})); err == nil {
					return pane.PaneID, nil
				}
			}
			return "", err
		}
		return "", errors.New("herdr workspace for " + spec.Dir + " is gone")
	}
	c := exec.Command(self, "_run", dir)
	c.Dir = spec.Dir
	if spec.Work != nil { // its worktree is made by the supervisor
		c.Dir = spec.Work.Checkout
	}
	if err := proc.StartDetached(c); err != nil {
		return "", err
	}
	c.Process.Release()
	return "", nil
}

// herdrFits: an interactive run can go to a Herdr tab here: Herdr is up and exactly one workspace covers dir.
func herdrFits(dir string) bool {
	if !herdr.Reachable() {
		return false
	}
	ws, _ := herdr.WorkspacesFor("", dir)
	return len(ws) == 1
}

func underAny(dir string, roots []string) bool {
	real, err := realPath(dir)
	if err != nil {
		return false
	}
	for _, r := range roots {
		if rr, err := filepath.EvalSymlinks(paths.Expand(r)); err == nil && (paths.Same(real, rr) || paths.Under(real, rr)) {
			return true
		}
	}
	return false
}

// realPath is dir with its links resolved; a part that does not exist yet is kept as written.
func realPath(dir string) (string, error) {
	dir = filepath.Clean(dir)
	real, err := filepath.EvalSymlinks(dir)
	if !errors.Is(err, os.ErrNotExist) {
		return real, err
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		return "", err
	}
	p, err := realPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(p, filepath.Base(dir)), nil
}

// Supervise runs the agent of run directory dir to its end and records it; it is `tend _run <dir>`.
func Supervise(dir string) error {
	unlock, err := filelock.TryLock(filepath.Join(dir, "lock"))
	for deadline := time.Now().Add(time.Second); errors.Is(err, filelock.ErrLocked) && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond) // ⚠️ Held probes the lock by taking it for a moment
		unlock, err = filelock.TryLock(filepath.Join(dir, "lock"))
	}
	if err != nil {
		return err // another supervisor has this run
	}
	defer unlock()
	var spec Spec
	if err := readJSON(filepath.Join(dir, "spec.json"), &spec); err != nil {
		return err
	}
	if paths.Exists(filepath.Join(dir, "state.json")) || !claim(dir) { // decided already: never start twice
		return nil
	}
	s := &sup{dir: dir, spec: spec, st: State{State: StateStarting, Provider: spec.Provider, Session: spec.Session, Sup: os.Getpid(),
		Stream: spec.Stream}, inputs: map[string]pending{}, userDenied: map[string]bool{}}
	if s.stopAsked() {
		return s.end(StateStopped, "asked", nil)
	}
	if time.Since(spec.Created) > notLaunched { // snapshots already report it not launched
		return s.end(StateFailed, "not_launched", nil)
	}
	crashAt("claimed")
	if err := s.save(); err != nil {
		return err // ⚠️ the agent starts only after its first state is written: the node reports it not launched
	}
	return s.run()
}

type sup struct {
	dir  string
	spec Spec
	mu   sync.Mutex
	st   State
	out  outcome
	seen time.Time // the agent's latest output
	read int64     // how far reports.jsonl has been read

	in           *streamIn          // a stream run's stdin
	proto        proto              // how it speaks there
	inputs       map[string]pending // requests waiting for an answer, by id
	userDenied   map[string]bool    // tools the user denied
	reqAttention bool               // the attention was set by waiting requests
	answersAt    int64              // how far answers.jsonl has been read
	inboxAt      int64              // how far inbox.jsonl has been read
	sent         bool               // a message went to the agent while it ran

	transcript string    // an interactive run's transcript, once found
	liveAt     time.Time // when the pane, hook events and transcript were last read
	liveAsked  bool      // the attention was set by what they showed, so it goes when they stop showing it
}

// outcome is what the agent's output said about how it ends.
type outcome struct {
	final    string   // its final message so far
	err      string   // the last error it reported
	denied   []string // tools a permission prompt denied (no one answers them in the background)
	last     string   // the newest thing it said
	usage    *agent.Usage
	dirty    bool      // last or usage changed since the state was written
	turnDone time.Time // a stream run's turn ended then and nothing followed yet
}

func (s *sup) save() error {
	s.st.Rev++
	return writeJSON(filepath.Join(s.dir, "state.json"), s.st)
}

func (s *sup) set(f func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.st)
	return s.save()
}

// end records how the run ended: without it the run reads as unknown.
func (s *sup) end(state, reason string, code *int) error {
	now := time.Now()
	return s.keep(func(st *State) { st.State, st.Reason, st.ExitCode, st.EndedAt = state, reason, code, &now })
}

// keep applies f and writes the state, trying again for a while when the write fails.
func (s *sup) keep(f func(*State)) error {
	err := s.set(f)
	for i := 0; err != nil && i < 10; i++ {
		time.Sleep(time.Second)
		err = s.set(func(*State) {})
	}
	return err
}

func (s *sup) stopAsked() bool { return paths.Exists(filepath.Join(s.dir, "stop")) }

// EnvCrashAt names a point where the supervisor exits at once, as if killed: claimed, started (agent running, no
// pid recorded), running (pid recorded), ending (agent exited, no end recorded). Tests only.
const EnvCrashAt = "TEND_CRASH_AT"

func crashAt(point string) {
	if os.Getenv(EnvCrashAt) == point {
		os.Exit(86)
	}
}

// finish ends a run someone stopped: nothing it asked for waits any more.
func (s *sup) finish(state, reason string, code *int) error {
	now := time.Now()
	return s.keep(func(st *State) {
		st.State, st.Reason, st.ExitCode, st.EndedAt = state, reason, code, &now
		st.Attention, st.Ask = "", ""
		s.settleRequests(st)
	})
}

// exited ends a run whose agent exited by itself, with what its output said: a question it ends on, tools it was
// denied, why it failed.
func (s *sup) exited(code int) error {
	s.mu.Lock()
	o := s.out
	s.mu.Unlock()
	tail := ""
	if code != 0 && o.err == "" {
		tail = s.logTail(4 << 10)
	}
	now := time.Now()
	return s.keep(func(st *State) {
		st.State, st.ExitCode, st.EndedAt = StateExited, &code, &now
		st.Last, st.Usage = o.last, o.usage
		s.settleRequests(st)
		if st.Attention == AttentionStalled {
			st.Attention = ""
		}
		if denied := slices.DeleteFunc(slices.Clone(o.denied), func(t string) bool { return s.userDenied[t] }); len(denied) > 0 {
			st.Attention, st.Reason, st.Detail = AttentionPermission, agent.ReasonPermission, strings.Join(denied, ", ")
		}
		if ask := agent.AskOf(o.final); ask != "" {
			st.Attention, st.Ask = AttentionAsked, clip(ask, maxReport)
		}
		if code != 0 || o.err != "" {
			said := cmp.Or(o.err, tail)
			r := agent.Classify(said)
			if r != "" {
				st.Reason = r
			}
			st.Detail = clip(saying(said, r), 300)
		}
	})
}

// reports takes in what the agent reported since the last look (tend run ask / note).
func (s *sup) reports() {
	rs, next := reportsFrom(s.dir, s.read)
	s.read = next
	if len(rs) == 0 {
		return
	}
	s.mu.Lock()
	s.seen = time.Now()
	s.mu.Unlock()
	s.keep(func(st *State) {
		for _, r := range rs {
			switch r.Kind {
			case ReportAsk:
				st.Attention, st.Ask = AttentionAsked, r.Text
			case ReportVerdict:
				st.Verdict = &agent.Verdict{Verdict: r.Verdict, Summary: r.Text, At: r.At}
			case ReportPlan:
				st.Plan = json.RawMessage(r.Text)
			case ReportPR:
				if st.Work == nil {
					st.Work = &agent.Work{}
				}
				st.Work.PR = r.Text
			case ReportNote:
				st.Note = clip(r.Text, maxNote)
				if st.Attention == AttentionStalled {
					st.Attention = ""
				}
			}
		}
	})
}

// stall marks a run whose agent has said nothing for spec.StallAfter, and unmarks it when it speaks again; it never
// stops it.
func (s *sup) stall() {
	if s.spec.StallAfter <= 0 {
		return
	}
	s.mu.Lock()
	quiet, att := time.Since(s.seen) > s.spec.StallAfter, s.st.Attention
	s.mu.Unlock()
	switch {
	case quiet && att == "":
		s.keep(func(st *State) { st.Attention = AttentionStalled })
	case !quiet && att == AttentionStalled:
		s.keep(func(st *State) { st.Attention = "" })
	}
}

// interrupt asks a stream run's agent to end its turn and closes its input; false when the run has no stream.
func (s *sup) interrupt() bool {
	if s.in == nil || s.in.isClosed() {
		return false
	}
	s.proto.interrupt()
	s.in.close()
	return true
}

// liveEvery is how often an interactive run's pane, hook events and transcript are read.
const liveEvery = 3 * time.Second

// paneStatus is Herdr's status of an agent pane (working | idle | blocked | done | unknown), "" when unknown.
var paneStatus = func(pane string) string {
	agents, err := herdr.Agents()
	if err != nil {
		return ""
	}
	for _, a := range agents {
		if a.PaneID == pane {
			return a.AgentStatus
		}
	}
	return ""
}

// watchLive marks an interactive run asked while its agent waits for the user, and unmarks it when it goes on: Herdr
// shows its pane blocked, its latest Claude hook event is a prompt, or its transcript ends on a question.
func (s *sup) watchLive() {
	if time.Since(s.liveAt) < liveEvery {
		return
	}
	s.liveAt = time.Now()
	s.mu.Lock()
	pane, session, att := s.st.Pane, s.st.Session, s.st.Attention
	s.mu.Unlock()
	waiting := pane != "" && paneStatus(pane) == "blocked"
	if !waiting && session != "" && s.spec.Provider == tend.ProviderClaude {
		if s.transcript == "" {
			s.transcript = capture.TranscriptPath(tend.ProviderClaude, session)
		}
		if p, ok := capture.ReadPulse(s.transcript); ok {
			waiting = p.Asking || capture.HookWaiting(session, p.Size)
		}
	}
	switch {
	case waiting && att == "":
		s.liveAsked = true
		s.keep(func(st *State) { st.Attention = AttentionAsked })
	case !waiting && s.liveAsked && att == AttentionAsked:
		s.liveAsked = false
		s.keep(func(st *State) { st.Attention, st.Ask = "", "" })
	case !waiting:
		s.liveAsked = false
	}
}

// logTail is the end of output.log, at most n bytes.
func (s *sup) logTail(n int64) string {
	f, err := os.Open(filepath.Join(s.dir, "output.log"))
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	from := max(0, fi.Size()-n)
	b := make([]byte, fi.Size()-from)
	f.ReadAt(b, from)
	return string(b)
}

// saying is the last line of text that tells reason (stdout and stderr interleave in the log), else its last line.
func saying(text, reason string) string {
	if reason != "" {
		lines := strings.Split(text, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if agent.Classify(lines[i]) == reason {
				return strings.TrimSpace(lines[i])
			}
		}
	}
	return lastLine(text)
}

// lastLine is the last line of text that says something.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// drainWait is how long the agent's output may stay open after it exits (a child it left holds it) before the tree
// is ended.
const drainWait = 2 * time.Second

func (s *sup) run() error {
	var from string
	if w := s.spec.Work; w != nil {
		if w.Merge != "" {
			return s.merge()
		}
		var err error
		if from, err = s.prepare(); err != nil {
			return s.workFailed(err)
		}
	}
	c := exec.Command(s.spec.Argv[0], s.spec.Argv[1:]...)
	c.Dir = s.spec.Dir
	c.Env = append(append(os.Environ(), EnvRun+"="+s.spec.Run, EnvRunDir+"="+s.dir), s.spec.env()...)
	interactive := s.spec.Runner == RunnerHerdr
	if s.spec.Stdin {
		f, err := os.Open(filepath.Join(s.dir, "prompt.md"))
		if err != nil {
			s.end(StateFailed, err.Error(), nil)
			return err
		}
		defer f.Close()
		c.Stdin = f
	} else if s.spec.Stream {
		r, w, err := os.Pipe()
		if err != nil {
			s.end(StateFailed, err.Error(), nil)
			return err
		}
		defer r.Close()
		c.Stdin, s.in = r, &streamIn{w: w}
		s.proto = newProto(s)
		defer s.in.close()
	} else if interactive {
		c.Stdin = os.Stdin
	}
	var pipes sync.WaitGroup
	var readEnds, writeEnds []*os.File
	if interactive {
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
	} else {
		log := &rolling{path: filepath.Join(s.dir, "output.log")}
		defer log.Close()
		out := activity{log, s}
		// ⚠️ os.Pipe, not StdoutPipe: Wait then returns when the agent exits, not when every child it left closes
		// the output
		or, ow, err := os.Pipe()
		if err != nil {
			s.end(StateFailed, err.Error(), nil)
			return err
		}
		er, ew, err := os.Pipe()
		if err != nil {
			or.Close()
			ow.Close()
			s.end(StateFailed, err.Error(), nil)
			return err
		}
		c.Stdout, c.Stderr = ow, ew
		readEnds, writeEnds = []*os.File{or, er}, []*os.File{ow, ew}
		pipes.Add(2)
		go func() { defer pipes.Done(); s.copyOut(or, out) }()
		go func() { defer pipes.Done(); io.Copy(out, er) }()
	}
	tree, err := proc.StartTree(c, interactive)
	closeAll(writeEnds) // the agent has its own copies
	if s.in != nil {
		c.Stdin.(*os.File).Close()
	}
	if err != nil {
		closeAll(readEnds)
		pipes.Wait()
		s.end(StateFailed, err.Error(), nil)
		return err
	}
	now := time.Now()
	var pane map[string]string
	readJSON(filepath.Join(s.dir, "pane.json"), &pane)
	s.mu.Lock()
	s.seen = now
	s.mu.Unlock()
	crashAt("started")
	s.keep(func(st *State) {
		st.State, st.Pid, st.PidStart, st.StartedAt, st.Pane = StateRunning, c.Process.Pid, proc.StartTime(c.Process.Pid), &now, pane["pane"]
	})
	crashAt("running")
	var fast <-chan time.Time
	if s.in != nil {
		s.startStream()
		t := time.NewTicker(streamEvery)
		defer t.Stop()
		fast = t.C
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(sigs)
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var asked time.Time
	reason, soft := "", false // soft: a stop interrupted the turn and waits for the agent to end it
	for {
		select {
		case err := <-done:
			s.drain(tree, &pipes, readEnds)
			if s.in != nil {
				s.endStream()
			}
			code := c.ProcessState.ExitCode()
			if asked.IsZero() && s.stopAsked() {
				asked, reason = time.Now(), "asked"
			}
			s.reports()
			s.flushOut()
			crashAt("ending")
			var ee *exec.ExitError
			well := asked.IsZero() && code == 0 && (err == nil || errors.As(err, &ee))
			if w := s.spec.Work; w != nil && !w.ReadOnly && well {
				s.commitLeft()
			}
			if well && len(s.spec.Check) > 0 {
				s.check()
			}
			if s.spec.Work != nil {
				s.settle(from)
			}
			if !asked.IsZero() {
				return s.finish(StateStopped, reason, &code)
			}
			if err != nil && !errors.As(err, &ee) {
				return s.end(StateFailed, err.Error(), nil)
			}
			return s.exited(code)
		case sig := <-sigs:
			if asked.IsZero() || soft {
				soft = false
				asked, reason = time.Now(), "signal"
				if sig == syscall.SIGHUP {
					reason = "tab_closed"
				}
				tree.Stop()
			}
		case <-tick.C:
			s.reports()
			s.flushOut()
			if interactive {
				s.watchLive()
			} else {
				s.stall()
			}
			switch {
			case asked.IsZero() && s.stopAsked():
				asked, reason = time.Now(), "asked"
				if soft = s.interrupt(); !soft {
					tree.Stop()
				}
			case soft && time.Since(asked) > interruptGrace:
				soft, asked = false, time.Now()
				tree.Stop()
			case !asked.IsZero() && !soft && time.Since(asked) > stopGrace:
				tree.Kill()
			}
		case <-fast:
			s.streamTick()
		}
	}
}

// drain waits for the agent's output to close after it exited; what it left running is ended.
func (s *sup) drain(tree *proc.Tree, pipes *sync.WaitGroup, readEnds []*os.File) {
	closed := make(chan struct{})
	go func() { pipes.Wait(); close(closed) }()
	select {
	case <-closed:
		return
	case <-time.After(drainWait):
	}
	tree.Stop()
	select {
	case <-closed:
		return
	case <-time.After(stopGrace):
	}
	tree.Kill()
	select {
	case <-closed:
	case <-time.After(drainWait):
		closeAll(readEnds)
		<-closed
	}
}

func closeAll(fs []*os.File) {
	for _, f := range fs {
		f.Close()
	}
}

// copyOut writes the agent's stdout to the log and reads it as it goes: codex's thread id, the final message, errors
// and denied permissions; a line longer than the buffer goes to the log in pieces and is not read.
func (s *sup) copyOut(r io.Reader, w io.Writer) {
	br := bufio.NewReaderSize(r, 64<<10)
	piece := false
	for {
		line, err := br.ReadSlice('\n')
		if len(line) > 0 {
			w.Write(line)
			if err == nil && !piece {
				s.line(line)
			}
		}
		piece = errors.Is(err, bufio.ErrBufferFull)
		if err != nil && !piece {
			return
		}
	}
}

// event is the part of a claude stream-json or codex exec --json line the supervisor reads.
type event struct {
	Type     string          `json:"type"`
	Subtype  string          `json:"subtype"`
	ThreadID string          `json:"thread_id"`
	Result   string          `json:"result"`
	IsError  bool            `json:"is_error"`
	Message  json.RawMessage `json:"message"` // codex: the error's text; claude: the assistant's message
	Cost     float64         `json:"total_cost_usd"`
	Turns    int             `json:"num_turns"`
	Usage    struct {
		Input      int64 `json:"input_tokens"`
		CacheRead  int64 `json:"cache_read_input_tokens"`
		CacheWrite int64 `json:"cache_creation_input_tokens"`
		Cached     int64 `json:"cached_input_tokens"` // codex: part of input_tokens
		Output     int64 `json:"output_tokens"`
	} `json:"usage"`
	Denials []struct {
		ToolName string `json:"tool_name"`
	} `json:"permission_denials"`
	Item struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Error json.RawMessage `json:"error"`
}

const (
	maxFinal = 16 << 10
	maxLast  = 500
)

// said records the newest thing the agent said.
func (s *sup) said(text string) {
	if text = strings.TrimSpace(text); text != "" {
		s.out.last, s.out.dirty = clip(text, maxLast), true
	}
}

// spent adds one turn's usage.
func (s *sup) spent(t agent.Usage) {
	u := agent.Usage{}
	if s.out.usage != nil {
		u = *s.out.usage
	}
	u = u.Add(t)
	s.out.usage, s.out.dirty = &u, true
}

// flushOut writes what the agent said and spent since the last write.
func (s *sup) flushOut() {
	s.mu.Lock()
	dirty, last, usage := s.out.dirty, s.out.last, s.out.usage
	s.out.dirty = false
	s.mu.Unlock()
	if dirty {
		s.keep(func(st *State) { st.Last, st.Usage = last, usage })
	}
}

func (s *sup) line(line []byte) {
	text := bytes.TrimSpace(line)
	if len(text) == 0 || s.proto != nil && s.proto.line(text) {
		return
	}
	var ev event
	if text[0] != '{' || json.Unmarshal(text, &ev) != nil || ev.Type == "" {
		if s.spec.Agent != tend.ProviderClaude && s.spec.Agent != tend.ProviderCodex {
			s.mu.Lock()
			s.out.final = clip(string(text), maxFinal) // a plain CLI: its last line is its final message
			s.said(string(text))
			s.mu.Unlock()
		}
		return
	}
	switch ev.Type {
	case "thread.started":
		if s.spec.Thread && ev.ThreadID != "" && !s.bound() {
			s.keep(func(st *State) { st.Session = ev.ThreadID })
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case ev.Type == "result":
		s.out.turnDone = time.Now()
	case ev.Type == "assistant" || ev.Type == "user" || ev.Type == "system" && ev.Subtype == "init":
		s.out.turnDone = time.Time{}
	}
	switch ev.Type {
	case "assistant": // claude
		var m struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(ev.Message, &m) == nil {
			for _, c := range m.Content {
				if c.Type == "text" {
					s.said(c.Text)
				}
			}
		}
	case "turn.completed": // codex
		s.spent(agent.Usage{Input: ev.Usage.Input - ev.Usage.Cached, CacheRead: ev.Usage.Cached, Output: ev.Usage.Output, Turns: 1})
	case "result": // claude's last event
		s.out.final = clip(ev.Result, maxFinal)
		s.spent(agent.Usage{Input: ev.Usage.Input, CacheRead: ev.Usage.CacheRead, CacheWrite: ev.Usage.CacheWrite,
			Output: ev.Usage.Output, CostUSD: ev.Cost, Turns: ev.Turns})
		if ev.IsError {
			s.out.err = clip(cmp.Or(ev.Result, ev.Subtype), maxFinal)
		}
		s.out.denied = s.out.denied[:0]
		for _, d := range ev.Denials {
			if d.ToolName != "" && !slices.Contains(s.out.denied, d.ToolName) {
				s.out.denied = append(s.out.denied, d.ToolName)
			}
		}
	case "item.completed": // codex
		if ev.Item.Type == "agent_message" {
			s.out.final = clip(ev.Item.Text, maxFinal)
			s.said(ev.Item.Text)
		}
	case "error", "turn.failed": // codex
		var msg string
		json.Unmarshal(ev.Message, &msg)
		var e struct {
			Message string `json:"message"`
		}
		if msg == "" && json.Unmarshal(ev.Error, &e) == nil {
			msg = e.Message
		}
		if msg != "" {
			s.out.err = clip(msg, maxFinal)
		}
	}
}

func (s *sup) bound() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Session != ""
}

// activity is the agent's output on its way to the log: it marks when the agent last said something.
type activity struct {
	w io.Writer
	s *sup
}

func (a activity) Write(p []byte) (int, error) {
	a.s.mu.Lock()
	a.s.seen = time.Now()
	a.s.mu.Unlock()
	return a.w.Write(p)
}

// rolling is output.log, moved to output.log.1 once it passes logCap.
type rolling struct {
	mu   sync.Mutex
	path string
	f    *os.File
	n    int64
}

func (l *rolling) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil && l.n+int64(len(p)) > logCap {
		l.f.Close()
		l.f = nil
		fileio.Rename(l.path, l.path+".1") // ⚠️ may fail while a reader holds it (Windows): then it grows on and rolls later
	}
	if l.f == nil {
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return 0, err
		}
		l.f, l.n = f, 0
	}
	n, err := l.f.Write(p)
	l.n += int64(n)
	return n, err
}

func (l *rolling) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}

// hookTimeout bounds a hook.
const hookTimeout = 30 * time.Minute

// check runs the run's check hook where the agent worked and records how it went.
func (s *sup) check() {
	exit, tail := s.hook(s.spec.Check, s.spec.Dir, "check")
	s.keep(func(st *State) { st.Check = &agent.CheckResult{Argv: s.spec.Check, Exit: exit, Tail: tail} })
}

// hook runs argv in dir, its output going to output.log too; it answers the exit code and the end of the output.
func (s *sup) hook(argv []string, dir, name string) (int, string) {
	s.keep(func(st *State) { st.Note = clip(name+": "+strings.Join(argv, " "), maxNote) })
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = append(os.Environ(), s.spec.env()...)
	var out bytes.Buffer
	log, err := os.OpenFile(filepath.Join(s.dir, "output.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		defer log.Close()
		c.Stdout, c.Stderr = io.MultiWriter(&out, log), io.MultiWriter(&out, log)
	} else {
		c.Stdout, c.Stderr = &out, &out
	}
	exit := 0
	if err := c.Run(); err != nil {
		exit = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		}
		if exit == -1 {
			fmt.Fprintln(&out, err)
		}
	}
	tail := out.String()
	if len(tail) > 4<<10 {
		tail = tail[len(tail)-4<<10:]
	}
	return exit, strings.ToValidUTF8(tail, "")
}

// startedNow marks a run without an agent running; it answers when.
func (s *sup) startedNow() *time.Time {
	now := time.Now()
	s.keep(func(st *State) { st.State, st.StartedAt = StateRunning, &now })
	return &now
}

func time1() *time.Time { now := time.Now(); return &now }
