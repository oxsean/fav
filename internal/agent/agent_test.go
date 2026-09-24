package agent

import (
	"os"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/fav"
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
			[]string{"claude", "-p", "--output-format", "stream-json", "--verbose", "--session-id", sid}},
		{"codex headless, brief on stdin", LaunchSpec{Profile: Profile{Provider: "codex", Model: "gpt-x", Permission: "workspace-write", Args: []string{"-c", "k=v"}}, Dir: "/w", Headless: true},
			[]string{"codex", "exec", "--json", "--skip-git-repo-check", "-C", "/w", "-m", "gpt-x", "--sandbox", "workspace-write", "-c", "k=v", "-"}},
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
	r := &fav.Rec{Provider: fav.ProviderCodex, SessionID: "s1", Cwd: "/w"}
	if c, err := ResumeOf(r, "n"); err != nil || !slices.Equal(c.Argv(), []string{"codex", "resume", "s1"}) || c.Cwd != "/w" {
		t.Fatalf("%q %v", c.Argv(), err)
	}
	if c, err := ForkOf(&fav.Rec{Provider: fav.ProviderClaude, SessionID: "s1"}); err != nil || !slices.Equal(c.Argv(), []string{"claude", "--resume", "s1", "--fork-session"}) {
		t.Fatalf("%q %v", c.Argv(), err)
	}
	if _, err := ResumeOf(&fav.Rec{Provider: fav.ProviderClaude}, ""); err == nil {
		t.Fatal("no session id")
	}
	if _, err := ResumeOf(&fav.Rec{Provider: ProviderCommand, SessionID: "x"}, ""); err == nil {
		t.Fatal("command runs cannot be resumed")
	}
	if c, _ := StartOf(fav.ProviderClaude, "/w", ""); !slices.Equal(c.Argv(), []string{"claude"}) {
		t.Fatalf("%q", c.Argv())
	}
	if SessionProvider(ProviderFake) != fav.ProviderClaude || SessionProvider(ProviderCommand) != "" || SessionProvider("codex") != "codex" {
		t.Fatal("session providers")
	}
}
