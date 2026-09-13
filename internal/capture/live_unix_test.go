//go:build !windows

package capture

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCodexLiveFromLocks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	locks := filepath.Join(home, "thread-writer-locks")
	os.MkdirAll(locks, 0o755)
	os.WriteFile(filepath.Join(locks, "free.lock"), nil, 0o644)
	held := filepath.Join(locks, "held.lock")
	f, _ := os.Create(held)
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Skip("flock 不可用")
	}
	got := CodexLive()
	if _, ok := got["held"]; !ok || len(got) != 1 {
		t.Fatalf("只有持锁的算活：%v", got)
	}

	// plugin app-server threads and sub-agents: lock held, still not live
	day := filepath.Join(home, "sessions", "2026", "09", "21")
	os.MkdirAll(day, 0o755)
	hold := func(id, meta string) {
		os.WriteFile(filepath.Join(day, "rollout-2026-09-21T03-00-00-"+id+".jsonl"), []byte(meta+"\n"), 0o644)
		f, _ := os.Create(filepath.Join(locks, id+".lock"))
		syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		t.Cleanup(func() { f.Close() })
	}
	hold("plugin", `{"type":"session_meta","payload":{"id":"plugin","originator":"Claude Code"}}`)
	hold("sub", `{"type":"session_meta","payload":{"id":"sub","originator":"codex_cli_rs","parent_thread_id":"plugin"}}`)
	hold("human", `{"type":"session_meta","payload":{"id":"human","originator":"codex_cli_rs","parent_thread_id":null}}`)
	got = CodexLive()
	if _, ok := got["human"]; !ok || len(got) != 2 || got["plugin"].Agent != "" || got["sub"].Agent != "" {
		t.Fatalf("插件线程和子代理线程不该算在跑：%v", got)
	}
}
