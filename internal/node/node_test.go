package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	if len(os.Args) > 2 && os.Args[1] == "_touch" { // a hook: leaves a file where it ran
		cwd, _ := os.Getwd()
		os.WriteFile(os.Args[2], []byte(cwd), 0o600)
		os.Exit(0)
	}
	if len(os.Args) > 3 && os.Args[1] == "_say" { // a hook: says one line on stdout, one on stderr
		fmt.Println(os.Args[2])
		fmt.Fprintln(os.Stderr, os.Args[3])
		os.Exit(0)
	}
	if len(os.Args) > 2 && os.Args[1] == "_act" { // an agent: writes files, removes some, then says lines (actScript)
		var act actScript
		b, _ := os.ReadFile(os.Args[2])
		json.Unmarshal(b, &act)
		for p, text := range act.Write {
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(text), 0o644)
		}
		for _, p := range act.Remove {
			os.Remove(p)
		}
		os.Stdout.WriteString(act.Say)
		os.Exit(0)
	}
	if len(os.Args) > 2 && os.Args[1] == "_cat" { // an agent: says what a file holds on stdout
		b, _ := os.ReadFile(os.Args[2])
		os.Stdout.Write(b)
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
	reap(t, n, s.Run, p.Coordinator)
	return s
}

// reap waits, before the test's directories go, until run has no live supervisor, stopping the run when it has one: a
// supervisor still writing a run directory makes its removal fail. Call it after the directories are made, so that
// its cleanup runs before theirs.
func reap(t *testing.T, n *Node, run, coordinator string) {
	t.Helper()
	launched := reflect.ValueOf(n.Launch).Pointer() == reflect.ValueOf(n.launch).Pointer()
	t.Cleanup(func() {
		dir := n.runDir(run)
		stopped := false
		for deadline := time.Now().Add(20 * time.Second); supervised(dir, launched); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Errorf("run %s: its supervisor still runs", run)
				return
			}
			if !stopped && paths.Exists(filepath.Join(dir, "claim")) {
				n.Stop(RunRef{Run: run, Coordinator: coordinator})
				stopped = true
			}
		}
	})
}

// supervised: a supervisor may still write run directory dir: the one launched for it has not claimed it yet, or the
// one that did still runs. ⚠️ A supervisor records its pid only after its claim, and Supervise called by a test runs
// as the test's own pid.
func supervised(dir string, launched bool) bool {
	if !paths.Exists(dir) {
		return false
	}
	if launched && !paths.Exists(filepath.Join(dir, "claim")) {
		return true
	}
	var st State
	readState(dir, &st)
	return filelock.Held(filepath.Join(dir, "lock")) || st.Sup > 0 && st.Sup != os.Getpid() && proc.Alive(st.Sup)
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
	reap(t, n, first.Run, p.Coordinator)
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

// logIn is a log in a fresh directory, and a way to read what it holds, the rolled part first.
func logIn(t *testing.T) (*rolling, func() string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "output.log")
	l := &rolling{path: path}
	t.Cleanup(func() { l.Close() })
	return l, func() string {
		old, _ := os.ReadFile(path + ".1")
		b, _ := os.ReadFile(path)
		return string(old) + string(b)
	}
}

func TestALineIsReadWholeUpToItsLimit(t *testing.T) {
	started := func(id string, pad int) string {
		return `{"type":"thread.started","thread_id":"` + id + `","pad":"` + strings.Repeat("x", pad) + `"}` + "\n"
	}
	text := strings.Repeat("x", 200<<10) + "\n" + started("th-1", 1<<20)
	log, logged := logIn(t)
	s := &sup{dir: t.TempDir(), spec: Spec{Thread: true}}
	s.copyOut(strings.NewReader(text), log)
	if logged() != text || s.st.Session != "th-1" {
		t.Fatalf("%d bytes, session %q", len(logged()), s.st.Session)
	}
	text = started("th-2", maxLine) + started("th-3", 10)
	log, logged = logIn(t)
	s = &sup{dir: t.TempDir(), spec: Spec{Thread: true}}
	s.copyOut(strings.NewReader(text), log)
	if logged() != text || s.st.Session != "th-3" {
		t.Fatalf("a line past the limit is logged as it came and not read: %d bytes, session %q", len(logged()), s.st.Session)
	}
}

