package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fav"
)

func writeRollout(t *testing.T, root string, day time.Time, sessionID, cwd string, big bool) string {
	t.Helper()
	dir := filepath.Join(root, day.Format("2006"), day.Format("01"), day.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-"+day.Format("2006-01-02T15-04-05")+"-"+sessionID+".jsonl")

	meta, _ := json.Marshal(map[string]any{
		"timestamp": day.Format(time.RFC3339), "ordinal": 0, "type": "session_meta",
		"payload": map[string]any{"session_id": sessionID, "cwd": cwd, "originator": "codex_cli_rs"},
	})
	body := append(meta, '\n')
	if big {
		body = append(body, make([]byte, 2<<20)...)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetectCodexSingleMatch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	sessions := filepath.Join(root, "sessions")
	now := time.Now()

	want := writeRollout(t, sessions, now, "aaaa-1111", "/work/notes-api", true)
	writeRollout(t, sessions, now, "bbbb-2222", "/work/other", false)

	cands := activeCodexSessions("/work/notes-api", now)
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(cands))
	}
	if cands[0].SessionID != "aaaa-1111" || cands[0].Path != want {
		t.Fatalf("匹配错了：%+v", cands[0])
	}
}

func TestDetectCodexAmbiguous(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	sessions := filepath.Join(root, "sessions")
	now := time.Now()

	writeRollout(t, sessions, now, "aaaa-1111", "/work/notes-api", false)
	writeRollout(t, sessions, now.Add(-time.Minute), "cccc-3333", "/work/notes-api", false)

	_, err := detectCodex("/work/notes-api")
	amb, ok := err.(*ErrAmbiguous)
	if !ok {
		t.Fatalf("应返回 ErrAmbiguous，got %v", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("应列出 2 个候选，got %d", len(amb.Candidates))
	}
	for _, id := range []string{"aaaa-1111", "cccc-3333"} {
		if !strings.Contains(amb.Error(), id) {
			t.Errorf("错误信息里应列出候选 %s，got:\n%s", id, amb.Error())
		}
	}
}

func TestStaleSessionIgnored(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	sessions := filepath.Join(root, "sessions")
	now := time.Now()

	p := writeRollout(t, sessions, now, "aaaa-1111", "/work/notes-api", false)
	old := now.Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if got := activeCodexSessions("/work/notes-api", now); len(got) != 0 {
		t.Fatalf("超过活跃窗口的会话不应被匹配，got %+v", got)
	}
}

func TestDetectCodexNoMatch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	got, err := detectCodex("/work/nothing-here")
	if err != nil || got != nil {
		t.Fatalf("没有会话时应返回 (nil, nil)，got %v, %v", got, err)
	}
}

func TestTranscriptPathFindsOldAndArchivedRollouts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	old := writeRollout(t, filepath.Join(root, "sessions"), time.Now().AddDate(-2, 0, 0), "old-1111", "/w", false)
	archived := filepath.Join(root, "archived_sessions", "rollout-2025-01-02T03-04-05-arch-2222.jsonl")
	os.MkdirAll(filepath.Dir(archived), 0o755)
	os.WriteFile(archived, []byte("{}\n"), 0o600)
	if got := TranscriptPath(fav.ProviderCodex, "old-1111"); got != old {
		t.Errorf("a rollout from two years ago: %q", got)
	}
	if got := TranscriptPath(fav.ProviderCodex, "arch-2222"); got != archived {
		t.Errorf("an archived rollout: %q", got)
	}
}
