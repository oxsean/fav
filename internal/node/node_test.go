package node

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/testkit"
	"github.com/oxsean/fav/internal/wire"
)

// TestMain doubles as tend: the supervisor and the fake agent are this binary started with _run / _fake-agent.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "_run" {
		if err := Supervise(os.Args[2]); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "_fake-agent" {
		if err := FakeAgent(os.Args[2:]); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	testkit.Main(m)
}

func fake(args ...string) agent.Profile {
	return agent.Profile{Name: "fake", Provider: agent.ProviderFake, Args: args}
}

func start(t *testing.T, n *Node, p StartParams) Snapshot {
	t.Helper()
	if p.Run == "" {
		p.Run = NewRunID()
	}
	if p.Dir == "" {
		p.Dir = t.TempDir()
	}
	if p.Coordinator == "" {
		p.Coordinator = "c1"
	}
	p.Runner = RunnerBackground
	s, err := n.Start(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func wait(t *testing.T, n *Node, id string, done func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		s, err := n.Snapshot(id)
		if err == nil && done(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s: %+v %v", id, s, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestARunGoesToItsEndAndLeavesItsSession(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "2", "--every", "100ms", "--exit", "3"), Brief: "fix the build"})
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateExited || end.ExitCode == nil || *end.ExitCode != 3 || end.Session == "" || end.Provider != "claude" || end.StartedAt == nil {
		t.Fatalf("%+v", end)
	}
	path := capture.TranscriptPath("claude", end.Session)
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "fix the build") || !strings.Contains(string(b), "fake step 2 of 2") {
		t.Fatalf("the fake transcript holds the brief (from stdin) and its replies: %v %.300s", err, b)
	}
	tail, err := n.Tail(TailParams{Run: s.Run, Before: -1})
	if err != nil || !strings.Contains(tail.Text, "fake step 1 of 2") || !tail.Done || tail.File == "" {
		t.Fatalf("output: %+v %v", tail, err)
	}
	t.Setenv("TEND_HOME", filepath.Dir(n.Dir))
	if got := capture.RunSessions()[end.Session]; got.Run != s.Run || got.Open || got.Provider != "claude" {
		t.Fatalf("run sessions: %+v", got)
	}
}

func TestStartingARunAgainStartsNothing(t *testing.T) {
	n := New(t.TempDir())
	p := StartParams{Run: NewRunID(), Task: "t_1", Profile: fake("--steps", "3", "--every", "100ms"), Dir: t.TempDir(), Coordinator: "c1", Runner: RunnerBackground}
	first, err := n.Start(p)
	if err != nil {
		t.Fatal(err)
	}
	running := wait(t, n, first.Run, func(s Snapshot) bool { return s.State.State == StateRunning })
	again, err := n.Start(p)
	if err != nil || again.Pid != running.Pid || again.Sup != running.Sup {
		t.Fatalf("a replayed start answers the same run: %+v %v", again, err)
	}
	end := wait(t, n, first.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	err = Supervise(n.runDir(first.Run))
	for deadline := time.Now().Add(5 * time.Second); errors.Is(err, filelock.ErrLocked) && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond) // the first supervisor wrote its end and is exiting
		err = Supervise(n.runDir(first.Run))
	}
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := n.Snapshot(first.Run); after.Rev != end.Rev {
		t.Fatal("a second supervisor of a finished run changes nothing")
	}
}

func TestStopEndsTheRun(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "100", "--every", "100ms")})
	wait(t, n, s.Run, func(s Snapshot) bool { return s.State.State == StateRunning })
	if _, err := n.Stop(RunRef{Run: s.Run, Coordinator: "c1"}); err != nil {
		t.Fatal(err)
	}
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateStopped || !end.StopAsked || end.Reason != "asked" {
		t.Fatalf("%+v", end)
	}
}

