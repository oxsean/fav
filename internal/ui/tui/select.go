package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/oxsean/fav/internal/i18n"
)

// Mouse drag selection: press records the start, drag inverts by line, release copies the selected text (borders and edge blanks stripped).
type selection struct {
	x1, y1, x2, y2 int
	dragging       bool // only counts as a drag after moving
	pressed        bool
	// the selection is clamped to the region pressed in (overlay / right pane / list)
	left, right, top, bottom int
}

func (m *Model) region(x, y int) (left, right, top, bottom int) {
	if m.ov.active() {
		return m.ovX + 1, m.ovX + m.ovW - 1, m.ovY + 1, m.ovY + m.ovH - 1
	}
	if m.twoColumn() && x >= m.listWidth() {
		return m.listWidth() + 1, m.w, 0, m.h
	}
	if m.twoColumn() {
		return 0, m.listWidth(), 0, m.h
	}
	return 0, m.w, 0, m.h
}

func (m *Model) mouseSelect(msg tea.MouseMsg) {
	mo := msg.Mouse()
	switch msg.(type) {
	case tea.MouseClickMsg:
		l, r, t, b := m.region(mo.X, mo.Y)
		m.sel = selection{x1: mo.X, y1: mo.Y, x2: mo.X, y2: mo.Y, pressed: true, left: l, right: r, top: t, bottom: b}
	case tea.MouseMotionMsg:
		if m.sel.pressed && (mo.X != m.sel.x1 || mo.Y != m.sel.y1) {
			m.sel.x2, m.sel.y2, m.sel.dragging = mo.X, mo.Y, true
		}
	case tea.MouseReleaseMsg:
		if m.sel.dragging {
			m.sel.x2, m.sel.y2 = mo.X, mo.Y
			m.copySelection()
		}
		m.sel = selection{}
	}
}

func (s selection) ordered() (int, int, int, int) {
	if s.y2 < s.y1 || (s.y2 == s.y1 && s.x2 < s.x1) {
		return s.x2, s.y2, s.x1, s.y1
	}
	return s.x1, s.y1, s.x2, s.y2
}

// span is the selected column range [from, to) of line y; (0, 0) when none.
func (s selection) span(y, _ int) (int, int) {
	x1, y1, x2, y2 := s.ordered()
	y1, y2 = max(y1, s.top), min(y2, s.bottom-1)
	clamp := func(x int) int { return min(max(x, s.left), s.right) }
	var from, to int
	switch {
	case y < y1 || y > y2:
		return 0, 0
	case y1 == y2:
		from, to = x1, x2+1
	case y == y1:
		from, to = x1, s.right
	case y == y2:
		from, to = s.left, x2+1
	default:
		from, to = s.left, s.right
	}
	from, to = clamp(from), clamp(to)
	if from >= to {
		return 0, 0
	}
	return from, to
}

func cut(plain string, from, to int) string {
	var b strings.Builder
	col := 0
	for _, r := range plain {
		w := runewidth.RuneWidth(r)
		if col >= to {
			break
		}
		if col >= from {
			b.WriteRune(r)
		}
		col += w
	}
	return b.String()
}

func (m *Model) copySelection() {
	lines := strings.Split(m.lastFrame, "\n")
	var out []string
	for y, line := range lines {
		from, to := m.sel.span(y, m.w)
		if from == to {
			continue
		}
		s := cut(ansi.Strip(line), from, to)
		s = strings.Trim(s, " │╭╮╰╯─┃")
		out = append(out, s)
	}
	text := strings.TrimSpace(strings.Join(out, "\n"))
	if text == "" {
		return
	}
	if err := copyText(text); err != nil {
		m.flash(i18n.T("flash.clipboard_unavailable") + err.Error())
		return
	}
	m.flash(i18n.F("select.copied", len([]rune(text)), strings.ReplaceAll(text, "\n", " ⏎ ")))
}

func (m *Model) paintSelection(frame string) string {
	if !m.sel.dragging {
		return frame
	}
	lines := strings.Split(frame, "\n")
	for y, line := range lines {
		from, to := m.sel.span(y, m.w)
		if from == to {
			continue
		}
		plain := fit(ansi.Strip(line), m.w)
		lines[y] = cut(plain, 0, from) + selTitle.Render(cut(plain, from, to)) + cut(plain, to, m.w)
	}
	return strings.Join(lines, "\n")
}
