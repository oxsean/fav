package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
)

// fakeHerdr answers `agent read` with a permission question and logs every other call.
func fakeHerdr(t *testing.T) (log string) {
	bin := t.TempDir()
	log = filepath.Join(bin, "calls")
	script := "#!/bin/sh\nif [ \"$2\" = read ]; then printf 'Edit a.go?\\n❯ 1. Yes\\n  2. No\\n'; exit 0; fi\necho \"$@\" >> " + log + "\n"
	os.WriteFile(filepath.Join(bin, "herdr"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestPeekAndReply(t *testing.T) {
	log := fakeHerdr(t)
	m := sized(t, 140, 40)
	r := m.store.All()[0]
	m.live = map[string]capture.Live{r.SessionID: {PaneID: "p1", TabID: "t1", Status: "blocked"}}
	m.setView(viewLive)
	for i, row := range m.rows {
		if row.rec == r {
			m.cursor = i
		}
	}
	run := func(cmd tea.Cmd) {
		for msg := range drain(cmd) {
			if _, tick := msg.(peekTickMsg); !tick {
				m.Update(msg)
			}
		}
	}
	key := func(s string) { _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}); run(cmd) }
	key("v")
	if m.ov.kind != ovPeek {
		t.Fatalf("v on a Herdr agent peeks: kind=%d", m.ov.kind)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Edit a.go?") || !strings.Contains(v, "1. Yes") {
		t.Fatalf("the pane's terminal is shown:\n%s", v)
	}
	for i, l := range viewLines(m) {
		if w := ansi.StringWidth(l); w > 140 {
			t.Errorf("line %d is %d wide", i, w)
		}
	}
	key("1")
	if b, _ := os.ReadFile(log); len(b) != 0 || !strings.Contains(ansi.Strip(m.View()), "再按一次 1") {
		t.Fatalf("the first 1 only arms: calls %q", b)
	}
	key("1")
	key(":")
	key("继续")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	run(cmd)
	b, _ := os.ReadFile(log)
	if got := string(b); got != "agent send-keys p1 1\nagent prompt p1 继续\n" {
		t.Fatalf("second 1 answers, the typed line is the next prompt: %q", got)
	}
}

// drain runs cmd and yields every message, opening batches.
func drain(cmd tea.Cmd) func(func(tea.Msg) bool) {
	return func(yield func(tea.Msg) bool) {
		var walk func(tea.Cmd) bool
		walk = func(c tea.Cmd) bool {
			if c == nil {
				return true
			}
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, c := range b {
					if !walk(c) {
						return false
					}
				}
				return true
			}
			return yield(msg)
		}
		walk(cmd)
	}
}