func TestAGoneSupervisorLeavesTheRunUnknown(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "100", "--every", "100ms")})
	running := wait(t, n, s.Run, func(s Snapshot) bool { return s.State.State == StateRunning && s.Sup > 0 })
	if err := proc.KillPID(running.Sup); err != nil {
		t.Fatalf("kill the supervisor %d: %v", running.Sup, err)
	}
	gone := wait(t, n, s.Run, func(s Snapshot) bool { return s.State.State == StateUnknown })
	if gone.Reason != "supervisor_gone" {
		t.Fatalf("%+v", gone)
	}
	proc.KillPID(running.Pid)
}

func TestARunThatNeverGotItsSupervisorFails(t *testing.T) {
	n := New(t.TempDir())
	n.Launch = func(string, Spec) (string, error) { return "", nil }
	s := start(t, n, StartParams{Task: "t_1", Profile: fake()})
	if s.State.State != StateStarting {
		t.Fatalf("%+v", s)
	}
	age(t, n, s.Run)
	if got, _ := n.Snapshot(s.Run); got.State.State != StateFailed || got.Reason != "not_launched" {
		t.Fatalf("%+v", got)
	}
	if err := Supervise(n.runDir(s.Run)); err != nil {
		t.Fatal(err)
	}
	if got, _ := n.Snapshot(s.Run); got.State.State != StateFailed || got.Pid != 0 {
		t.Fatalf("a supervisor coming after the run was reported not launched starts nothing: %+v", got)
	}
}

// age makes run id look created a minute ago.
func age(t *testing.T, n *Node, id string) {
	t.Helper()
	var spec Spec
	if err := readJSON(filepath.Join(n.runDir(id), "spec.json"), &spec); err != nil {
		t.Fatal(err)
	}
	spec.Created = spec.Created.Add(-time.Minute)
	writeJSON(filepath.Join(n.runDir(id), "spec.json"), spec)
}

func noLaunch(string, Spec) (string, error) { return "", nil }

func TestAStopBeforeTheSupervisorStartsNothing(t *testing.T) {
	n := New(t.TempDir())
	n.Launch = noLaunch
	s := start(t, n, StartParams{Task: "t_1", Profile: fake()})
	if _, err := n.Stop(RunRef{Run: s.Run, Coordinator: "c1"}); err != nil {
		t.Fatal(err)
	}
	if err := Supervise(n.runDir(s.Run)); err != nil {
		t.Fatal(err)
	}
	if got, _ := n.Snapshot(s.Run); got.State.State != StateStopped || got.Reason != "asked" || got.Pid != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestStoppingARunThatNeverArrivedLeavesATombstone(t *testing.T) {
	n := New(t.TempDir())
	launched := 0
	n.Launch = func(string, Spec) (string, error) { launched++; return "", nil }
	id := NewRunID()
	got, err := n.Stop(RunRef{Run: id, Coordinator: "c1"})
	if err != nil || got.State.State != StateStopped || got.Reason != "never_started" {
		t.Fatalf("%+v %v", got, err)
	}
	late := start(t, n, StartParams{Run: id, Task: "t_1", Profile: fake()})
	if late.State.State != StateStopped || launched != 0 {
		t.Fatalf("a start arriving after the stop starts nothing: %+v, %d launches", late, launched)
	}
	if runs, _ := n.List("c1", nil); len(runs) != 1 || runs[0].Run != id {
		t.Fatalf("%+v", runs)
	}
	if runs, _ := n.List("c2", nil); len(runs) != 0 {
		t.Fatalf("another coordinator's tombstone: %+v", runs)
	}
	if _, err := n.Stop(RunRef{Run: "../x"}); wire.Code(err) != wire.CodeBadRequest {
		t.Fatal(err)
	}
}

func TestARunEndsWhenItsAgentExitsThoughAChildKeepsItsOutput(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms", "--leave-child", "1m")})
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateExited || end.ExitCode == nil || *end.ExitCode != 0 {
		t.Fatalf("%+v", end)
	}
}

func TestAVeryLongLineIsLoggedInPieces(t *testing.T) {
	var out strings.Builder
	s := &sup{dir: t.TempDir(), spec: Spec{Thread: true}}
	long := strings.Repeat("x", 200<<10)
	s.copyOut(strings.NewReader(long+"\n"+`{"type":"thread.started","thread_id":"th-1"}`+"\n"), &out)
	if out.Len() != len(long)+1+len(`{"type":"thread.started","thread_id":"th-1"}`)+1 || s.st.Session != "th-1" {
		t.Fatalf("%d bytes, session %q", out.Len(), s.st.Session)
	}
}

func TestAnUnknownRunCanBeAcknowledged(t *testing.T) {
	n := New(t.TempDir())
	n.Launch = noLaunch
	s := start(t, n, StartParams{Task: "t_1", Profile: fake()})
	writeJSON(filepath.Join(n.runDir(s.Run), "claim"), nil)
	writeJSON(filepath.Join(n.runDir(s.Run), "state.json"), State{Rev: 2, State: StateRunning})
	n.List("c1", []string{s.Run})
	if !paths.Exists(filepath.Join(n.runDir(s.Run), "acked")) {
		t.Fatal("a run whose supervisor is gone is acknowledged once its coordinator gave up on it")
	}
}

func TestLimits(t *testing.T) {
	n := New(t.TempDir())
	allowed := t.TempDir()
	n.Limits = Limits{AllowDirs: []string{allowed}}
	if _, err := n.Start(StartParams{Run: NewRunID(), Profile: agent.Profile{Provider: "claude", Permission: "bypassPermissions"}, Dir: allowed}); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("bypass: %v", err)
	}
	if _, err := n.Start(StartParams{Run: NewRunID(), Profile: fake(), Dir: t.TempDir()}); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("outside allow_dirs: %v", err)
	}
	if _, err := n.Start(StartParams{Run: "../x", Profile: fake(), Dir: allowed}); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("run id: %v", err)
	}
	if _, err := n.Start(StartParams{Run: NewRunID(), Profile: fake(), Dir: filepath.Join(allowed, "nope")}); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("missing dir: %v", err)
	}
}