func TestStderrIsLoggedInWholeLines(t *testing.T) {
	log, logged := logIn(t)
	errs := newLineWriter(log)
	errs.Write([]byte("half "))
	log.Write([]byte(`{"type":"system"}` + "\n"))
	errs.Write([]byte("a line\nthe next"))
	if got := logged(); got != `{"type":"system"}`+"\n"+"half a line\n" {
		t.Fatalf("%q", got)
	}
	errs.stale(time.Now())
	if strings.Contains(logged(), "the next") {
		t.Fatal("a fresh half line waits")
	}
	errs.stale(time.Now().Add(halfLineWait))
	if !strings.HasSuffix(logged(), "a line\nthe next") {
		t.Fatalf("an old half line is logged: %q", logged())
	}
	big := strings.Repeat("y", halfLineMax)
	errs.Write([]byte(big))
	if !strings.HasSuffix(logged(), big) {
		t.Fatal("a half line as long as halfLineMax is logged")
	}
}

func TestAHooksOutputGoesThroughTheLog(t *testing.T) {
	log, logged := logIn(t)
	s := &sup{dir: t.TempDir(), log: log}
	exit, tail := s.hook([]string{os.Args[0], "_say", "out line", "err line"}, t.TempDir(), "check")
	got := logged()
	if exit != 0 || !strings.Contains(tail, "out line") || !strings.Contains(got, "out line\n") || !strings.Contains(got, "err line\n") {
		t.Fatalf("exit %d, tail %q, log %q", exit, tail, got)
	}
	if fi, _ := os.Stat(log.path); log.n != fi.Size() {
		t.Fatalf("the log counts %d bytes, the file has %d", log.n, fi.Size())
	}
}

func TestTheAgentsAccountStaysOutOfTheLog(t *testing.T) {
	account := `"account":{"email":"someone@example.com","organization":"Example"}`
	short := `{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{` + account + `}}}`
	long := `{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{` + account +
		`,"commands":"` + strings.Repeat("x", 200<<10) + `"}}}`
	other := `{"type":"control_response","response":{"subtype":"success","request_id":"stop","response":{}}}`
	said := `{"type":"assistant","message":{"content":[{"type":"text","text":"the log says \"request_id\":\"init\" and \"codexHome\""}]}}`
	limits := `{"method":"account/rateLimits/updated","params":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":18},"planType":"pro"}}}`
	longLimits := `{"method":"account/rateLimits/updated","params":{"rateLimits":{"planType":"pro","x":"` + strings.Repeat("y", 200<<10) + `"}}}`
	home := `{"id":1,"result":{"userAgent":"tend/0.155.1","codexHome":"/home/someone/.codex","platformFamily":"unix"}}`
	started := `{"method":"thread/started","params":{"thread":{"id":"th-1"}}}`
	log, logged := logIn(t)
	s := &sup{dir: t.TempDir()}
	s.copyOut(strings.NewReader(strings.Join([]string{short, long, other, said, limits, longLimits, home, started}, "\n")+"\n"), log)
	want := other + "\n" + said + "\n" + `{"id":1,"result":{"platformFamily":"unix","userAgent":"tend/0.155.1"}}` + "\n" + started + "\n"
	if got := logged(); got != want {
		t.Fatalf("logged %d bytes: %.600q", len(got), got)
	}
}

