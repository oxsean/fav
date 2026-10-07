package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/tend"
)

func turnLines(from, n int) string {
	var b strings.Builder
	for i := from; i < from+n; i++ {
		fmt.Fprintf(&b, `{"type":"user","timestamp":"2026-10-01T10:%02d:00Z","cwd":"/tmp/follow","message":{"content":"第 %d 句：把分页的游标再查一遍"}}`+"\n", i, i)
	}
	return b.String()
}

func writeTurns(t *testing.T, path string, from, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(turnLines(from, n)); err != nil {
		t.Fatal(err)
	}
}

// step runs one follow tick to its end, as the program would without the second's wait.
func step(t *testing.T, m *Model) {
	t.Helper()
	cmd := m.followLive()
	if cmd == nil {
		t.Fatal("在跑的会话应排下一次 stat")
	}
	msg := cmd()
	if _, ok := msg.(followTickMsg); ok {
		t.Fatal("这一拍什么都没 stat")
	}
	m.Update(msg)
}

func TestFollowRunningSessionsEverySecond(t *testing.T) {
	claude := filepath.Join(t.TempDir(), "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("TEND_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	proj := filepath.Join(claude, "projects", "-tmp-follow")
	running, idle := filepath.Join(proj, "run1.jsonl"), filepath.Join(proj, "idle1.jsonl")
	writeTurns(t, running, 0, 3)
	writeTurns(t, idle, 0, 3)
	idx, err := index.OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Refresh()
	st, _ := tend.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	m := New(st, idx, tend.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if m.bySession("run1") == nil || m.bySession("idle1") == nil {
		t.Fatal("两个会话应列出来")
	}

	if m.Update(liveMsg{local: map[string]capture.Live{}}); m.follow.on {
		t.Fatal("没有在跑的会话不该开始 stat")
	}
	m.Update(liveMsg{local: map[string]capture.Live{"run1": {Agent: tend.ProviderClaude, Status: "working"}}})
	if !m.follow.on {
		t.Fatal("有会话在跑应开始每秒 stat")
	}
	run := m.bySession("run1")
	m.setView(viewSessions)
	for i, row := range m.rows {
		if row.rec == run {
			m.cursor = i
		}
	}
	m.probes = map[*tend.Rec]*probe{run: {done: true}}
	writeTurns(t, running, 3, 2)
	writeTurns(t, idle, 3, 2)
	step(t, m)
	if !m.probes[run].polling {
		t.Fatal("光标上的在跑会话变了，右栏跟着重读")
	}
	if r := m.bySession("run1"); r.Turns != 5 {
		t.Fatalf("在跑的会话变长后一拍内跟上：%d 轮", r.Turns)
	}
	if r := m.bySession("idle1"); r.Turns != 3 {
		t.Fatalf("不在跑的会话等常规刷新：%d 轮", r.Turns)
	}

	fresh := filepath.Join(proj, "new1.jsonl")
	m.Update(liveMsg{local: map[string]capture.Live{
		"run1": {Agent: tend.ProviderClaude, Status: "working"},
		"new1": {Agent: tend.ProviderClaude, Status: "working"},
	}})
	step(t, m)
	if m.follow.tried["new1"].IsZero() {
		t.Fatal("还没落盘的新会话记下找过的时间")
	}
	writeTurns(t, fresh, 0, 3)
	m.follow.tried["new1"] = time.Now().Add(-followRetry)
	step(t, m)
	if r := m.bySession("new1"); r == nil || r.Turns != 3 || r.TranscriptPath != fresh {
		t.Fatalf("新开的会话几秒内出现：%+v", r)
	}

	m.Update(m.refreshIndex()())
	if r := m.bySession("idle1"); r.Turns != 5 {
		t.Fatalf("常规刷新照样读到不在跑的会话：%d 轮", r.Turns)
	}

	m.Update(liveMsg{local: map[string]capture.Live{}})
	if cmd := m.followLive(); cmd != nil || m.follow.on {
		t.Fatal("没有会话在跑就停")
	}
}

func TestFollowDropsAResultOverANewerSnapshot(t *testing.T) {
	claude := filepath.Join(t.TempDir(), "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("TEND_HOME", t.TempDir())
	running := filepath.Join(claude, "projects", "-tmp-follow", "run1.jsonl")
	writeTurns(t, running, 0, 3)
	idx, _ := index.OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	st, _ := tend.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	m := New(st, idx, tend.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m.Update(liveMsg{local: map[string]capture.Live{"run1": {Agent: tend.ProviderClaude}}})
	writeTurns(t, running, 3, 1)
	stale := m.followLive()()
	m.reindex(nil)
	m.Update(stale)
	if m.heldIdx != nil || m.bySession("run1").Turns != 3 {
		t.Fatal("快照已经换代，晚到的结果丢掉，下一拍重来")
	}
	step(t, m)
	if m.bySession("run1").Turns != 4 {
		t.Fatal("下一拍从新快照接着读")
	}
}
