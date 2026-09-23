package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDragSelectStaysInRegion(t *testing.T) {
	m := sized(t, 140, 40)
	m.screen()
	lw := m.listWidth()
	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: lw + 5, Y: 12})
	m.Update(tea.MouseMotionMsg{Button: tea.MouseLeft, X: lw + 20, Y: 14})
	for y := 12; y <= 14; y++ {
		from, to := m.sel.span(y)
		if from < lw+1 || to > m.w {
			t.Fatalf("第 %d 行选区 [%d,%d) 穿到了左栏", y, from, to)
		}
	}
	m.Update(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: lw + 20, Y: 14})
	m.ov = overlay{kind: ovHelp}
	m.screen()
	if m.ovW == 0 {
		t.Fatal("渲染浮层后应记下框的大小")
	}
	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: m.ovX + 3, Y: m.ovY + 2})
	m.Update(tea.MouseMotionMsg{Button: tea.MouseLeft, X: m.ovX + 10, Y: m.ovY + 4})
	for y := m.ovY + 2; y <= m.ovY+4; y++ {
		from, to := m.sel.span(y)
		if from < m.ovX+1 || to > m.ovX+m.ovW-1 {
			t.Fatalf("第 %d 行选区 [%d,%d) 穿出了浮层 [%d,%d)", y, from, to, m.ovX+1, m.ovX+m.ovW-1)
		}
	}
	from, _ := m.sel.span(m.ovY + m.ovH + 1)
	if from != 0 {
		t.Fatal("浮层外的行不该在选区里")
	}
}