func TestTheAgentsConfigPathsStayOutOfTheLog(t *testing.T) {
	home := "/home/someone"
	limits := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","overageDisabledReason":"org_level_disabled","unifiedWindows":{"five_hour":{"utilization":0.34}}},"uuid":"u1"}`
	initLine := `{"type":"system","subtype":"init","cwd":"/work","session_id":"s1","memory_paths":{"auto":"` + home + `/.claude/projects/-work/memory/"},"plugins":[{"name":"p1","path":"` + home + `/.claude/plugins/cache/p1"}]}`
	start := `{"id":2,"result":{"thread":{"id":"th-1","path":"` + home + `/.codex/sessions/rollout-1.jsonl","cwd":"/work"},"instructionSources":["` + home + `/.codex/AGENTS.md"]}}`
	resumed := `{"id":3,"result":{"thread":{"id":"th-2","path":"` + home + `/.codex/sessions/rollout-2.jsonl","turns":["` + strings.Repeat("t", 200<<10) + `"]}}}`
	started := `{"method":"thread/started","params":{"thread":{"id":"th-1","path":"` + home + `/.codex/sessions/rollout-1.jsonl"}}}`
	hook := `{"method":"hook/started","params":{"run":{"id":"session-start:6:` + home + `/.codex/hooks.json","sourcePath":"` + home + `/.codex/hooks.json"}}}`
	said := `{"type":"assistant","message":{"content":[{"type":"text","text":"a \"path\" in \"thread\""}]}}`
	log, read := logIn(t)
	s := &sup{dir: t.TempDir()}
	s.copyOut(strings.NewReader(strings.Join([]string{limits, initLine, start, resumed, started, hook, said}, "\n")+"\n"), log)
	logged := read()
	lines := strings.Split(strings.TrimSuffix(logged, "\n"), "\n")
	if strings.Contains(logged, home) || strings.Contains(logged, "rate_limit") || len(lines) != 5 || lines[4] != said {
		t.Fatalf("logged %d lines: %.800q", len(lines), logged)
	}
	for _, want := range []string{`"cwd":"/work"`, `"session_id":"s1"`, `"plugins":[{"name":"p1"}]`, `"id":"th-1"`, `"id":"th-2"`, strings.Repeat("t", 200<<10)} {
		if !strings.Contains(logged, want) {
			t.Errorf("the rest of the lines stays: %s missing", want[:min(len(want), 40)])
		}
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
	if _, err := n.Start(StartParams{Run: NewRunID(), Profile: sh, Dir: t.TempDir()}); err != nil {
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
	if !slices.Equal(Methods, []string{MRunStart, MRunStop, MRunList, MRunTail, MRunLine, MRunResume, MAgents, MRunAnswer, MRunSend, MRunInterrupt, MDirs,
		MRunFollow, MRunChanges, MRunDiff, MRunBlob, MRunOutputFind}) {
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
	for range 200 {
		s, err := n.Start(StartParams{Run: NewRunID(), Task: "t", Coordinator: "c1", Profile: fake(), Dir: t.TempDir(), Runner: RunnerBackground})
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
		{Name: "claude", Provider: "claude", Permission: "auto"},
		{Name: "fast", Provider: "claude", Model: "haiku", Args: []string{"--add-dir", "/y"}},
	} {
		if err := n.admit(&StartParams{Run: NewRunID(), Profile: p, Dir: dir}); wire.Code(err) != wire.CodeUnauthorized {
			t.Errorf("%+v: %v", p, err)
		}
	}
	for _, p := range []agent.Profile{
		{Name: "claude", Provider: "claude", Permission: "acceptEdits", Model: "opus"},
		{Name: "codex", Provider: "codex", Permission: "workspace-write"},
		{Name: "claude", Provider: "claude", Permission: "manual"},
		{Name: "claude", Provider: "claude", Permission: "dontAsk"},
		{Name: "fast", Provider: "claude", Model: "haiku", Args: []string{"--add-dir", "/x"}, Machine: "m"},
	} {
		if err := n.admit(&StartParams{Run: NewRunID(), Profile: p, Dir: dir}); err != nil {
			t.Errorf("%+v: %v", p, err)
		}
	}
}
