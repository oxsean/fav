package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/tend"
)

func ended(t *testing.T, n *Node, p StartParams) Snapshot {
	t.Helper()
	s := start(t, n, p)
	return wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
}

func TestARunThatCannotStartHereFailsWithWhy(t *testing.T) {
	n := New(t.TempDir())
	launched := false
	n.Launch = func(string, Spec) (string, error) { launched = true; return "", nil }
	for _, c := range []struct {
		check agent.Check
		want  string
	}{
		{agent.Check{}, agent.ReasonCLIMissing},
		{agent.Check{Installed: true, Auth: agent.AuthMissing}, agent.ReasonAuthMissing},
	} {
		n.Probe = func(string) agent.Check { return c.check }
		n.checks = checks{}
		s := start(t, n, StartParams{Task: "t_1", Profile: agent.Profile{Name: "claude", Provider: tend.ProviderClaude}, Brief: "b"})
		if s.State.State != StateFailed || s.Reason != c.want {
			t.Errorf("%+v: %+v", c.check, s)
		}
	}
	if launched {
		t.Fatal("no supervisor starts for a run that cannot")
	}
	n.Probe = func(string) agent.Check { return agent.Check{Installed: true, Auth: agent.AuthUnknown} }
	n.checks = checks{}
	s := start(t, n, StartParams{Task: "t_1", Profile: agent.Profile{Name: "claude", Provider: tend.ProviderClaude}, Brief: "b"})
	if !launched || s.State.State == StateFailed {
		t.Fatalf("a login tend cannot tell does not hold a run back: %+v", s)
	}
}

func TestChecksAreKeptAWhileAndBadOnesBriefly(t *testing.T) {
	n := New(t.TempDir())
	probes := 0
	n.Probe = func(p string) agent.Check {
		probes++
		return agent.Check{Installed: p == tend.ProviderClaude, Auth: agent.AuthOK}
	}
	n.Checks(false)
	got := n.Checks(false)
	if probes != 2 || !got.Agents[tend.ProviderClaude].Installed || got.Agents[tend.ProviderCodex].Installed {
		t.Fatalf("each provider probed once: %d %+v", probes, got)
	}
	n.Checks(true)
	if probes != 4 {
		t.Fatalf("fresh probes again: %d", probes)
	}
}

func TestABackgroundBriefCarriesTheRunConvention(t *testing.T) {
	n := New(t.TempDir())
	n.Launch = noLaunch
	s := start(t, n, StartParams{Task: "t_1", Profile: fake(), Brief: "fix the build\n"})
	b, _ := os.ReadFile(filepath.Join(n.runDir(s.Run), "prompt.md"))
	if !strings.HasPrefix(string(b), "fix the build\n\n---\n") || !strings.Contains(string(b), agent.AskMark) || !strings.Contains(string(b), s.Run) {
		t.Fatalf("%s", b)
	}
}

func TestAFinalMessageThatAsksLeavesTheRunWaiting(t *testing.T) {
	n := New(t.TempDir())
	end := ended(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms", "--final", "Looked.\nASK: Keep the old API?"), Brief: "b"})
	if end.State.State != StateExited || end.Attention != AttentionAsked || end.Ask != "Keep the old API?" {
		t.Fatalf("%+v", end)
	}
}

func TestDeniedPermissionsAreNamed(t *testing.T) {
	n := New(t.TempDir())
	result := `{"type":"result","subtype":"success","is_error":false,"result":"I need approval to run it.","permission_denials":[{"tool_name":"Bash"},{"tool_name":"Write"},{"tool_name":"Bash"}]}`
	end := ended(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms", "--final", result), Brief: "b"})
	if end.Attention != AttentionPermission || end.Reason != agent.ReasonPermission || end.Detail != "Bash, Write" {
		t.Fatalf("%+v", end)
	}
}

func TestAFailureIsClassifiedFromWhatTheCLISaid(t *testing.T) {
	n := New(t.TempDir())
	end := ended(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms", "--exit", "1",
		"--stderr", "Error: You've hit your usage limit. Try again at 3:05 PM."), Brief: "b"})
	if end.Reason != agent.ReasonQuota || !strings.Contains(end.Detail, "usage limit") || *end.ExitCode != 1 {
		t.Fatalf("%+v", end)
	}
	codexErr := `{"type":"turn.failed","error":{"message":"stream disconnected before completion"}}`
	end = ended(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms", "--exit", "1", "--final", codexErr), Brief: "b"})
	if end.Reason != agent.ReasonNetwork || end.Detail != "stream disconnected before completion" {
		t.Fatalf("%+v", end)
	}
}

func TestReportsReachTheRunState(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "3", "--every", "400ms", "--note", "halfway", "--ask-report", "Which branch?"), Brief: "b"})
	got := wait(t, n, s.Run, func(s Snapshot) bool { return s.Note != "" && s.Ask != "" })
	if got.Note != "halfway" || got.Ask != "Which branch?" || got.Attention != AttentionAsked || Terminal(got.State.State) {
		t.Fatalf("reports arrive while it runs: %+v", got)
	}
	if err := AddReport(t.TempDir(), ReportNote, "x"); err == nil {
		t.Fatal("a directory that is no run takes no report")
	}
}

func TestASilentRunIsMarkedStalledAndUnmarkedWhenItSpeaks(t *testing.T) {
	n := New(t.TempDir())
	n.Limits.StallAfter = "1s"
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "2", "--every", "2500ms"), Brief: "b"})
	wait(t, n, s.Run, func(s Snapshot) bool { return s.Attention == AttentionStalled })
	got := wait(t, n, s.Run, func(s Snapshot) bool { return s.Attention == "" || Terminal(s.State.State) })
	if got.Attention != "" {
		t.Fatalf("%+v", got)
	}
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateExited || end.Attention == AttentionStalled {
		t.Fatalf("stalled is never an end: %+v", end)
	}
}

func TestResumeContinuesTheSession(t *testing.T) {
	n, dir := New(t.TempDir()), t.TempDir()
	first := ended(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms", "--final", "ASK: Which one?"), Dir: dir, Brief: "pick one"})
	second := ended(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "1", "--every", "10ms"), Dir: dir, Brief: "the second", Resume: first.Session})
	if second.Session != first.Session || second.State.State != StateExited || second.Attention != "" {
		t.Fatalf("%+v", second)
	}
	b, _ := os.ReadFile(capture.TranscriptPath("claude", first.Session))
	if !strings.Contains(string(b), "pick one") || !strings.Contains(string(b), "the second") {
		t.Fatalf("one transcript holds both turns: %.400s", b)
	}
	if _, err := n.Start(StartParams{Run: NewRunID(), Profile: fake(), Dir: t.TempDir(), Resume: "s", Runner: RunnerHerdr}); err == nil {
		t.Fatal("continuing runs in the background only")
	}
}

func TestAFailuresDetailIsTheLineThatToldItWhereverTheLogPutIt(t *testing.T) {
	log := "fake step 1 of 1\nError: You've hit your usage limit. Try again at 3:05 PM.\nfake step 1 of 1\n"
	if got := saying(log, agent.ReasonQuota); !strings.Contains(got, "usage limit") {
		t.Fatal(got)
	}
	if got := saying("a\nb\n", ""); got != "b" {
		t.Fatal(got)
	}
}
