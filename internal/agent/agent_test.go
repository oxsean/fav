package agent

import (
	"os"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

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

func TestSessionArgsGoWhereAnAliasPutsThem(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	c := tend.DefaultConfig()
	c.SessionArgs = map[string][]string{tend.ProviderClaude: {"--dangerously-skip-permissions"}, tend.ProviderCodex: {"-c", "model=a b"}}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	cl := &tend.Rec{Provider: tend.ProviderClaude, SessionID: "s1", Cwd: "/w"}
	cx := &tend.Rec{Provider: tend.ProviderCodex, SessionID: "s1", Cwd: "/w"}
	resume, _ := ResumeOf(cl, "n")
	fork, _ := ForkOf(cl)
	start, _ := StartOf(tend.ProviderClaude, "/w", "hi")
	cxResume, _ := ResumeOf(cx, "")
	cxFork, _ := ForkOf(cx)
	cxStart, _ := StartOf(tend.ProviderCodex, "", "")
	for got, want := range map[*CommandSpec][]string{
		&resume:   {"claude", "--dangerously-skip-permissions", "--resume", "s1", "--name", "n"},
		&fork:     {"claude", "--dangerously-skip-permissions", "--resume", "s1", "--fork-session"},
		&start:    {"claude", "--dangerously-skip-permissions", "hi"},
		&cxResume: {"codex", "-c", "model=a b", "resume", "s1"},
		&cxFork:   {"codex", "-c", "model=a b", "fork", "s1"},
		&cxStart:  {"codex", "-c", "model=a b"},
	} {
		if !slices.Equal(got.Argv(), want) {
			t.Errorf("%q, want %q", got.Argv(), want)
		}
	}
	if a := AttachOf(cl, "b1"); !slices.Equal(a.Argv(), []string{"claude", "attach", "b1"}) {
		t.Errorf("attaching to a background session takes no flags: %q", a.Argv())
	}
	if l, _ := LaunchOf(LaunchSpec{Profile: Profile{Provider: tend.ProviderClaude}, Dir: "/w"}); slices.Contains(l.Argv(), "--dangerously-skip-permissions") {
		t.Errorf("runs take their profile's args only: %q", l.Argv())
	}
	if words, ok := shell.POSIX.Split(cxStart.ShellLine()); !ok || !slices.Equal(words, cxStart.Argv()) {
		t.Errorf("an argument with a space survives the Herdr pane's line: %q %v", cxStart.ShellLine(), ok)
	}
	if line := shell.PowerShell.Line("", cxResume.Argv()); line != "codex -c 'model=a b' resume s1" {
		t.Errorf("and the line to run on a Windows machine: %s", line)
	}

	c.SessionArgs = map[string][]string{tend.ProviderClaude: {}}
	c.Save()
	if r, _ := ResumeOf(cl, ""); !slices.Equal(r.Argv(), []string{"claude", "--resume", "s1"}) {
		t.Errorf("no args: the command as without the setting: %q", r.Argv())
	}
}

func TestResumeFollowsTheSessionsPermissionMode(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	claudeRec := func(mode string) *tend.Rec {
		return &tend.Rec{Provider: tend.ProviderClaude, SessionID: "s1", Permission: tend.Permission{Mode: mode}}
	}
	codexRec := func(approval, sandbox string) *tend.Rec {
		return &tend.Rec{Provider: tend.ProviderCodex, SessionID: "s1", Permission: tend.Permission{Approval: approval, Sandbox: sandbox}}
	}
	for _, c := range []struct {
		r            *tend.Rec
		resume, fork []string
	}{
		{claudeRec("bypassPermissions"), []string{"claude", "--dangerously-skip-permissions", "--resume", "s1"}, []string{"claude", "--dangerously-skip-permissions", "--resume", "s1", "--fork-session"}},
		{claudeRec("acceptEdits"), []string{"claude", "--permission-mode", "acceptEdits", "--resume", "s1"}, nil},
		{claudeRec("plan"), []string{"claude", "--permission-mode", "plan", "--resume", "s1"}, nil},
		{claudeRec("auto"), []string{"claude", "--permission-mode", "auto", "--resume", "s1"}, nil},
		{claudeRec("dontAsk"), []string{"claude", "--permission-mode", "dontAsk", "--resume", "s1"}, nil},
		{claudeRec("default"), []string{"claude", "--resume", "s1"}, nil},
		{claudeRec("someFutureMode"), []string{"claude", "--resume", "s1"}, nil},
		{claudeRec(""), []string{"claude", "--resume", "s1"}, nil},
		{codexRec("never", "danger-full-access"), []string{"codex", "resume", "-a", "never", "-s", "danger-full-access", "s1"}, []string{"codex", "fork", "-a", "never", "-s", "danger-full-access", "s1"}},
		{codexRec("on-request", "workspace-write"), []string{"codex", "resume", "-a", "on-request", "-s", "workspace-write", "s1"}, nil},
		{codexRec("untrusted", "read-only"), []string{"codex", "resume", "-s", "read-only", "s1"}, nil},
		{codexRec("", "external-sandbox"), []string{"codex", "resume", "s1"}, nil},
		{codexRec("", ""), []string{"codex", "resume", "s1"}, nil},
	} {
		if got, _ := ResumeOf(c.r, ""); !slices.Equal(got.Argv(), c.resume) {
			t.Errorf("resume %+v: %q, want %q", c.r.Permission, got.Argv(), c.resume)
		}
		if got, _ := ForkOf(c.r); c.fork != nil && !slices.Equal(got.Argv(), c.fork) {
			t.Errorf("fork %+v: %q, want %q", c.r.Permission, got.Argv(), c.fork)
		}
	}
	if a := AttachOf(claudeRec("bypassPermissions"), "b1"); !slices.Equal(a.Argv(), []string{"claude", "attach", "b1"}) {
		t.Errorf("attach takes no mode: %q", a.Argv())
	}

	c := tend.DefaultConfig()
	c.SessionArgs = map[string][]string{tend.ProviderClaude: {"--permission-mode", "acceptEdits"}, tend.ProviderCodex: {"--yolo"}}
	c.Save()
	if got, _ := ResumeOf(claudeRec("bypassPermissions"), ""); !slices.Equal(got.Argv(), []string{"claude", "--permission-mode", "acceptEdits", "--resume", "s1"}) {
		t.Errorf("a permission flag in session_args wins over the session's mode: %q", got.Argv())
	}
	if got, _ := ForkOf(codexRec("on-request", "workspace-write")); !slices.Equal(got.Argv(), []string{"codex", "--yolo", "fork", "s1"}) {
		t.Errorf("codex too: %q", got.Argv())
	}
	c.SessionArgs = map[string][]string{tend.ProviderClaude: {"--verbose"}}
	c.Save()
	if got, _ := ResumeOf(claudeRec("plan"), ""); !slices.Equal(got.Argv(), []string{"claude", "--verbose", "--permission-mode", "plan", "--resume", "s1"}) {
		t.Errorf("other session_args leave the mode in: %q", got.Argv())
	}
}

func TestSetsPermissionReadsTheSameFlagsAsBypass(t *testing.T) {
	for _, argv := range [][]string{{"claude", "--dangerously-skip-permissions"}, {"claude", "--permission-mode", "plan"},
		{"claude", "--permission-mode=acceptEdits"}, {"codex", "--yolo"}, {"codex", "-a", "never"}, {"codex", "--ask-for-approval=on-request"},
		{"codex", "-s", "read-only"}, {"codex", "-sread-only"}, {"codex", "--full-auto"}, {"codex", "-c", "approval_policy=never"},
		{"codex", "--config=sandbox_mode=workspace-write"}, {"codex", "--dangerously-bypass-approvals-and-sandbox"}} {
		if !SetsPermission(argv) {
			t.Errorf("%q", argv)
		}
	}
	for _, argv := range [][]string{{"claude", "--verbose"}, {"claude", "--settings", "{}"}, {"codex", "-c", "model=x"}, {"codex", "-m", "gpt"}} {
		if SetsPermission(argv) {
			t.Errorf("%q", argv)
		}
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
