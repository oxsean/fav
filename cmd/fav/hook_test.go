package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/capture"
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
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
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

func TestIsFavHook(t *testing.T) {
	for cmd, want := range map[string]bool{
		"/Users/me/.local/bin/fav hook-event":        true,
		"'/Users/my name/bin/fav' hook-event":        true,
		"/usr/local/bin/other-hook --stdin":          false,
		"/usr/local/bin/fav-lookalike other":         false,
		"bash /x/hooks/herdr-agent-state.sh session": false,
	} {
		if isFavHook(cmd) != want {
			t.Errorf("isFavHook(%q) != %v", cmd, want)
		}
	}
}

func TestHookEventMarksWaitingUntilTheTranscriptMoves(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	tr := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(tr, []byte("{}\n"), 0o644)
	feed := func(event string) {
		r, w, _ := os.Pipe()
		w.WriteString(`{"session_id":"s1","transcript_path":"` + tr + `","hook_event_name":"` + event + `"}`)
		w.Close()
		old := os.Stdin
		os.Stdin = r
		cmdHookEvent(nil)
		os.Stdin = old
	}
	feed("Notification")
	if !capture.HookWaiting("s1", 3) {
		t.Fatal("a permission question marks it waiting")
	}
	if capture.HookWaiting("s1", 40) {
		t.Fatal("the transcript grew: answered")
	}
	feed("Stop")
	if capture.HookWaiting("s1", 3) {
		t.Fatal("Stop clears it")
	}
}
