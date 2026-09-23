package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
)

func TestWheel(t *testing.T) {
	m := sized(t, 120, 40)
	down := tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 5, Y: 20}
	up := tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 5, Y: 20}
	var recRows []int // row of the record (the date header's row depends on the time of day)
	for i, r := range m.rows {
		if r.rec != nil {
			recRows = append(recRows, i)
		}
	}
	spin := func(msg tea.MouseMsg, n int) { // events are batched; the frame tick applies them
		for range n {
			m.Update(msg)
		}
		m.Update(wheelTickMsg{})
	}
	spin(down, 2*m.wheelStep)
	if m.cursor != recRows[2] {
		t.Fatalf("%d 个滚轮事件应走两步到第三条（行 %d），cursor=%d", 2*m.wheelStep, recRows[2], m.cursor)
	}
	spin(up, 4*m.wheelStep)
	if m.cursor != recRows[0] || m.chipFocus != -1 {
		t.Fatalf("滚回顶部应停在第一条、不进 chip 行：cursor=%d chipFocus=%d", m.cursor, m.chipFocus)
	}
	r := m.current()
	var msgs []capture.Message
	for range 30 {
		msgs = append(msgs, capture.Message{Role: "user", Text: "一行"})
	}
	m.probes = map[*fav.Rec]*probe{r: {done: true, msgs: msgs}}
	m.screen()
	right := tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: m.listWidth() + 5, Y: 20}
	m.wheelAcc = 0
	spin(right, 3*m.wheelStep)
	if m.chatScroll != 1 || m.chatSkip != 0 || m.cursor != 1 {
		t.Fatalf("右栏滚三行应正好翻到第二句：chatScroll=%d skip=%d cursor=%d", m.chatScroll, m.chatSkip, m.cursor)
	}
	frame := m.screen()
	m.Update(right)
	if m.chatScroll != 1 || m.chatSkip != 0 || !m.reuseFrame {
		t.Fatal("an event waiting for the frame tick must not move anything and must reuse the last frame")
	}
	if m.screen() != frame || m.reuseFrame {
		t.Fatal("a pending event must return the previous frame unchanged")
	}
	m.Update(wheelTickMsg{})
	if m.chatScroll != 1 || m.chatSkip != 0 || !m.reuseFrame {
		t.Fatal("one event is less than a step: the tick must not move anything and must reuse the frame")
	}
	m.screen()
	m.probes[r].msgs = m.probes[r].msgs[:3]
	m.chatScroll, m.chatSkip = 0, 0
	m.screen()
	if m.chatRoom < 12 {
		t.Skipf("面板太矮（%d 行），本用例不成立", m.chatRoom)
	}
	spin(right, m.wheelStep)
	m.Update(press("J"))
	if m.chatScroll != 0 || m.chatSkip != 0 {
		t.Fatalf("内容装得下时不该滚过头：scroll=%d skip=%d", m.chatScroll, m.chatSkip)
	}
}

// wheel up after expanding must reach the first group header
func TestWheelReachesFirstGroup(t *testing.T) {
	m := sized(t, 140, 40)
	m.setView(viewProjects)
	m.move(1)
	m.toggleGroup()
	for range 20 {
		m.wheel(1, 0)
	}
	for range 200 {
		m.wheel(-1, 0)
	}
	m.screen()
	if m.cursor != 0 || m.scroll != 0 {
		t.Fatalf("应回到顶上：cursor=%d scroll=%d", m.cursor, m.scroll)
	}
}
