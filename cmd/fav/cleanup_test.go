package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func stdoutOf(t *testing.T, f func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestBigStaleTranscripts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	old := time.Now().Add(-40 * 24 * time.Hour)
	line := `{"type":"user","timestamp":"2026-08-01T01:00:00Z","cwd":"/w","message":{"content":"把分页修好，顺便补测试"}}` + "\n"
	for _, sid := range []string{"aaaa1111-big", "bbbb2222-fav", "cccc3333-new"} {
		p := filepath.Join(root, "claude", "projects", "-w", sid+".jsonl")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(strings.Repeat(line, 5)), 0o644)
		if sid != "cccc3333-new" {
			os.Chtimes(p, old, old)
		}
	}
	idx, _ := index.OpenAt(filepath.Join(root, "sessions.jsonl"))
	idx, _ = idx.Refresh()
	s, _ := fav.OpenAt(filepath.Join(root, "records.jsonl"))
	now := time.Now()
	s.Put(&fav.Rec{ID: "r1", Provider: fav.ProviderClaude, SessionID: "bbbb2222-fav", Title: "t", FavoritedAt: &now})

	defer func(b int64) { bigTranscript = b }(bigTranscript)
	bigTranscript = 100
	out := stdoutOf(t, func() { printBigStale(s, idx, map[string]capture.Live{}) })
	if !strings.Contains(out, "fav rm aaaa1111") || strings.Contains(out, "bbbb2222") || strings.Contains(out, "cccc3333") {
		t.Fatalf("only the big, old, unfavorited one:\n%s", out)
	}
	if out := stdoutOf(t, func() { printBigStale(s, idx, map[string]capture.Live{"aaaa1111-big": {}}) }); out != "" {
		t.Fatalf("a running session is never a cleanup candidate:\n%s", out)
	}
	if out := stdoutOf(t, func() {
		printIdleAgents(s, idx, map[string]capture.Live{"aaaa1111-big": {TabID: "t9"}, "cccc3333-new": {TabID: "t1"}})
	}); !strings.Contains(out, "Herdr tab t9") || strings.Contains(out, "t1") {
		t.Fatalf("idle = its transcript quiet for capture.IdleAfter:\n%s", out)
	}
}
