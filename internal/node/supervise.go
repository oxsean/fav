package node

import (
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
	"github.com/oxsean/fav/internal/output"
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
	proc.CrashAt("claimed")
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

	log  *rolling    // output.log
	errs *lineWriter // the agent's stderr on its way to the log
	slim *slimmer    // what takes the copies of files out of the log

	in           *streamIn          // a stream run's stdin
	proto        proto              // how it speaks there
	inputs       map[string]pending // requests waiting for an answer, by id
	userDenied   map[string]bool    // tools the user denied
	reqAttention bool               // the attention was set by waiting requests
	answersAt    int64              // how far answers.jsonl has been read
	inboxAt      int64              // how far inbox.jsonl has been read
	interruptsAt int64              // how far interrupts.jsonl has been read
	sent         bool               // a message went to the agent while it ran

	transcript string    // an interactive run's transcript, once found
	liveAt     time.Time // when the pane, hook events and transcript were last read
	liveSize   int64     // the transcript's size when last read: growing is the agent's output
	liveWait   bool      // the attention was set by what they showed, so it goes when they stop showing it
	calm       time.Time // when an interactive run last waited for the user or rested between turns: stall counts from it too
}

// outcome is what the agent's output said about how it ends.
type outcome struct {
	final    string   // its final message so far
	err      string   // the last error it reported
	denied   []string // tools a permission prompt denied (no one answers them in the background)
	last     string   // the newest thing it said
	usage    *agent.Usage
	doing    string    // the tool call its turn is at
	dirty    bool      // last, usage or doing changed since the state was written
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

// finish ends a run someone stopped: nothing it asked for waits any more.
func (s *sup) finish(state, reason string, code *int) error {
	now := time.Now()
	return s.keep(func(st *State) {
		st.State, st.Reason, st.ExitCode, st.EndedAt = state, reason, code, &now
		st.Attention, st.Ask, st.Doing = "", "", ""
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
		st.Last, st.Usage, st.Doing = o.last, o.usage, ""
		s.settleRequests(st)
		if st.Attention == AttentionStalled || s.liveWait {
			st.Attention = ""
		}
		if s.liveWait {
			st.Ask = ""
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
// stops it. An interactive run's quiet time starts again whenever it waits for the user or rests between turns.
func (s *sup) stall() {
	if s.spec.StallAfter <= 0 {
		return
	}
	s.mu.Lock()
	quiet, att := time.Since(later(s.seen, s.calm)) > s.spec.StallAfter, s.st.Attention
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
	s.proto.interrupt(newMarkID("stop_"))
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

// watchLive marks an interactive run while its agent waits for the user, and unmarks it when it goes on. Its
// transcript ending on an unanswered AskUserQuestion is a question (asked, with the question); its latest Claude hook
// event being a permission prompt is a permission (with the tool); a prompt the hooks cannot tell, ExitPlanMode or a
// pane Herdr shows blocked is asked without a question. The transcript growing is the agent's output.
func (s *sup) watchLive() {
	if time.Since(s.liveAt) < liveEvery {
		return
	}
	s.liveAt = time.Now()
	s.mu.Lock()
	pane, session, att, ask := s.st.Pane, s.st.Session, s.st.Attention, s.st.Ask
	s.mu.Unlock()
	status := ""
	if pane != "" {
		status = paneStatus(pane)
	}
	waits, wantAtt, wantAsk, finished := false, "", "", false
	if session != "" && s.spec.Provider == tend.ProviderClaude {
		if s.transcript == "" {
			s.transcript = capture.TranscriptPath(tend.ProviderClaude, session)
		}
		if p, ok := capture.ReadPulse(s.transcript); ok {
			if p.Size != s.liveSize {
				s.liveSize = p.Size
				s.heard()
			}
			finished = p.Finished
			if p.Asking {
				waits, wantAtt, wantAsk = true, AttentionAsked, p.Question
			} else if w, ok := capture.HookWaiting(session, p.Size); ok {
				waits, wantAtt = true, AttentionAsked
				if w.Permission {
					wantAtt, wantAsk = AttentionPermission, w.Summary
					if w.Tool != "" && w.Summary != "" {
						wantAsk = w.Tool + ": " + w.Summary
					}
				}
			}
		}
	}
	if !waits && status == "blocked" {
		waits, wantAtt = true, AttentionAsked
	}
	if waits || finished || status == "idle" || status == "done" {
		s.mu.Lock()
		s.calm = time.Now()
		s.mu.Unlock()
	}
	wantAsk = clip(wantAsk, maxReport)
	switch {
	case waits && (att == "" || att == AttentionStalled || s.liveWait && (att != wantAtt || ask != wantAsk)):
		s.liveWait = true
		s.keep(func(st *State) { st.Attention, st.Ask = wantAtt, wantAsk })
	case !waits && s.liveWait && (att == AttentionAsked || att == AttentionPermission):
		s.liveWait = false
		s.keep(func(st *State) { st.Attention, st.Ask = "", "" })
	case !waits:
		s.liveWait = false
	}
}

// outputAt records when the agent last put anything out, to the minute: within a minute nothing is written.
func (s *sup) outputAt() {
	s.mu.Lock()
	at, was := s.seen.Truncate(time.Minute), s.st.OutputAt
	s.mu.Unlock()
	if at.IsZero() || was != nil && was.Equal(at) {
		return
	}
	s.keep(func(st *State) { st.OutputAt = &at })
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
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
	s.log = &rolling{path: filepath.Join(s.dir, "output.log")}
	s.log.turns.doing = s.doing
	defer s.log.Close()
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
	s.slim = newSlimmer(s.dir, inGit(s.spec.Dir))
	s.slim.cwd = s.spec.Dir
	s.startChanges()
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
		c.Stdin, s.in = r, newStreamIn(w)
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
		// ⚠️ os.Pipe, not StdoutPipe: Wait then returns when the agent exits, not when every child it left closes
		// the output
		or, ow, err := proc.Pipe()
		if err != nil {
			s.end(StateFailed, err.Error(), nil)
			return err
		}
		er, ew, err := proc.Pipe()
		if err != nil {
			or.Close()
			ow.Close()
			s.end(StateFailed, err.Error(), nil)
			return err
		}
		c.Stdout, c.Stderr = ow, ew
		readEnds, writeEnds = []*os.File{or, er}, []*os.File{ow, ew}
		// ⚠️ the pipes are only read on goroutines of their own: logging and reading the output wait for the disk and
		// for the agent's input, and the agent waits whenever its output is not read
		errs := spooled(&pipes, er, maxSpoolErr)
		s.errs = newLineWriter(activity{s.log, s})
		pipes.Add(2)
		go func() { defer pipes.Done(); s.copyOut(or, s.log) }()
		go func() { defer pipes.Done(); io.Copy(s.errs, errs) }()
	}
	if !interactive {
		s.log.mark(Mark{Event: markStart})
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
	proc.CrashAt("started")
	s.keep(func(st *State) {
		st.State, st.Pid, st.PidStart, st.StartedAt, st.Pane = StateRunning, c.Process.Pid, proc.StartTime(c.Process.Pid), &now, pane["pane"]
		st.Caps = s.caps()
	})
	proc.CrashAt("running")
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
			if s.errs != nil {
				s.errs.flush()
			}
			if s.in != nil {
				s.endStream()
			}
			code := c.ProcessState.ExitCode()
			if !interactive {
				s.log.mark(Mark{Event: markExit, Code: &code})
			}
			if asked.IsZero() && s.stopAsked() {
				asked, reason = time.Now(), "asked"
			}
			s.reports()
			s.flushOut()
			proc.CrashAt("ending")
			var ee *exec.ExitError
			well := asked.IsZero() && code == 0 && (err == nil || errors.As(err, &ee))
			if w := s.spec.Work; w != nil && !w.ReadOnly && well {
				s.commitLeft()
			}
			if well && len(s.spec.Check) > 0 {
				s.check()
			}
			s.endChanges()
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
			if s.errs != nil {
				s.errs.stale(time.Now())
			}
			if interactive {
				s.watchLive()
			}
			s.stall()
			s.outputAt()
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
			if s.streamTick() && asked.IsZero() {
				asked, reason, soft = time.Now(), "interrupted", true
			}
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

// copyOut logs the agent's stdout a line at a time and reads each line as it goes: codex's thread id, the final
// message, errors and denied permissions. Deltas of a message being written go to partial.json instead of the log. A line longer than maxLine is logged in pieces as it comes, and not read.
// What says who is signed in, how much of their plan is used or where the agent's own configuration lives stays out
// of the log (scrub).
func (s *sup) copyOut(r io.Reader, log *rolling) {
	q := newSpool(maxSpoolOut)
	go readLines(r, q)
	parts := newPartials(s.dir)
	defer parts.close()
	for {
		line, more, ok := q.take()
		if !ok {
			return
		}
		s.heard()
		if more {
			copyLong(line, q, log)
			continue
		}
		if parts.take(line) {
			continue
		}
		var at logPos
		logged, keep := scrub(line)
		var mark *Mark
		if keep && s.slim != nil {
			logged, keep, mark = s.slim.line(logged)
		}
		if keep {
			at = log.put(logged)
		}
		if mark != nil {
			log.markAt(*mark, at)
		}
		parts.done(line)
		if bytes.HasSuffix(line, []byte("\n")) {
			s.lineAt(line, at)
		}
	}
}

// copyLong logs a line longer than maxLine, of which first is the start and q holds the rest; one scrub might
// change is left out whole.
func copyLong(first []byte, q *spool, log *rolling) {
	next := func() ([]byte, bool) {
		b, more, ok := q.take()
		return b, more && ok
	}
	if mayScrub(first) {
		for more := true; more; {
			_, more = next()
		}
		return
	}
	log.long(first, next)
}

var (
	// dropPatterns mark the lines that stay out of the log whole: claude's answer to initialize (the account) and its rate
	// limits, codex's rate limits (the plan and its use) and hooks (their files, in their ids too).
	dropPatterns = [][]byte{[]byte(`"request_id":"` + initRequest + `"`), []byte(`"type":"rate_limit_event"`),
		[]byte(`"method":"account/rateLimits/updated"`), []byte(`"method":"hook/`)}
	// cuts are fields left out of the lines they appear in: where the agent keeps its configuration, sessions and
	// plugins ("[]": in each element of an array).
	cuts = [][]string{{"result", "codexHome"}, {"result", "instructionSources"}, {"result", "thread", "path"},
		{"params", "thread", "path"}, {"memory_paths"}, {"plugins", "[]", "path"}}
	cutPatterns = [][]byte{[]byte(`"codexHome"`), []byte(`"instructionSources"`), []byte(`"thread":{`), []byte(`"memory_paths"`),
		[]byte(`"plugins":[`)}
)

// mayScrub tells a line scrub might change; a quote inside a JSON string is always escaped, so text that only
// mentions these never matches.
func mayScrub(line []byte) bool {
	for _, p := range slices.Concat(dropPatterns, cutPatterns) {
		if bytes.Contains(line, p) {
			return true
		}
	}
	return false
}

// scrub is a line of the agent's output as the log keeps it, or keep false to leave it out (dropPatterns, cuts).
func scrub(line []byte) (logged []byte, keep bool) {
	if !mayScrub(line) {
		return line, true
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(line, &m) != nil {
		for _, p := range dropPatterns {
			if bytes.Contains(line, p) {
				return nil, false
			}
		}
		return line, true
	}
	var head struct {
		Type     string `json:"type"`
		Method   string `json:"method"`
		Response struct {
			RequestID string `json:"request_id"`
		} `json:"response"`
	}
	json.Unmarshal(line, &head)
	if head.Type == "control_response" && head.Response.RequestID == initRequest || head.Type == "rate_limit_event" ||
		head.Method == "account/rateLimits/updated" || strings.HasPrefix(head.Method, "hook/") {
		return nil, false
	}
	changed := false
	for _, c := range cuts {
		changed = cut(m, c) || changed
	}
	if !changed {
		return line, true
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}
	return append(b, '\n'), true
}

// cut removes the field at path from m, answering whether it was there.
func cut(m map[string]json.RawMessage, path []string) bool {
	v, ok := m[path[0]]
	if !ok {
		return false
	}
	if len(path) == 1 {
		delete(m, path[0])
		return true
	}
	if path[1] == "[]" {
		var items []map[string]json.RawMessage
		if json.Unmarshal(v, &items) != nil {
			return false
		}
		changed := false
		for _, it := range items {
			changed = it != nil && cut(it, path[2:]) || changed
		}
		b, err := json.Marshal(items)
		if !changed || err != nil {
			return false
		}
		m[path[0]] = b
		return true
	}
	var inner map[string]json.RawMessage
	if json.Unmarshal(v, &inner) != nil || !cut(inner, path[1:]) {
		return false
	}
	b, err := json.Marshal(inner)
	if err != nil {
		return false
	}
	m[path[0]] = b
	return true
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

// flushOut writes what the agent said and spent since the last write, and the turn its output is at.
func (s *sup) flushOut() {
	turn := 0
	if s.log != nil {
		turn = s.log.turn().Turn
	}
	s.mu.Lock()
	dirty, last, usage, doing := s.out.dirty || turn != s.st.Turn, s.out.last, s.out.usage, s.out.doing
	s.out.dirty = false
	s.mu.Unlock()
	if dirty {
		s.keep(func(st *State) { st.Last, st.Usage, st.Doing, st.Turn = last, usage, doing, turn })
	}
}

// doing records the tool call the agent's turn is at.
func (s *sup) doing(title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out.doing != title {
		s.out.doing, s.out.dirty = title, true
	}
}

// caps is what the run can do, from how it runs and what its agent supports.
func (s *sup) caps() *agent.RunCaps {
	var pc agent.Caps
	if p, ok := agent.Get(s.spec.Agent); ok {
		pc = p.Caps()
	}
	st := s.in != nil
	return &agent.RunCaps{Steer: st, After: st && pc.Continue, Interrupt: st, AnswerScope: st, Questions: st,
		Continue: pc.Continue && s.spec.Runner != RunnerHerdr, Takeover: pc.Resume}
}

func (s *sup) line(line []byte) { s.lineAt(line, logPos{}) }

// lineAt reads a line of the agent's output, logged at at.
func (s *sup) lineAt(line []byte, at logPos) {
	text := bytes.TrimSpace(line)
	if len(text) == 0 || s.proto != nil && s.proto.line(text, at) {
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
	a.s.heard()
	return a.w.Write(p)
}

// heard marks when the agent last said something.
func (s *sup) heard() {
	s.mu.Lock()
	s.seen = time.Now()
	s.mu.Unlock()
}

// rolling is output.log, moved to output.log.1 once it passes logCap. Each write is whole lines (or a long line's
// pieces, or stderr's half line): the log never rolls inside one. It keeps marks.jsonl beside it: where the log goes
// on in another file, and where the turns begin and end.
type rolling struct {
	mu    sync.Mutex
	path  string
	f     *os.File
	id    string // the open file's
	n     int64
	turns turns
}

// logPos is where a line was written to the log; the zero value is wherever the log is.
type logPos struct {
	file string
	off  int64
}

// mark adds m to marks.jsonl, at where the log is now.
func (l *rolling) mark(m Mark) { l.markAt(m, logPos{}) }

// markAt adds m to marks.jsonl at at.
func (l *rolling) markAt(m Mark, at logPos) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if at.file != "" {
		m.File, m.Off = at.file, at.off
		l.add(m)
	} else if l.open(0) == nil {
		m.Off = l.n
		l.add(m)
	}
}

// add writes m, in the log's file unless it names one. The caller holds mu.
func (l *rolling) add(m Mark) {
	m.Type, m.File, m.At = "tend", cmp.Or(m.File, l.id), time.Now()
	appendLine(filepath.Join(filepath.Dir(l.path), marksFile), m)
}

// turn is where the log's turns stand now.
func (l *rolling) turn() output.State {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.turns.st
}

func (l *rolling) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.open(len(p)); err != nil {
		return 0, err
	}
	return l.write(p)
}

// put writes p, answering where it went.
func (l *rolling) put(p []byte) logPos {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open(len(p)) != nil {
		return logPos{}
	}
	at := logPos{l.id, l.n}
	l.write(p)
	return at
}

// long logs one line in pieces: first, then what next gives while it says the line goes on. Nothing else comes
// between them.
func (l *rolling) long(first []byte, next func() ([]byte, bool)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.open(len(first))
	if err == nil {
		_, err = l.write(first)
	}
	for more := true; more; {
		var b []byte
		if b, more = next(); err == nil {
			_, err = l.write(b)
		}
	}
}

// open rolls the log over when n more bytes would pass logCap, and opens it when it is not. The caller holds mu.
func (l *rolling) open(n int) error {
	if l.f != nil && l.n+int64(n) > logCap {
		l.f.Close()
		l.f = nil
		fileio.Rename(l.path, l.path+".1") // ⚠️ may fail while a reader holds it (Windows): then it grows on and rolls later
	}
	if l.f != nil {
		return nil
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.n = f, fi.Size()
	if id := fileio.IDOf(f); id != l.id {
		l.id = id
		l.turns.restart()
		l.add(Mark{Event: markRoll, Off: l.n})
	}
	return nil
}

func (l *rolling) write(p []byte) (int, error) {
	n, err := l.f.Write(p)
	l.turns.took(l.id, l.n, p[:n], l.add)
	l.n += int64(n)
	return n, err
}

func (l *rolling) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// lineWriter passes on what it is written in whole lines; a half line waits for its end, until it waited
// halfLineWait (stale) or grew to halfLineMax.
type lineWriter struct {
	mu    sync.Mutex
	w     io.Writer
	half  []byte
	since time.Time
}

func newLineWriter(w io.Writer) *lineWriter { return &lineWriter{w: w} }

func (lw *lineWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if len(lw.half) == 0 {
		lw.since = time.Now()
	}
	lw.half = append(lw.half, p...)
	if i := bytes.LastIndexByte(lw.half, '\n'); i >= 0 {
		lw.w.Write(lw.half[:i+1])
		lw.half, lw.since = append(lw.half[:0], lw.half[i+1:]...), time.Now()
	}
	if len(lw.half) >= halfLineMax {
		lw.w.Write(lw.half)
		lw.half = lw.half[:0]
	}
	return len(p), nil
}

// stale passes on a half line that has waited halfLineWait by now.
func (lw *lineWriter) stale(now time.Time) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if len(lw.half) > 0 && now.Sub(lw.since) >= halfLineWait {
		lw.w.Write(lw.half)
		lw.half = lw.half[:0]
	}
}

// flush passes on what is left.
func (lw *lineWriter) flush() { lw.stale(time.Now().Add(halfLineWait)) }

// hookTimeout bounds a hook.
const hookTimeout = 30 * time.Minute

// check runs the run's check hook where the agent worked and records how it went.
func (s *sup) check() {
	exit, tail := s.hook(s.spec.Check, s.spec.Dir, "check")
	s.keep(func(st *State) { st.Check = &agent.CheckResult{Argv: s.spec.Check, Exit: exit, Tail: tail} })
}

// hook runs argv in dir, its output going to the log too in whole lines; it answers the exit code and the end of the
// output.
func (s *sup) hook(argv []string, dir, name string) (int, string) {
	s.keep(func(st *State) { st.Note = clip(name+": "+strings.Join(argv, " "), maxNote) })
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = append(os.Environ(), s.spec.env()...)
	var out bytes.Buffer
	var w io.Writer = &out
	var lw *lineWriter
	if s.log != nil {
		lw = newLineWriter(s.log)
		w = io.MultiWriter(&out, lw)
		s.log.mark(Mark{Event: markHook, Name: name, Phase: phaseBegin})
	}
	c.Stdout, c.Stderr = w, w
	exit := 0
	err := c.Run()
	if lw != nil {
		lw.flush()
		defer func() { s.log.mark(Mark{Event: markHook, Name: name, Phase: phaseEnd, Code: &exit}) }()
	}
	if err != nil {
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
