package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestReadSessionMetaOnlyReadsFirstLine(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	p := writeRollout(t, sessions, time.Now(), "aaaa-1111", "/work/notes-api", true)

	meta, err := readSessionMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID != "aaaa-1111" || meta.Cwd != "/work/notes-api" {
		t.Fatalf("首行解析错了：%+v", meta)
	}
}
