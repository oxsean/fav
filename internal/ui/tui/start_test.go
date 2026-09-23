package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
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

func TestRunningSessionDialogActsOnItsOwnRecord(t *testing.T) {
	fakeCLIs(t)
	m := sized(t, 140, 44)
	cur := m.current()
	var other *fav.Rec
	for _, r := range m.store.All() {
		if r != cur {
			other = r
			break
		}
	}
	dir := t.TempDir()
	cur.Cwd, other.Cwd = dir, dir
	m.live = map[string]capture.Live{other.SessionID: {Status: "working", Cwd: dir}}
	openOther := func() {
		m.closeOverlay()
		m.Update(press("w"))
		m.Update(press("down"))
		m.Update(press("enter"))
		if m.ov.kind != ovResume || m.ov.rec != other {
			t.Fatalf("↓ Enter opens the running session's dialog: kind=%d", m.ov.kind)
		}
	}
	openOther()
	m.Update(press("f"))
	if other.Favorite() || !cur.Favorite() {
		t.Fatalf("f in that dialog unfavorites its session, not the cursor's: other=%v cur=%v", other.Favorite(), cur.Favorite())
	}
	openOther()
	m.Update(press("e"))
	if m.ov.kind != ovEdit || m.ov.rec != other {
		t.Fatal("e edits the dialog's session")
	}
	openOther()
	m.Update(press("D"))
	if m.ov.kind == ovConfirm || m.notice != i18n.T("trash.running") {
		t.Fatalf("D is refused because the dialog's session runs (the cursor's does not): kind=%d notice=%q", m.ov.kind, m.notice)
	}
}
