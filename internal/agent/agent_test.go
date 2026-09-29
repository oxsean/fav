package agent

import (
	"os"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/tend"
)

func TestLaunchLines(t *testing.T) {
	sid := "fa000001-0c1a-4de0-8000-000000000001"
	for _, c := range []struct {
		name string
		s    LaunchSpec
		want []string
	}{
		{"claude interactive", LaunchSpec{Profile: Profile{Provider: "claude", Model: "haiku", Permission: "acceptEdits"}, Dir: "/w", Prompt: "do it", SessionID: sid, Name: "fix ci"},
			[]string{"claude", "--session-id", sid, "--name", "fix ci", "--model", "haiku", "--permission-mode", "acceptEdits", "do it"}},
		{"claude headless, brief on stdin", LaunchSpec{Profile: Profile{Provider: "claude"}, Dir: "/w", SessionID: sid, Headless: true, Name: "x"},
			[]string{"claude", "-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--disallowedTools", "AskUserQuestion", "--session-id", sid}},
		{"claude continuing a session", LaunchSpec{Profile: Profile{Provider: "claude"}, Dir: "/w", Resume: "s0", Headless: true},
			[]string{"claude", "-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--disallowedTools", "AskUserQuestion", "--resume", "s0"}},
		{"codex headless, brief on stdin", LaunchSpec{Profile: Profile{Provider: "codex", Model: "gpt-x", Permission: "workspace-write", Args: []string{"-c", "k=v"}}, Dir: "/w", Headless: true},
			[]string{"codex", "exec", "--json", "--skip-git-repo-check", "-C", "/w", "-m", "gpt-x", "--sandbox", "workspace-write", "-c", "k=v", "-"}},
		{"codex continuing a session", LaunchSpec{Profile: Profile{Provider: "codex"}, Dir: "/w", Resume: "th1", Headless: true},
			[]string{"codex", "exec", "--json", "--skip-git-repo-check", "-C", "/w", "resume", "th1", "-"}},
		{"codex interactive", LaunchSpec{Profile: Profile{Provider: "codex"}, Dir: "/w", Prompt: "do it"},
			[]string{"codex", "-C", "/w", "do it"}},
	} {
		got, err := LaunchOf(c.s)
		if err != nil || !slices.Equal(got.Argv(), c.want) || got.Cwd != "/w" {
			t.Errorf("%s: %q %v", c.name, got.Argv(), err)
		}
	}
}

func TestCommandTemplates(t *testing.T) {
	exe, _ := os.Executable() // a CLI that exists on every platform
	s := LaunchSpec{Profile: Profile{Provider: ProviderCommand, Command: []string{exe, "-c", "cat {prompt_file}", "--model", "{model}", "{dir}"}}, Dir: "/w", PromptFile: "/p.md"}
	got, err := LaunchOf(s)
	if err != nil || !slices.Equal(got.Argv(), []string{exe, "-c", "cat /p.md", "--model", "/w"}) {
		t.Fatalf("an empty placeholder argument is dropped, the rest filled: %q %v", got.Argv(), err)
	}
	if _, err := LaunchOf(LaunchSpec{Profile: Profile{Provider: ProviderCommand, Command: []string{"no-such-cli-xyz", "{prompt}"}}}); err == nil {
		t.Fatal("a CLI that is not installed")
	}
	if _, err := LaunchOf(LaunchSpec{Profile: Profile{Provider: "nope"}}); err == nil {
		t.Fatal("an unknown provider")
	}
}

func TestResumeForkStart(t *testing.T) {
	r := &tend.Rec{Provider: tend.ProviderCodex, SessionID: "s1", Cwd: "/w"}
	if c, err := ResumeOf(r, "n"); err != nil || !slices.Equal(c.Argv(), []string{"codex", "resume", "s1"}) || c.Cwd != "/w" {
		t.Fatalf("%q %v", c.Argv(), err)
	}
	if c, err := ForkOf(&tend.Rec{Provider: tend.ProviderClaude, SessionID: "s1"}); err != nil || !slices.Equal(c.Argv(), []string{"claude", "--resume", "s1", "--fork-session"}) {
		t.Fatalf("%q %v", c.Argv(), err)
	}
	if _, err := ResumeOf(&tend.Rec{Provider: tend.ProviderClaude}, ""); err == nil {
		t.Fatal("no session id")
	}
	if _, err := ResumeOf(&tend.Rec{Provider: ProviderCommand, SessionID: "x"}, ""); err == nil {
		t.Fatal("command runs cannot be resumed")
	}
	if c, _ := StartOf(tend.ProviderClaude, "/w", ""); !slices.Equal(c.Argv(), []string{"claude"}) {
		t.Fatalf("%q", c.Argv())
	}
	if SessionProvider(ProviderFake) != tend.ProviderClaude || SessionProvider(ProviderCommand) != "" || SessionProvider("codex") != "codex" {
		t.Fatal("session providers")
	}
}

func TestBypassIsReadFromTheCommandLine(t *testing.T) {
	for _, argv := range [][]string{
		{"claude", "--permission-mode", "bypassPermissions"}, {"claude", "--permission-mode=bypassPermissions"},
		{"claude", "--dangerously-skip-permissions"}, {"codex", "exec", "-s", "danger-full-access"},
		{"codex", "exec", "--sandbox=danger-full-access"}, {"codex", "-c", `sandbox_mode="danger-full-access"`},
		{"codex", "--config=sandbox_mode=danger-full-access"}, {"codex", "--yolo"},
		{"codex", "exec", "-sdanger-full-access"}, {"codex", "exec", "-csandbox_mode=danger-full-access"},
		{"claude", "-p", "--settings", "{}"}, {"claude", "-p", "--allowedTools", "Bash"}, {"codex", "exec", "-p", "wide"},
	} {
		if !BypassArgv(argv) {
			t.Errorf("%v", argv)
		}
	}
	for _, argv := range [][]string{{"claude", "-p", "--permission-mode", "acceptEdits"}, {"codex", "exec", "-s", "workspace-write"},
		{"claude", "-p", "--permission-mode"}} {
		if BypassArgv(argv) {
			t.Errorf("%v", argv)
		}
	}
	got := Profiles([]Profile{{Name: "claude", Provider: "claude", Model: "opus"}, {Name: "mine", Provider: "command"}})
	if len(got) != 4 || got[0].Model != "opus" || got[3].Name != "mine" {
		t.Fatalf("%+v", got)
	}
}
