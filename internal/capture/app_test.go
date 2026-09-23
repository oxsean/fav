package capture

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oxsean/fav/internal/fav"
)

func appRec(t *testing.T, provider, id string) *fav.Rec {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".claude", "projects", "-p")
	if provider == fav.ProviderCodex {
		dir = filepath.Join(home, ".codex", "sessions", "2026", "09", "23")
	}
	os.MkdirAll(dir, 0o755)
	tr := filepath.Join(dir, id+".jsonl")
	os.WriteFile(tr, []byte("{}\n"), 0o644)
	return &fav.Rec{Provider: provider, SessionID: id, Cwd: home, TranscriptPath: tr}
}

func TestAppURLs(t *testing.T) {
	id := "c5126b86-64bb-46a8-9a69-fc421c8f4f9a"
	if u := AppURL(appRec(t, fav.ProviderClaude, id)); u != "claude://resume?session="+id {
		t.Errorf("claude: %q", u)
	}
	if u := AppURL(appRec(t, fav.ProviderCodex, id)); u != "codex://threads/"+id {
		t.Errorf("codex: %q", u)
	}
	if u := AppURL(appRec(t, fav.ProviderClaude, "not-a-uuid")); u != "" {
		t.Errorf("the apps take only UUIDs: %q", u)
	}
}

func TestAppNeedsWhatTheAppReads(t *testing.T) {
	id := "c5126b86-64bb-46a8-9a69-fc421c8f4f9a"
	r := appRec(t, fav.ProviderClaude, id)
	r.Cwd = filepath.Join(r.Cwd, "gone")
	if AppURL(r) != "" || appBlock(r) != "resume.app_no_cwd" {
		t.Error("a vanished working directory: the app cannot start the session")
	}

	r = appRec(t, fav.ProviderClaude, id)
	other := filepath.Join(t.TempDir(), id+".jsonl") // CLAUDE_CONFIG_DIR elsewhere, a remote or WSL copy
	os.WriteFile(other, []byte("{}\n"), 0o644)
	r.TranscriptPath = other
	if AppURL(r) != "" || appBlock(r) != "resume.app_elsewhere" {
		t.Error("a transcript outside ~/.claude/projects: the app does not see it")
	}

	r = appRec(t, fav.ProviderCodex, id)
	os.Remove(r.TranscriptPath)
	if AppURL(r) != "" {
		t.Error("a deleted transcript (only the pinned link left): the app cannot read it")
	}

	r = appRec(t, fav.ProviderCodex, id)
	archived := filepath.Join(r.Cwd, ".codex", "archived_sessions", id+".jsonl") // r.Cwd is HOME
	os.MkdirAll(filepath.Dir(archived), 0o755)
	os.WriteFile(archived, []byte("{}\n"), 0o644)
	r.TranscriptPath = archived
	if AppURL(r) != "" {
		t.Error("an archived Codex thread is not under ~/.codex/sessions")
	}
}

func TestStartedInApp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfg, _ := os.UserConfigDir()
	dir := filepath.Join(cfg, "Claude", "claude-code-sessions", "acct", "org")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "local_1.json"), []byte(`{"sessionId":"local_1","cliSessionId":"aaaa"}`), 0o644)
	if !StartedInApp(&fav.Rec{Provider: fav.ProviderClaude, SessionID: "aaaa"}) || StartedInApp(&fav.Rec{Provider: fav.ProviderClaude, SessionID: "bbbb"}) {
		t.Error("Claude: the desktop app's session list decides")
	}

	rollout := filepath.Join(home, "rollout.jsonl")
	os.WriteFile(rollout, []byte(`{"type":"session_meta","payload":{"id":"x","originator":"Codex Desktop"}}`+"\n"), 0o644)
	cli := filepath.Join(home, "cli.jsonl")
	os.WriteFile(cli, []byte(`{"type":"session_meta","payload":{"id":"y","originator":"codex-tui"}}`+"\n"), 0o644)
	if !StartedInApp(&fav.Rec{Provider: fav.ProviderCodex, TranscriptPath: rollout}) || StartedInApp(&fav.Rec{Provider: fav.ProviderCodex, TranscriptPath: cli}) {
		t.Error("Codex: the rollout's originator decides")
	}
}

func TestAppHandlerMustBeTheApp(t *testing.T) {
	for _, c := range []struct {
		provider, handler string
		want              bool
	}{
		{fav.ProviderClaude, "/Applications/Claude.app com.anthropic.claudefordesktop", true},
		{fav.ProviderCodex, "/Applications/ChatGPT.app com.openai.codex", true},
		{fav.ProviderCodex, "ChatGPT C:\\Program Files\\WindowsApps\\OpenAI.ChatGPT\\app\\ChatGPT.exe", true},
		{fav.ProviderClaude, "claude-desktop.desktop", true},
		{fav.ProviderClaude, "", false},
		{fav.ProviderCodex, "/Applications/Other.app com.example.other", false},
		{fav.ProviderClaude, "OpenWith.exe", false},
	} {
		if got := handlerIs(c.provider, c.handler); got != c.want {
			t.Errorf("handlerIs(%s, %q) = %v", c.provider, c.handler, got)
		}
	}
}

func TestSchemeHandlerOnThisMachine(t *testing.T) {
	if schemeHandler("fav-no-such-scheme") != "" {
		t.Fatal("an unregistered scheme has no handler")
	}
}
