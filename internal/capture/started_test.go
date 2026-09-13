package capture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionStartSkipsUntimestampedHead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(p, []byte(`{"type":"mode","sessionId":"x"}
{"type":"last-prompt"}
{"type":"user","timestamp":"2026-09-12T15:15:02.142Z"}
{"type":"assistant","timestamp":"2026-09-12T15:15:04.855Z"}
`), 0o600)
	got, ok := SessionStart(p)
	if !ok || got.UTC().Format("15:04:05") != "15:15:02" {
		t.Fatalf("SessionStart = %v %v，应取第一条带时间戳的行", got, ok)
	}
	if _, ok := SessionStart(filepath.Join(t.TempDir(), "missing")); ok {
		t.Fatal("文件不存在应返回 false")
	}
}
