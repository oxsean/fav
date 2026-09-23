package capture

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestClaudeLiveFromSessionFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	os.MkdirAll(filepath.Join(dir, "sessions"), 0o755)
	me := strconv.Itoa(os.Getpid())
	os.WriteFile(filepath.Join(dir, "sessions", "1.json"), []byte(`{"pid":`+me+`,"sessionId":"s-busy","cwd":"/w","kind":"interactive","name":"活着","status":"busy","statusUpdatedAt":1789915727157}`), 0o644)
	os.WriteFile(filepath.Join(dir, "sessions", "2.json"), []byte(`{"pid":999999,"sessionId":"s-dead","cwd":"/w","status":"idle"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "sessions", "3.json"), []byte(`{"pid":`+me+`,"sessionId":"s-bg","kind":"bg","jobId":"s-bg0000","status":"idle"}`), 0o644)
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

func TestCodexLiveFromLocks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	locks := filepath.Join(home, "thread-writer-locks")
	os.MkdirAll(locks, 0o755)
	os.WriteFile(filepath.Join(locks, "free.lock"), nil, 0o644)
	hold := func(id string) {
		unlock, err := filelock.Lock(filepath.Join(locks, id+".lock"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(unlock)
	}
	hold("held")
	got := CodexLive()
	if _, ok := got["held"]; !ok || len(got) != 1 {
		t.Fatalf("只有持锁的算活：%v", got)
	}

	// plugin app-server threads and sub-agents: lock held, still not live
	day := filepath.Join(home, "sessions", "2026", "09", "21")
	os.MkdirAll(day, 0o755)
	for id, meta := range map[string]string{
		"plugin": `{"type":"session_meta","payload":{"id":"plugin","originator":"Claude Code"}}`,
		"sub":    `{"type":"session_meta","payload":{"id":"sub","originator":"codex_cli_rs","parent_thread_id":"plugin"}}`,
		"human":  `{"type":"session_meta","payload":{"id":"human","originator":"codex_cli_rs","parent_thread_id":null}}`,
	} {
		os.WriteFile(filepath.Join(day, "rollout-2026-09-21T03-00-00-"+id+".jsonl"), []byte(meta+"\n"), 0o644)
		hold(id)
	}
	got = CodexLive()
	if _, ok := got["human"]; !ok || len(got) != 2 || got["plugin"].Agent != "" || got["sub"].Agent != "" {
		t.Fatalf("插件线程和子代理线程不该算在跑：%v", got)
	}
}

func TestHerdrLive(t *testing.T) {
	pane := func(id, agent, sid, status string, seq int) herdr.Pane {
		return herdr.Pane{PaneID: id, TabID: "t" + id, Agent: agent, AgentSession: &herdr.Agent{Value: sid}, AgentStatus: status, StateSeq: seq, Title: "title " + id}
	}
	agents := []herdr.Pane{
		pane("1", fav.ProviderClaude, "c-live", "working", 3),
		pane("2", fav.ProviderClaude, "c-stale", "idle", 1),
		pane("3", fav.ProviderCodex, "x-1", "idle", 7),
		{PaneID: "4", Agent: fav.ProviderCodex},
	}
	local := map[string]Live{"c-live": {Agent: fav.ProviderClaude}}
	then, now := time.Unix(100, 0), time.Unix(200, 0)
	prev := map[string]Live{"c-live": {Status: "working", Seq: 3, Since: then}, "x-1": {Status: "idle", Seq: 6, Since: then}}

	got := herdrLive(agents, local, prev, true, now)
	if _, ok := got["c-stale"]; ok || len(got) != 2 {
		t.Fatalf("a Claude id without a sessions file is stale; a pane without a session is skipped: %v", got)
	}
	if l := got["c-live"]; !l.Since.Equal(then) || l.PaneID != "1" || l.TabID != "t1" || l.Title != "title 1" || l.Agent != fav.ProviderClaude {
		t.Errorf("unchanged status and seq keep Since; pane, tab, agent and title filled: %+v", l)
	}
	if l := got["x-1"]; !l.Since.Equal(now) {
		t.Errorf("a new seq restarts Since: %+v", l)
	}
	if got := herdrLive(agents, local, nil, false, now); len(got) != 3 {
		t.Errorf("without Claude's sessions dir every id counts: %v", got)
	}
}

func TestMergeLive(t *testing.T) {
	since := time.Unix(100, 0)
	got := MergeLive(
		map[string]Live{"a": {Agent: fav.ProviderClaude, Title: "local", Cwd: "/w", BackgroundID: "bg", Status: "idle", Since: time.Unix(1, 0)}},
		map[string]Live{"a": {PaneID: "p", TabID: "t", Title: "herdr", Status: "working", Seq: 4, Since: since}, "b": {Agent: fav.ProviderCodex}},
	)
	want := Live{PaneID: "p", TabID: "t", Status: "working", Seq: 4, Since: since, Agent: fav.ProviderClaude, Title: "herdr", Cwd: "/w", BackgroundID: "bg"}
	if got["a"] != want || got["b"].Agent != fav.ProviderCodex || len(got) != 2 {
		t.Fatalf("later non-empty fields win, Status carries Seq and Since:\n got %+v\nwant %+v", got["a"], want)
	}
}
