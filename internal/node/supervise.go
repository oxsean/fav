package node

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

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
		spec.Runner = RunnerBackground // the workspace went away between planning and launch
		writeJSON(filepath.Join(dir, "spec.json"), spec)
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
	real, err := filepath.EvalSymlinks(dir)
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

// Supervise runs the agent of run directory dir to its end and records it; it is `tend _run <dir>`.
func Supervise(dir string) error {
	unlock, err := filelock.TryLock(filepath.Join(dir, "lock"))
	if err != nil {
		return err // another supervisor has this run
	}
	defer unlock()
	var spec Spec
	if err := readJSON(filepath.Join(dir, "spec.json"), &spec); err != nil {
		return err
	}
	var prev State
	if readJSON(filepath.Join(dir, "state.json"), &prev) == nil { // a supervisor already ran: never start twice
		return nil
	}
	s := &sup{dir: dir, spec: spec, st: State{State: StateStarting, Provider: spec.Provider, Session: spec.Session, Sup: os.Getpid()}}
	s.save()
	return s.run()
}

type sup struct {
	dir  string
	spec Spec
	mu   sync.Mutex
	st   State
}

func (s *sup) save() {
	s.st.Rev++
	writeJSON(filepath.Join(s.dir, "state.json"), s.st)
}

func (s *sup) set(f func(*State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.st)
	s.save()
}

func (s *sup) end(state, reason string, code *int) {
	now := time.Now()
	s.set(func(st *State) { st.State, st.Reason, st.ExitCode, st.EndedAt = state, reason, code, &now })
}

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
	var out *rolling
	var pipes sync.WaitGroup
	if interactive {
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
	} else {
		out = &rolling{path: filepath.Join(s.dir, "output.log")}
		defer out.Close()
		stdout, err := c.StdoutPipe()
		if err != nil {
			s.end(StateFailed, err.Error(), nil)
			return err
		}
		c.Stderr = out
		pipes.Add(1)
		go func() { defer pipes.Done(); s.copyOut(stdout, out) }()
	}
	tree, err := proc.StartTree(c, interactive)
	if err != nil {
		pipes.Wait()
		s.end(StateFailed, err.Error(), nil)
		return err
	}
	now := time.Now()
	var pane map[string]string
	readJSON(filepath.Join(s.dir, "pane.json"), &pane)
	s.set(func(st *State) {
		st.State, st.Pid, st.StartedAt, st.Pane = StateRunning, c.Process.Pid, &now, pane["pane"]
	})

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(sigs)
	done := make(chan error, 1)
	go func() { pipes.Wait(); done <- c.Wait() }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var asked time.Time
	reason := ""
	for {
		select {
		case err := <-done:
			code := c.ProcessState.ExitCode()
			if !asked.IsZero() {
				s.end(StateStopped, reason, &code)
				return nil
			}
			var ee *exec.ExitError
			if err != nil && !errors.As(err, &ee) {
				s.end(StateFailed, err.Error(), nil)
				return nil
			}
			s.end(StateExited, "", &code)
			return nil
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
				if _, err := os.Stat(filepath.Join(s.dir, "stop")); err == nil {
					asked, reason = time.Now(), "asked"
					tree.Stop()
				}
			} else if time.Since(asked) > stopGrace {
				tree.Kill()
			}
		}
	}
}

// copyOut writes the agent's stdout to the log and picks codex's thread id out of its JSON events.
func (s *sup) copyOut(r io.Reader, w io.Writer) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			w.Write(line)
			if s.spec.Thread && !s.bound() && strings.Contains(string(line), `"thread.started"`) {
				var ev struct {
					Type     string `json:"type"`
					ThreadID string `json:"thread_id"`
				}
				if json.Unmarshal(line, &ev) == nil && ev.ThreadID != "" {
					s.set(func(st *State) { st.Session = ev.ThreadID })
				}
			}
		}
		if err != nil {
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
	if l.f == nil || l.n+int64(len(p)) > logCap {
		if l.f != nil {
			l.f.Close()
			os.Rename(l.path, l.path+".1")
		}
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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
