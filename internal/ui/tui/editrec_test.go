package tui

import (
	"strings"
	"testing"
)

func TestEditOverlay(t *testing.T) {
	m := sized(t, 120, 40)
	r := m.current()
	m.Update(press("e"))
	if m.ov.kind != ovEdit {
		t.Fatal("e 应打开编辑框")
	}
	m.Update(press(" 改"))
	m.Update(press("tab"))
	m.ov.edit2.SetValue("#a b, C")
	m.Update(press("tab"))
	m.Update(press("第一行"))
	m.Update(press("enter"))
	m.Update(press("第二行"))
	if !m.ov.active() {
		t.Fatal("摘要栏里 Enter 不该保存")
	}
	m.Update(press("ctrl+s"))
	if m.ov.active() || !strings.HasSuffix(r.Title, " 改") || strings.Join(r.Tags, ",") != "a,b,c" || !strings.HasSuffix(r.Summary, "第一行\n第二行") {
		t.Fatalf("Ctrl+S 应保存三栏：title=%q tags=%v summary=%q ov=%v", r.Title, r.Tags, r.Summary, m.ov.kind)
	}
	if got := m.store.Get(r.ID); got == nil || got.Title != r.Title {
		t.Fatal("应落盘")
	}
	m.Update(press("e"))
	m.Update(press("x"))
	m.Update(press("esc"))
	if !m.ov.active() || !strings.Contains(m.notice, "再按一次") {
		t.Fatalf("有改动时第一下 Esc 只提醒：ov=%v notice=%q", m.ov.active(), m.notice)
	}
	m.Update(press("esc"))
	if m.ov.active() || strings.HasSuffix(r.Title, "x") {
		t.Fatal("第二下 Esc 才放弃")
	}
}
