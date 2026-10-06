package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/testkit"
)

const settingsBefore = `{
  "env": {
    "A": "1"
  },
  "model": "opus",
  "hooks": {
    "Notification": [
      {
        "matcher": "permission_prompt",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/other-hook --stdin",
            "async": true
          }
        ]
      }
    ]
  },
  "alwaysThinkingEnabled": true
}
`

func TestInstallHookKeepsTheRestAndUninstallRestores(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte(settingsBefore), 0o600)

	stdoutOf(t, func() {
		if err := cmdInstallHook(nil); err != nil {
			t.Fatal(err)
		}
	})
	b, _ := os.ReadFile(path)
	got := string(b)
	for _, want := range []string{`"env"`, `/usr/local/bin/other-hook --stdin`, `"PermissionRequest"`, `"UserPromptSubmit"`, `"Stop"`, " hook-event"} {
		if !strings.Contains(got, want) {
			t.Errorf("after install lacks %s:\n%s", want, got)
		}
	}
	if strings.Index(got, `"env"`) > strings.Index(got, `"model"`) || strings.Index(got, `"hooks"`) > strings.Index(got, `"alwaysThinkingEnabled"`) {
		t.Errorf("key order kept:\n%s", got)
	}
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode kept: %v", st.Mode())
	}
	if baks, _ := filepath.Glob(path + ".bak-*"); len(baks) != 1 {
		t.Errorf("one backup: %v", baks)
	}
	if !hookInstalled() {
		t.Error("doctor sees it installed")
	}

	stdoutOf(t, func() { cmdInstallHook(nil) })
	if again, _ := os.ReadFile(path); string(again) != got {
		t.Error("installing twice changes nothing")
	}
	stdoutOf(t, func() { cmdUninstallHook(nil) })
	if after, _ := os.ReadFile(path); string(after) != settingsBefore {
		t.Errorf("uninstall leaves the file as it was:\n%s", after)
	}
}

func TestIsTendHook(t *testing.T) {
	for cmd, want := range map[string]bool{
		"/Users/me/.local/bin/tend hook-event":       true,
		"'/Users/my name/bin/tend' hook-event":       true,
		"/usr/local/bin/other-hook --stdin":          false,
		"/usr/local/bin/tend-lookalike other":        false,
		"bash /x/hooks/herdr-agent-state.sh session": false,
	} {
		if isTendHook(cmd) != want {
			t.Errorf("isTendHook(%q) != %v", cmd, want)
		}
	}
}

func TestHookEventMarksWaitingUntilTheTranscriptMoves(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	tr := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(tr, []byte("{}\n"), 0o644)
	feed := func(event string) {
		r, w, _ := os.Pipe()
		w.WriteString(`{"session_id":"s1","transcript_path":` + testkit.JSONString(tr) + `,"hook_event_name":"` + event + `"}`)
		w.Close()
		old := os.Stdin
		os.Stdin = r
		cmdHookEvent(nil)
		os.Stdin = old
	}
	feed("Notification")
	if _, ok := capture.HookWaiting("s1", 3); !ok {
		t.Fatal("a permission question marks it waiting")
	}
	if _, ok := capture.HookWaiting("s1", 40); ok {
		t.Fatal("the transcript grew: answered")
	}
	feed("Stop")
	if _, ok := capture.HookWaiting("s1", 3); ok {
		t.Fatal("Stop clears it")
	}
}

func TestHookEventKeepsWhatThePromptIsAbout(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	tr := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(tr, []byte("{}\n"), 0o644)
	feed := func(fields string) {
		r, w, _ := os.Pipe()
		w.WriteString(`{"session_id":"s2","transcript_path":` + testkit.JSONString(tr) + `,` + fields + `}`)
		w.Close()
		old := os.Stdin
		os.Stdin = r
		cmdHookEvent(nil)
		os.Stdin = old
	}
	feed(`"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/build","description":"Clean"},"permission_suggestions":["Bash(rm /tmp/**)"]`)
	if w, ok := capture.HookWaiting("s2", 3); !ok || !w.Permission || w.Tool != "Bash" || w.Summary != "rm -rf /tmp/build" {
		t.Fatalf("PermissionRequest: %+v %v", w, ok)
	}
	feed(`"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"`)
	if w, _ := capture.HookWaiting("s2", 3); !w.Permission || w.Tool != "Bash" {
		t.Fatalf("its notification keeps the tool: %+v", w)
	}
	feed(`"hook_event_name":"Notification","notification_type":"elicitation_dialog","message":"An MCP server asks for input"`)
	if w, ok := capture.HookWaiting("s2", 3); !ok || w.Permission || w.Summary != "" {
		t.Fatalf("a dialog is a wait the hook cannot tell: %+v %v", w, ok)
	}
}

func TestInstallSkillHonoursConfigDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need developer mode")
	}
	root := t.TempDir()
	for _, k := range []string{"TEND_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "HOME"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	src := filepath.Join(root, "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("x"), 0o644)
	stdoutOf(t, func() {
		if err := run([]string{"install-skill", "--from", src}); err != nil {
			t.Fatal(err)
		}
	})
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if dst, err := os.Readlink(filepath.Join(root, k, "skills", "tend")); err != nil || dst != src {
			t.Errorf("%s: %q %v", k, dst, err)
		}
	}
	stdoutOf(t, func() { run([]string{"uninstall-skill"}) })
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if _, err := os.Lstat(filepath.Join(root, k, "skills", "tend")); err == nil {
			t.Errorf("%s: uninstall removes the link", k)
		}
	}
}