func TestANodeRunsItsOwnProfilesAndNoBypass(t *testing.T) {
	n := New(t.TempDir())
	n.Launch = noLaunch
	dir := t.TempDir()
	sh := agent.Profile{Name: "claude", Provider: agent.ProviderCommand, Command: []string{os.Args[0], "-test.run", "none"}}
	if _, err := n.Start(StartParams{Run: NewRunID(), Profile: sh, Dir: dir}); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a command profile this node does not define: %v", err)
	}
	for _, args := range [][]string{{"--permission-mode=bypassPermissions"}, {"--dangerously-skip-permissions"}} {
		p := agent.Profile{Name: "claude", Provider: "claude", Args: args}
		if _, err := n.Start(StartParams{Run: NewRunID(), Profile: p, Dir: dir}); wire.Code(err) != wire.CodeUnauthorized {
			t.Fatalf("%v: %v", args, err)
		}
	}
	n.Profiles = agent.Profiles([]agent.Profile{{Name: "lint", Provider: agent.ProviderFake, Args: []string{"--steps", "7"}}})
	n.Limits = Limits{AllowProfiles: []string{"lint"}}
	if _, err := n.Start(StartParams{Run: NewRunID(), Profile: fake(), Dir: dir}); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a profile not allowed: %v", err)
	}
	s, err := n.Start(StartParams{Run: NewRunID(), Profile: agent.Profile{Name: "lint", Provider: agent.ProviderCommand, Command: []string{os.Args[0]}}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	var spec Spec
	readJSON(filepath.Join(n.runDir(s.Run), "spec.json"), &spec)
	if !slices.Contains(spec.Argv, "_fake-agent") || !slices.Contains(spec.Argv, "7") {
		t.Fatalf("the node's own lint profile runs, not the coordinator's: %v", spec.Argv)
	}
	n.Limits = Limits{AllowBypass: true}
	if _, err := n.Start(StartParams{Run: NewRunID(), Profile: sh, Dir: dir}); err != nil {
		t.Fatalf("allow_bypass lets any profile run: %v", err)
	}
}

func TestListIsPerCoordinatorAndForgetsOldAcknowledgedRuns(t *testing.T) {
	n := New(t.TempDir())
	n.Launch = func(string, Spec) (string, error) { return "", nil }
	a := start(t, n, StartParams{Task: "t_1", Profile: fake(), Coordinator: "a"})
	start(t, n, StartParams{Task: "t_2", Profile: fake(), Coordinator: "b"})
	runs, _ := n.List("a", nil)
	if len(runs) != 1 || runs[0].Run != a.Run {
		t.Fatalf("%+v", runs)
	}
	now := time.Now()
	writeJSON(filepath.Join(n.runDir(a.Run), "state.json"), State{Rev: 3, State: StateExited, EndedAt: &now})
	n.List("a", []string{a.Run})
	old := now.Add(-keepDone - time.Hour)
	os.Chtimes(filepath.Join(n.runDir(a.Run), "acked"), old, old)
	if runs, _ := n.List("a", nil); len(runs) != 0 {
		t.Fatalf("an acknowledged run past keepDone is removed: %+v", runs)
	}
	if !slices.Equal(Methods, []string{MRunStart, MRunStop, MRunList, MRunTail}) {
		t.Fatal(Methods)
	}
}

func TestTailPagesBackwardFromALineStart(t *testing.T) {
	n := New(t.TempDir())
	id := NewRunID()
	os.MkdirAll(n.runDir(id), 0o700)
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%50))
		b.WriteString("\n")
	}
	os.WriteFile(filepath.Join(n.runDir(id), "output.log"), []byte(b.String()), 0o600)
	var got []string
	before := int64(-1)
	file := ""
	for {
		p, err := n.Tail(TailParams{Run: id, Before: before, Max: 4096, File: file})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(p.Text, "line") && p.Text != "" {
			t.Fatalf("a page starts at a line: %.40q", p.Text)
		}
		got = append([]string{p.Text}, got...)
		if p.Done {
			break
		}
		before, file = p.From, p.File
	}
	if strings.Join(got, "") != b.String() {
		t.Fatal("pages put back together are the whole log")
	}
}

func TestASessionOutlivesItsRunDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TEND_HOME", home)
	n := New(home)
	n.Launch = noLaunch
	s := start(t, n, StartParams{Task: "t_1", Profile: fake(), Title: "fix it"})
	now := time.Now()
	writeJSON(filepath.Join(n.runDir(s.Run), "state.json"), State{Rev: 3, State: StateExited, EndedAt: &now})
	n.List("c1", []string{s.Run})
	old := now.Add(-keepDone - time.Hour)
	os.Chtimes(filepath.Join(n.runDir(s.Run), "acked"), old, old)
	n.List("c1", nil)
	if paths.Exists(n.runDir(s.Run)) {
		t.Fatal("the run directory is removed")
	}
	got, ok := capture.RunSessions()[s.Session]
	if !ok || got.Run != s.Run || got.Title != "fix it" || got.Open {
		t.Fatalf("its session stays a run's session: %+v %v", got, ok)
	}
}

func TestASnapshotWhileARunIsBeingMadeLeavesItToStart(t *testing.T) {
	n := New(t.TempDir())
	id := NewRunID()
	dir := n.runDir(id)
	os.MkdirAll(filepath.Dir(dir), 0o700)
	os.Mkdir(dir, 0o700) // Start has made the directory, not yet its spec
	if s, _ := n.Snapshot(id); s.State.State != StateStarting {
		t.Fatalf("%+v", s)
	}
	writeJSON(filepath.Join(dir, "spec.json"), Spec{Run: id, Coordinator: "c1", Argv: []string{os.Args[0], "-test.run", "none"}, Dir: t.TempDir(), Runner: RunnerBackground, Created: time.Now()})
	os.WriteFile(filepath.Join(dir, "prompt.md"), nil, 0o600)
	if err := Supervise(dir); err != nil {
		t.Fatal(err)
	}
	if s, _ := n.Snapshot(id); s.State.State != StateExited {
		t.Fatalf("the run ran: %+v", s)
	}
}

