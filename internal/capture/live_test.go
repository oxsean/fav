package capture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeLiveFromSessionFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	os.MkdirAll(filepath.Join(dir, "sessions"), 0o755)
	me := os.Getpid()
	os.WriteFile(filepath.Join(dir, "sessions", "1.json"), []byte(`{"pid":`+itoa(me)+`,"sessionId":"s-busy","cwd":"/w","kind":"interactive","name":"活着","status":"busy","statusUpdatedAt":1789915727157}`), 0o644)
	os.WriteFile(filepath.Join(dir, "sessions", "2.json"), []byte(`{"pid":999999,"sessionId":"s-dead","cwd":"/w","status":"idle"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "sessions", "3.json"), []byte(`{"pid":`+itoa(me)+`,"sessionId":"s-bg","kind":"bg","jobId":"s-bg0000","status":"idle"}`), 0o644)
	got := ClaudeLive()
	l, ok := got["s-busy"]
	if !ok || l.Status != "working" || l.Title != "活着" || l.Cwd != "/w" || l.Since.IsZero() {
		t.Fatalf("活着的应被认出：%+v", l)
	}
	if _, ok := got["s-dead"]; ok {
		t.Fatal("进程不在的不该算")
	}
	if got["s-bg"].BackgroundID != "s-bg0000" {
		t.Fatalf("后台会话应带 attach 用的短 id：%+v", got["s-bg"])
	}
}

func itoa(n int) string {
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
