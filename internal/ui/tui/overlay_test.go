package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/i18n"
)

func TestSplitBlocksKeepsOrderAndBalances(t *testing.T) {
	block := func(name string, n int) []string {
		b := []string{name}
		for range n - 1 {
			b = append(b, "·")
		}
		return b
	}
	blocks := [][]string{block("a", 6), block("b", 2), block("c", 3), block("d", 5), block("e", 1)}
	cols := splitBlocks(blocks, 2)
	if len(cols) != 2 {
		t.Fatalf("want 2 columns, got %d", len(cols))
	}
	var order []string
	tallest := 0
	for _, c := range cols {
		tallest = max(tallest, len(c))
		for _, l := range c {
			if l != "·" && l != "" {
				order = append(order, l)
			}
		}
	}
	if strings.Join(order, "") != "abcde" {
		t.Fatalf("groups out of reading order: %v", order)
	}
	if tallest != 11 { // a b | c d e = 9 | 11 beats a b c | d e = 13 | 7
		t.Fatalf("tallest column %d, want 11", tallest)
	}
	if got := splitBlocks(blocks, 1); len(got) != 1 {
		t.Fatalf("one column wanted, got %d", len(got))
	}
}

func TestHelpPagesTurnByKeyAndClick(t *testing.T) {
	m := sized(t, 140, 40)
	m.ov = overlay{kind: ovHelp}
	m.screen()
	m.Update(press("tab"))
	if m.ov.page != 1 || !strings.Contains(ansi.Strip(m.screen()), "a|b") {
		t.Fatalf("Tab goes to the syntax page: page %d", m.ov.page)
	}
	m.Update(press("shift+tab"))
	m.Update(press("shift+tab"))
	if m.ov.page != 2 {
		t.Fatalf("Shift+Tab wraps around to the last page: page %d", m.ov.page)
	}
	x, y := findText(m.screen(), i18n.T("help.tab.keys"))
	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x + 1, Y: y})
	if m.ov.kind != ovHelp || m.ov.page != 0 {
		t.Fatalf("clicking a page name opens it: kind %d page %d", m.ov.kind, m.ov.page)
	}
}

func TestHelpEnterIsTheButtonDrawnAsPrimary(t *testing.T) {
	m := sized(t, 140, 40)
	m.Update(press("?"))
	m.screen()
	if m.ov.kind != ovHelp || m.ov.focus >= 0 {
		t.Fatalf("help opens with no button focused, so the primary Close is drawn as Enter's: kind=%d focus=%d", m.ov.kind, m.ov.focus)
	}
	m.Update(press("enter"))
	if m.ov.active() {
		t.Fatal("Enter closes help")
	}
}

func TestPickerFoldsSingletons(t *testing.T) {
	var items []item
	for i := range 4 {
		items = append(items, item{name: "common" + string(rune('a'+i)), count: 2})
	}
	for i := range 30 {
		items = append(items, item{name: "rare" + string(rune('a'+i)), count: 1})
	}
	o := overlay{items: items, checked: map[string]bool{"rarez": true}}
	vis := o.visible()
	if len(vis) != pickerFill {
		t.Fatalf("默认应补到 %d 行，实际 %d", pickerFill, len(vis))
	}
	if vis[4].name != "rarez" {
		t.Errorf("勾选中的单次项应紧跟常用项，实际 %s", vis[4].name)
	}
	o.filter.SetValue("rare")
	if n := len(o.visible()); n != 30 {
		t.Errorf("输入后应全量匹配 30 项，实际 %d", n)
	}
}

// help, the full message and the handoff pack scroll with the same keys, clamped to the text
func TestOverlayScrollKeys(t *testing.T) {
	for _, kind := range []string{"help", "message", "handoff"} {
		m := newModel(t, demoStore(t), 100, 24)
		openOverlay(m, kind)
		m.screen()
		room, end := m.ov.room, m.ov.scrollMax
		if end == 0 || room == 0 {
			t.Fatalf("%s: the text overflows the box: room=%d scrollMax=%d", kind, room, end)
		}
		for _, step := range []struct {
			key  string
			want int
		}{{"j", 1}, {"k", 0}, {"k", 0}, {"space", min(room, end)}, {"b", 0}, {"G", end}, {"j", end}, {"ctrl+u", end - max(1, room/2)}, {"g", 0}} {
			m.Update(press(step.key))
			m.screen()
			if m.ov.cursor != step.want {
				t.Fatalf("%s: %s scrolls to %d, want %d", kind, step.key, m.ov.cursor, step.want)
			}
		}
		m.Update(press("esc"))
		if m.ov.active() {
			t.Fatalf("%s: Esc closes", kind)
		}
	}
}
