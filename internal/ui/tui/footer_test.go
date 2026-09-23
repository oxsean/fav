package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/render"
)

func TestFooterMatchesEnterBehaviour(t *testing.T) {
	for _, c := range []struct {
		w, h int
		want string
	}{
		{140, 40, "Enter 操作"},
		{80, 24, "Enter 详情"},
		{50, 16, "Enter 详情"},
	} {
		m := sized(t, c.w, c.h)
		got := ansi.Strip(m.footer())
		if !strings.Contains(got, c.want) {
			t.Errorf("%d 列下 footer 应含 %q，got %q", c.w, c.want, got)
		}
	}
}

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

func TestArrowsWhileTyping(t *testing.T) {
	m := sized(t, 120, 40)
	m.focusSearch()
	m.Update(press("notes-api"))
	m.Update(press("down"))
	if m.cursor == 1 || !m.typing {
		t.Fatalf("打字时 ↓ 应选下一条且不退出输入：cursor=%d typing=%v", m.cursor, m.typing)
	}
	m.Update(press("enter"))
	if m.typing || m.ov.kind != ovResume {
		t.Fatalf("选过记录后 Enter 应直接起恢复确认：typing=%v ov=%v", m.typing, m.ov.kind)
	}
}

func TestCtrlCQuitsEverywhere(t *testing.T) {
	for name, setup := range map[string]func(*Model){
		"搜索框":  func(m *Model) { m.focusSearch() },
		"右栏搜索": func(m *Model) { m.startChatSearch() },
		"浮层":   func(m *Model) { m.pickTags() },
	} {
		m := sized(t, 120, 40)
		setup(m)
		m.Update(press("ctrl+c"))
		if !m.quitting {
			t.Errorf("%s里 ctrl+c 没退出", name)
		}
	}
}

func TestResumeCommand(t *testing.T) {
	m := sized(t, 120, 40)
	m.askResume()
	cmd := m.resumeCommand()
	r := m.ov.rec
	if i, j := strings.Index(cmd, r.Cwd), strings.Index(cmd, "claude --resume "+r.SessionID); i < 0 || j < i {
		t.Fatalf("恢复命令不对：%q", cmd)
	}
}

func TestSettingsPanel(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	defer func() { render.RelativeTime = true; fav.DefaultTurns = 3 }()
	m := sized(t, 120, 40)
	m.Update(press(","))
	if m.ov.kind != ovSettings {
		t.Fatal(", 应打开设置面板")
	}
	m.Update(press("right"))
	if m.cfg.RelativeTime || render.RelativeTime {
		t.Fatal("换值应立刻生效")
	}
	m.Update(press("down"))
	m.Update(press("down"))
	m.Update(press("down"))
	m.Update(press("right"))
	if m.cfg.MinTurns != 5 || fav.DefaultTurns != 5 {
		t.Fatalf("阈值没生效：cfg=%d global=%d", m.cfg.MinTurns, fav.DefaultTurns)
	}
	if got := fav.LoadConfig(); got.RelativeTime || got.MinTurns != 5 {
		t.Fatalf("没落盘：%+v", got)
	}
	m.Update(press("esc"))
	if m.ov.active() {
		t.Fatal("Esc 应关闭设置面板")
	}
}

func TestDragSelectStaysInRegion(t *testing.T) {
	m := sized(t, 140, 40)
	m.screen()
	lw := m.listWidth()
	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: lw + 5, Y: 12})
	m.Update(tea.MouseMotionMsg{Button: tea.MouseLeft, X: lw + 20, Y: 14})
	for y := 12; y <= 14; y++ {
		from, to := m.sel.span(y, m.w)
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
		from, to := m.sel.span(y, m.w)
		if from < m.ovX+1 || to > m.ovX+m.ovW-1 {
			t.Fatalf("第 %d 行选区 [%d,%d) 穿出了浮层 [%d,%d)", y, from, to, m.ovX+1, m.ovX+m.ovW-1)
		}
	}
	from, _ := m.sel.span(m.ovY+m.ovH+1, m.w)
	if from != 0 {
		t.Fatal("浮层外的行不该在选区里")
	}
}

func TestOpenInIDE(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 120, 40)
	if m.ideName() != defaultIDE() {
		t.Fatalf("默认 IDE 应是 %s", defaultIDE())
	}
	m.Update(press(","))
	for i, s := range settingsTable() {
		if s.text != nil {
			m.ov.cursor = i
		}
	}
	m.Update(press("enter"))
	if !m.ov.editing {
		t.Fatal("Enter 应进编辑")
	}
	m.Update(press("/no/such/ide"))
	m.Update(press("enter"))
	if m.ov.editing || m.cfg.IDE != defaultIDE()+"/no/such/ide" && m.cfg.IDE != "/no/such/ide" || fav.LoadConfig().IDE != m.cfg.IDE {
		t.Fatalf("Enter 应保存并落盘：editing=%v ide=%q", m.ov.editing, m.cfg.IDE)
	}
	m.cfg.IDE = "/no/such/ide"
	m.Update(press("esc"))
	m.askResume()
	m.ov.rec.Cwd = t.TempDir()
	m.screen()
	m.Update(press("i"))
	if m.ov.focus < 0 || m.notice != "" {
		t.Fatal("i only focuses the IDE button: its meaning differs from the list")
	}
	m.Update(press("i"))
	if !strings.Contains(m.notice, "没打开") || !strings.Contains(m.notice, "/no/such/ide") {
		t.Fatalf("不存在的 IDE 应报错并留在框里：%q", m.notice)
	}
	if v := ansi.Strip(m.renderResume()); !strings.Contains(v, "i IDE") || !strings.Contains(v, "c VS Code") {
		t.Fatal("恢复框应有 IDE / VS Code 两个按钮")
	}
	if err := openDir("code", "/definitely/not/here"); err == nil || !strings.Contains(err.Error(), "目录不存在") {
		t.Fatalf("目录不存在应直接报：%v", err)
	}
}

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
