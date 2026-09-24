package node

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/shell"
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
	s := &sup{dir: dir, spec: spec, st: State{State: StateStarting, Provider: spec.Provider, Session: spec.Session, Sup: os.Getpid()}}
	if s.stopAsked() {
		return s.end(StateStopped, "asked", nil)
	}
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

// drainWait is how long the agent's output may stay open after it exits (a child it left holds it) before the tree
// is ended.
const drainWait = 2 * time.Second

func (s *sup) run() error {
	c := exec.Command(s.spec.Argv[0], s.spec.Argv[1:]...)
	c.Dir = s.spec.Dir
	c.Env = append(os.Environ(), "TEND_RUN="+s.spec.Run)
	interactive := s.spec.Runner == RunnerHerdr
	if s.spec.Stdin {
		f, err := os.Open(filepath.Join(s.dir, "prompt.md"))
		if err != nil {
			s.end(StateFailed, err.Error(), nil)
			return err
		}
		defer f.Close()
		c.Stdin = f
	} else if interactive {
		c.Stdin = os.Stdin
	}
	var pipes sync.WaitGroup
	var readEnds, writeEnds []*os.File
	if interactive {
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
	} else {
		out := &rolling{path: filepath.Join(s.dir, "output.log")}
		defer out.Close()
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
	if err != nil {
		closeAll(readEnds)
		pipes.Wait()
		s.end(StateFailed, err.Error(), nil)
		return err
	}
	now := time.Now()
	var pane map[string]string
	readJSON(filepath.Join(s.dir, "pane.json"), &pane)
	s.keep(func(st *State) {
		st.State, st.Pid, st.StartedAt, st.Pane = StateRunning, c.Process.Pid, &now, pane["pane"]
	})

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(sigs)
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var asked time.Time
	reason := ""
	for {
		select {
		case err := <-done:
			s.drain(tree, &pipes, readEnds)
			code := c.ProcessState.ExitCode()
			if asked.IsZero() && s.stopAsked() {
				asked, reason = time.Now(), "asked"
			}
			if !asked.IsZero() {
				return s.end(StateStopped, reason, &code)
			}
			var ee *exec.ExitError
			if err != nil && !errors.As(err, &ee) {
				return s.end(StateFailed, err.Error(), nil)
			}
			return s.end(StateExited, "", &code)
		case sig := <-sigs:
			if asked.IsZero() {
				asked, reason = time.Now(), "signal"
				if sig == syscall.SIGHUP {
					reason = "tab_closed"
				}
				tree.Stop()
			}
		case <-tick.C:
			if asked.IsZero() {
				if s.stopAsked() {
					asked, reason = time.Now(), "asked"
					tree.Stop()
				}
			} else if time.Since(asked) > stopGrace {
				tree.Kill()
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

// copyOut writes the agent's stdout to the log and picks codex's thread id out of its JSON events; a line longer
// than the buffer goes to the log in pieces and is not parsed.
func (s *sup) copyOut(r io.Reader, w io.Writer) {
	br := bufio.NewReaderSize(r, 64<<10)
	piece := false
	for {
		line, err := br.ReadSlice('\n')
		if len(line) > 0 {
			w.Write(line)
			if err == nil && !piece && s.spec.Thread && !s.bound() && bytes.Contains(line, []byte(`"thread.started"`)) {
				var ev struct {
					Type     string `json:"type"`
					ThreadID string `json:"thread_id"`
				}
				if json.Unmarshal(line, &ev) == nil && ev.ThreadID != "" {
					s.keep(func(st *State) { st.Session = ev.ThreadID })
				}
			}
		}
		piece = errors.Is(err, bufio.ErrBufferFull)
		if err != nil && !piece {
			return
		}
	}
}

func (s *sup) bound() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Session != ""
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
