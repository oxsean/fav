package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func TestMouseFragmentGuard(t *testing.T) {
	m := sized(t, 120, 40)
	m.focusSearch()
	for _, frag := range []string{"[<64;33;12M", "<64;33;12", "64;33;12m"} {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(frag)})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}, Alt: true})
	if got := m.search.Value(); got != "" {
		t.Fatalf("残片进了搜索框：%q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("notes-api;12")})
	if got := m.search.Value(); got != "notes-api;12" {
		t.Fatalf("正常输入被吃了：%q", got)
	}
}

func TestWheel(t *testing.T) {
	m := sized(t, 120, 40)
	down := tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress, X: 5, Y: 20}
	up := tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress, X: 5, Y: 20}
	var recRows []int // row of the record (the date header's row depends on the time of day)
	for i, r := range m.rows {
		if r.rec != nil {
			recRows = append(recRows, i)
		}
	}
	spin := func(msg tea.MouseMsg, n int) { // events are batched; the frame tick applies them
		for i := 0; i < n; i++ {
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
	for i := 0; i < 30; i++ {
		msgs = append(msgs, capture.Message{Role: "user", Text: "一行"})
	}
	m.probes = map[*fav.Rec]*probe{r: {done: true, msgs: msgs}}
	m.View()
	right := tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress, X: m.listWidth() + 5, Y: 20}
	m.wheelAcc = 0
	spin(right, 3*m.wheelStep)
	if m.chatScroll != 1 || m.chatSkip != 0 || m.cursor != 1 {
		t.Fatalf("右栏滚三行应正好翻到第二句：chatScroll=%d skip=%d cursor=%d", m.chatScroll, m.chatSkip, m.cursor)
	}
	frame := m.View()
	m.Update(right)
	if m.chatScroll != 1 || m.chatSkip != 0 || !m.reuseFrame {
		t.Fatal("an event waiting for the frame tick must not move anything and must reuse the last frame")
	}
	if m.View() != frame || m.reuseFrame {
		t.Fatal("a pending event must return the previous frame unchanged")
	}
	m.Update(wheelTickMsg{})
	if m.chatScroll != 1 || m.chatSkip != 0 || !m.reuseFrame {
		t.Fatal("one event is less than a step: the tick must not move anything and must reuse the frame")
	}
	m.View()
	m.probes[r].msgs = m.probes[r].msgs[:3]
	m.chatScroll, m.chatSkip = 0, 0
	m.View()
	if m.chatRoom < 12 {
		t.Skipf("面板太矮（%d 行），本用例不成立", m.chatRoom)
	}
	spin(right, m.wheelStep)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("J")})
	if m.chatScroll != 0 || m.chatSkip != 0 {
		t.Fatalf("内容装得下时不该滚过头：scroll=%d skip=%d", m.chatScroll, m.chatSkip)
	}
}

func TestArrowsWhileTyping(t *testing.T) {
	m := sized(t, 120, 40)
	m.focusSearch()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("notes-api")})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor == 1 || !m.typing {
		t.Fatalf("打字时 ↓ 应选下一条且不退出输入：cursor=%d typing=%v", m.cursor, m.typing)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
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
	if !strings.HasPrefix(cmd, "cd "+r.Cwd+" && claude --resume "+r.SessionID) {
		t.Fatalf("恢复命令不对：%q", cmd)
	}
}

func TestSettingsPanel(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	defer func() { render.RelativeTime = true; fav.DefaultTurns = 3 }()
	m := sized(t, 120, 40)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")})
	if m.ov.kind != ovSettings {
		t.Fatal(", 应打开设置面板")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.cfg.RelativeTime || render.RelativeTime {
		t.Fatal("换值应立刻生效")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.cfg.MinTurns != 5 || fav.DefaultTurns != 5 {
		t.Fatalf("阈值没生效：cfg=%d global=%d", m.cfg.MinTurns, fav.DefaultTurns)
	}
	if got := fav.LoadConfig(); got.RelativeTime || got.MinTurns != 5 {
		t.Fatalf("没落盘：%+v", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.ov.active() {
		t.Fatal("Esc 应关闭设置面板")
	}
}

func TestDragSelectStaysInRegion(t *testing.T) {
	m := sized(t, 140, 40)
	m.View()
	lw := m.listWidth()
	m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: lw + 5, Y: 12})
	m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion, X: lw + 20, Y: 14})
	for y := 12; y <= 14; y++ {
		from, to := m.sel.span(y, m.w)
		if from < lw+1 || to > m.w {
			t.Fatalf("第 %d 行选区 [%d,%d) 穿到了左栏", y, from, to)
		}
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: lw + 20, Y: 14})
	m.ov = overlay{kind: ovHelp}
	m.View()
	if m.ovW == 0 {
		t.Fatal("渲染浮层后应记下框的大小")
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: m.ovX + 3, Y: m.ovY + 2})
	m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion, X: m.ovX + 10, Y: m.ovY + 4})
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
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")})
	for i, s := range settingsTable() {
		if s.text != nil {
			m.ov.cursor = i
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.ov.editing {
		t.Fatal("Enter 应进编辑")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/no/such/ide")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.editing || m.cfg.IDE != defaultIDE()+"/no/such/ide" && m.cfg.IDE != "/no/such/ide" || fav.LoadConfig().IDE != m.cfg.IDE {
		t.Fatalf("Enter 应保存并落盘：editing=%v ide=%q", m.ov.editing, m.cfg.IDE)
	}
	m.cfg.IDE = "/no/such/ide"
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.askResume()
	m.ov.rec.Cwd = t.TempDir()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
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
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.ov.kind != ovEdit {
		t.Fatal("e 应打开编辑框")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" 改")})
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m.ov.edit2.SetValue("#a b, C")
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("第一行")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("第二行")})
	if !m.ov.active() {
		t.Fatal("摘要栏里 Enter 不该保存")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.ov.active() || !strings.HasSuffix(r.Title, " 改") || strings.Join(r.Tags, ",") != "a,b,c" || !strings.HasSuffix(r.Summary, "第一行\n第二行") {
		t.Fatalf("Ctrl+S 应保存三栏：title=%q tags=%v summary=%q ov=%v", r.Title, r.Tags, r.Summary, m.ov.kind)
	}
	if got := m.store.Get(r.ID); got == nil || got.Title != r.Title {
		t.Fatal("应落盘")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.ov.active() || !strings.Contains(m.notice, "再按一次") {
		t.Fatalf("有改动时第一下 Esc 只提醒：ov=%v notice=%q", m.ov.active(), m.notice)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.ov.active() || strings.HasSuffix(r.Title, "x") {
		t.Fatal("第二下 Esc 才放弃")
	}
}
