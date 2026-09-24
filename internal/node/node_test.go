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
	t.Setenv("FAV_HOME", filepath.Dir(n.Dir))
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
	if _, err := n.Stop(s.Run); err != nil {
		t.Fatal(err)
	}
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateStopped || !end.StopAsked || end.Reason != "asked" {
		t.Fatalf("%+v", end)
	}
	if _, err := n.Stop("r_00000000"); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("stopping a run nobody started: %v", err)
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
	old := time.Now().Add(-time.Minute)
	os.Chtimes(n.runDir(s.Run), old, old)
	if got, _ := n.Snapshot(s.Run); got.State.State != StateFailed || got.Reason != "not_launched" {
		t.Fatalf("%+v", got)
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
