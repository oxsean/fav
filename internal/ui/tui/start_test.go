package tui

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
)

func TestStartFromProjectShowsWhatRuns(t *testing.T) {
	fakeCLIs(t)
	m := sized(t, 140, 44)
	dir := t.TempDir()
	var running string
	for _, r := range m.store.All() {
		if r.Project == "notes-api" {
			r.Cwd = dir
			running = r.SessionID
		}
	}
	m.live = map[string]capture.Live{running: {Status: "working", Cwd: dir}}
	m.setView(viewProjects)
	for i, row := range m.rows {
		if row.group == "notes-api" {
			m.cursor = i
		}
	}
	key := func(s string) { m.Update(press(s)) }
	key("w")
	if m.ov.kind != ovStart || m.ov.rec.Cwd != dir || len(m.ov.running) != 1 || m.ov.running[0].SessionID != running {
		t.Fatalf("w on a project header: a new-session dialog in its directory listing what runs there: %+v", m.ov)
	}
	v := ansi.Strip(m.screen())
	for _, want := range []string{"开新会话 · notes-api", "这里已经在跑（1）", "1 Claude Code", "2 Codex CLI"} {
		if !strings.Contains(v, want) {
			t.Errorf("dialog lacks %q:\n%s", want, v)
		}
	}
	m.Update(press("down"))
	m.Update(press("enter"))
	if m.ov.kind != ovResume || m.ov.rec.SessionID != running {
		t.Fatalf("↓ Enter goes to the running session's dialog: kind=%d", m.ov.kind)
	}
	m.closeOverlay()
	key("w")
	m.screen()
	key("2")
	if m.quitting {
		t.Fatal("the first 2 only selects Codex")
	}
	key("2")
	s := m.result.Start
	if !m.quitting || s == nil || !slices.Equal(s.Argv(), []string{"codex"}) || s.Cwd != dir {
		t.Fatalf("2 starts a bare Codex session in the directory: %+v", s)
	}
}

func TestCloseIdleTabs(t *testing.T) {
	m := sized(t, 140, 40)
	recs := m.store.All()
	old, fresh, unseen := recs[0], recs[1], recs[2]
	for _, r := range recs[:3] {
		r.LastAt = m.now.Add(-5 * time.Hour)
	}
	fresh.LastAt = m.now
	m.live = map[string]capture.Live{
		old.SessionID:    {TabID: "t-old", Status: "idle"},
		fresh.SessionID:  {TabID: "t-fresh", Status: "idle"},
		unseen.SessionID: {TabID: "t-unseen", Status: "idle"},
	}
	m.pulse = pulseMsg{unseen.SessionID: {Size: 10, Finished: true}}
	m.setView(viewLive)
	m.Update(press("Z"))
	if m.ov.kind != ovConfirm || m.ov.focus != 1 {
		t.Fatalf("Z asks first, focus on Cancel: kind=%d focus=%d", m.ov.kind, m.ov.focus)
	}
	body := strings.Join(m.ov.lines, "\n")
	if !strings.Contains(body, old.Title[:6]) || strings.Contains(body, fresh.Title[:6]) || strings.Contains(body, unseen.Title[:6]) {
		t.Fatalf("only tabs quiet for hours with nothing unseen:\n%s", body)
	}
}

func TestResumeRefusedWhileRunningElsewhere(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	r.Cwd = t.TempDir()
	r.TranscriptPath = r.Cwd + "/s.jsonl"
	os.WriteFile(r.TranscriptPath, []byte("{}\n"), 0o644)
	m.live = map[string]capture.Live{r.SessionID: {Status: "idle"}} // another terminal, not Herdr
	m.askResume()
	v := ansi.Strip(m.screen())
	if !strings.Contains(v, "在别的终端里运行") || !strings.Contains(v, "在跑") || !strings.Contains(v, "H 暂缓") || strings.Contains(v, "X 关掉 tab") {
		t.Fatalf("the check says why, and the running row has the actions that apply:\n%s", v)
	}
	m.Update(press("enter"))
	if m.quitting || !strings.Contains(m.notice, "别的终端") {
		t.Fatalf("resuming a second copy is refused: quitting=%v notice=%q", m.quitting, m.notice)
	}
}

func TestPeekForgetsTheArmedDigitWhenTheScreenChanges(t *testing.T) {
	m := sized(t, 140, 40)
	m.ov = overlay{kind: ovPeek, rec: m.current(), title: "p1", lines: []string{"1. Yes"}, armed: "1", armedAt: time.Now()}
	m.applyPeek(peekMsg{pane: "p1", text: "1. Yes\n"})
	if m.ov.armed != "1" {
		t.Fatal("same screen: still armed")
	}
	m.applyPeek(peekMsg{pane: "p1", text: "Another question?\n1. Yes\n"})
	if m.ov.armed != "" {
		t.Fatal("a changed screen forgets the armed digit")
	}
	m.ov.armed, m.ov.armedAt = "2", time.Now().Add(-peekArmFor-time.Second)
	m.applyPeek(peekMsg{pane: "p1", text: "Another question?\n1. Yes\n"})
	if m.ov.armed != "" {
		t.Fatal("an old armed digit expires")
	}
}