func TestStartsRacingSnapshotsAllLaunch(t *testing.T) {
	n := New(t.TempDir())
	n.Launch = noLaunch
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			ents, _ := os.ReadDir(filepath.Join(n.Dir, "runs"))
			for _, e := range ents {
				n.Snapshot(e.Name())
			}
		}
	}()
	dir := t.TempDir()
	for range 200 {
		s, err := n.Start(StartParams{Run: NewRunID(), Task: "t", Coordinator: "c1", Profile: fake(), Dir: dir, Runner: RunnerBackground})
		if err != nil || s.State.State != StateStarting {
			close(stop)
			<-done
			t.Fatalf("%+v %v", s, err)
		}
	}
	close(stop)
	<-done
}

func TestAProbeOfTheLockDoesNotSendTheSupervisorAway(t *testing.T) {
	root := t.TempDir()
	for range 50 {
		dir := filepath.Join(root, NewRunID())
		os.MkdirAll(dir, 0o700)
		lock := filepath.Join(dir, "lock")
		os.WriteFile(lock, nil, 0o600)
		writeJSON(filepath.Join(dir, "spec.json"), Spec{Argv: []string{os.Args[0], "-test.run", "none"}, Dir: root, Runner: RunnerBackground, Created: time.Now()})
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				default:
					filelock.Held(lock)
				}
			}
		}()
		err := Supervise(dir)
		close(stop)
		<-done
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDecidingNeverOverwritesAState(t *testing.T) {
	dir := t.TempDir()
	claim(dir)
	code := 0
	writeJSON(filepath.Join(dir, "state.json"), State{Rev: 3, State: StateExited, ExitCode: &code})
	got, err := decide(dir, State{Rev: 1, State: StateFailed, Reason: "not_launched"})
	if err != nil || got.State != StateExited || got.Rev != 3 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestACommandProfileMatchesTheNodesOwnWhateverItsMachine(t *testing.T) {
	own := agent.Profile{Name: "mine", Provider: agent.ProviderCommand, Command: []string{os.Args[0]}, Args: []string{}}
	n := New(t.TempDir())
	n.Profiles = agent.Profiles([]agent.Profile{own})
	sent := own
	sent.Args, sent.Machine = nil, "linux"
	p := StartParams{Run: NewRunID(), Profile: sent, Dir: t.TempDir()}
	if err := n.admit(&p); err != nil {
		t.Fatal(err)
	}
	sent.Command = []string{os.Args[0], "-x"}
	p = StartParams{Run: NewRunID(), Profile: sent, Dir: t.TempDir()}
	if err := n.admit(&p); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatal(err)
	}
}

func TestWithoutBypassClaudeAndCodexTakeOnlyTheNodesArgs(t *testing.T) {
	n := New(t.TempDir())
	n.Launch = noLaunch
	n.Profiles = agent.Profiles([]agent.Profile{{Name: "fast", Provider: "claude", Model: "haiku", Args: []string{"--add-dir", "/x"}}})
	dir := t.TempDir()
	for _, p := range []agent.Profile{
		{Name: "claude", Provider: "claude", Args: []string{"--settings", "{}"}},
		{Name: "codex", Provider: "codex", Args: []string{"-sdanger-full-access"}},
		{Name: "claude", Provider: "claude", Permission: "bypassPermissions"},
		{Name: "codex", Provider: "codex", Permission: "danger-full-access"},
		{Name: "fast", Provider: "claude", Model: "haiku", Args: []string{"--add-dir", "/y"}},
	} {
		if err := n.admit(&StartParams{Run: NewRunID(), Profile: p, Dir: dir}); wire.Code(err) != wire.CodeUnauthorized {
			t.Errorf("%+v: %v", p, err)
		}
	}
	for _, p := range []agent.Profile{
		{Name: "claude", Provider: "claude", Permission: "acceptEdits", Model: "opus"},
		{Name: "codex", Provider: "codex", Permission: "workspace-write"},
		{Name: "fast", Provider: "claude", Model: "haiku", Args: []string{"--add-dir", "/x"}, Machine: "m"},
	} {
		if err := n.admit(&StartParams{Run: NewRunID(), Profile: p, Dir: dir}); err != nil {
			t.Errorf("%+v: %v", p, err)
		}
	}
}
