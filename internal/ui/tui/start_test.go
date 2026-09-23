package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oxsean/fav/internal/capture"
)

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
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Z")})
	if m.ov.kind != ovConfirm || m.ov.focus != 1 {
		t.Fatalf("Z asks first, focus on Cancel: kind=%d focus=%d", m.ov.kind, m.ov.focus)
	}
	body := strings.Join(m.ov.lines, "\n")
	if !strings.Contains(body, old.Title[:6]) || strings.Contains(body, fresh.Title[:6]) || strings.Contains(body, unseen.Title[:6]) {
		t.Fatalf("only tabs quiet for hours with nothing unseen:\n%s", body)
	}
}
