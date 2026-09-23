package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/testkit"
)

func fixture(t *testing.T) *fav.Store {
	t.Helper()
	s, err := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	base, cwd := time.Now(), t.TempDir()
	seed := []struct {
		title, project, provider string
		tags                     []string
		ago                      time.Duration
	}{
		{"notes-api 搜索分页游标漂移排障", "notes-api", fav.ProviderClaude, []string{"notes-api", "pagination", "debug"}, time.Hour},
		{"SaaS 公共能力盘点补全", "webapp", fav.ProviderCodex, []string{"saas", "design"}, 4 * time.Hour},
		{"WebSocket launch regression", "notes-api", fav.ProviderClaude, []string{"notes-api", "debug", "websocket"}, 9 * time.Hour},
		{"RBAC 数据范围设计", "webapp", fav.ProviderCodex, []string{"rbac", "design"}, 30 * time.Hour},
	}
	for _, x := range seed {
		r := &fav.Rec{
			ID: fav.NewID(), Provider: x.provider, SessionID: fav.NewID(),
			Title: x.title, Summary: "排查搜索接口游标分页重复返回同一条的问题。确认 region-specific toggle 未创建时后端回退到 embedded YAML default。",
			Project: x.project, Tags: x.tags, Status: fav.StatusDone,
			Cwd: cwd, GitBranch: "feature/cursor-pagination",
			FavoritedAt: new(base.Add(-x.ago)),
		}
		if err := s.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func noIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func sized(t *testing.T, w, h int) *Model { return newModel(t, fixture(t), w, h) }

// newModel is a w×h Model on st with an empty index; FAV_HOME is the test's own unless it set one.
func newModel(t *testing.T, st *fav.Store, w, h int) *Model {
	t.Helper()
	if testkit.Shared(os.Getenv("FAV_HOME")) {
		t.Setenv("FAV_HOME", t.TempDir())
	}
	m := New(st, noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

var pressCodes = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "backspace": tea.KeyBackspace, "up": tea.KeyUp, "down": tea.KeyDown,
	"left": tea.KeyLeft, "right": tea.KeyRight, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd,
}

// press is the key message bubbletea delivers for k, spelled as msg.String() reports it ("enter", "ctrl+s", "space", "J", "，", or typed text).
func press(k string) tea.KeyPressMsg {
	switch k {
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	}
	if c, ok := pressCodes[k]; ok {
		return tea.KeyPressMsg{Code: c}
	}
	if rest, ok := strings.CutPrefix(k, "ctrl+"); ok && utf8.RuneCountInString(rest) == 1 {
		r, _ := utf8.DecodeRuneInString(rest)
		return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
	}
	if r, n := utf8.DecodeRuneInString(k); n == len(k) {
		return tea.KeyPressMsg{Code: r, Text: k}
	}
	return tea.KeyPressMsg{Code: tea.KeyExtended, Text: k}
}

func viewLines(m *Model) []string {
	return strings.Split(m.screen(), "\n")
}

// two panes from 100 columns; the footer says what Enter does there
func TestLayoutSwitchesOnWidth(t *testing.T) {
	for _, c := range []struct {
		w, h  int
		two   bool
		enter string
	}{{140, 40, true, "footer.enter_actions"}, {80, 24, false, "footer.enter_details"}, {50, 16, false, "footer.enter_details"}} {
		m := sized(t, c.w, c.h)
		if m.twoColumn() != c.two || strings.Contains(ansi.Strip(m.screen()), i18n.T("card.resume_target")) != c.two {
			t.Errorf("%d 列：双栏=%v，右侧预览只在双栏", c.w, m.twoColumn())
		}
		if f := ansi.Strip(m.footer()); !strings.Contains(f, keyed(enterKey, i18n.T(c.enter))) {
			t.Errorf("%d 列下 footer 应说 Enter 做什么：%q", c.w, f)
		}
	}
}

// while typing letters are text; arrows and Ctrl+N / Ctrl+P move the list; Enter after moving opens the dialog
func TestTypingKeys(t *testing.T) {
	m := sized(t, 140, 40)
	m.Update(press("/"))
	if !m.typing {
		t.Fatal("/ 应聚焦搜索框")
	}
	for _, r := range "tpsdx" {
		m.Update(press(string(r)))
	}
	if m.ov.active() || m.search.Value() != "tpsdx" {
		t.Fatalf("输入状态下字母是文字，不触发快捷键：ov=%v %q", m.ov.active(), m.search.Value())
	}
	m.search.SetValue("")
	m.Update(press("notes-api"))
	first := m.current()
	m.Update(press("down"))
	if m.current() == first || !m.typing {
		t.Fatalf("打字时 ↓ 选下一条且不退出输入：typing=%v", m.typing)
	}
	m.Update(press("ctrl+p"))
	if m.current() != first || !m.typing {
		t.Fatalf("ctrl+p 选上一条且不退出输入：typing=%v", m.typing)
	}
	m.Update(press("ctrl+n"))
	m.Update(press("enter"))
	if m.typing || m.ov.kind != ovResume {
		t.Fatalf("选过记录后 Enter 应直接起恢复确认：typing=%v ov=%v", m.typing, m.ov.kind)
	}
}

// a paste types only into the focused input
func TestPasteGoesOnlyToTheFocusedInput(t *testing.T) {
	m := sized(t, 140, 40)
	paste := func() { m.Update(tea.PasteMsg{Content: "粘贴"}) }
	paste()
	if m.search.Value() != "" || m.chat.input.Value() != "" {
		t.Fatal("nothing focused: the paste goes nowhere")
	}
	m.focusSearch()
	paste()
	if m.search.Value() != "粘贴" {
		t.Fatalf("the search box takes it: %q", m.search.Value())
	}
	m.Update(press("esc"))
	m.search.SetValue("")
	m.refresh()
	m.startChatSearch()
	paste()
	if m.chat.input.Value() != "粘贴" || m.search.Value() != "" {
		t.Fatalf("the chat find takes it, the search box not: %q / %q", m.chat.input.Value(), m.search.Value())
	}
	m.Update(press("esc"))
	m.pickTags()
	paste()
	if m.ov.filter.Value() != "粘贴" {
		t.Fatalf("the picker filter takes it: %q", m.ov.filter.Value())
	}
	m.closeOverlay()
	m.openEdit(m.current())
	title, tags, summary := m.ov.edit.Value(), m.ov.edit2.Value(), m.ov.area.Value()
	paste()
	if m.ov.edit.Value() != title+"粘贴" || m.ov.edit2.Value() != tags || m.ov.area.Value() != summary {
		t.Fatalf("only the focused title field takes it: %q %q", m.ov.edit.Value(), m.ov.edit2.Value())
	}
}

func TestTabKeepsFilters(t *testing.T) {
	m := sized(t, 140, 40)
	m.search.SetValue("#debug")
	m.refresh()
	before := m.countRecs()

	m.navKey(press("tab"))
	if m.view != viewSessions {
		t.Fatal("Tab 应切到「会话」")
	}
	if m.search.Value() != "#debug" {
		t.Fatal("切视图不应清空查询")
	}
	if got := m.countRecs(); got != before {
		t.Fatalf("切视图后命中数变了：%d -> %d", before, got)
	}
}

func TestOverlayCancelKeepsQuery(t *testing.T) {
	m := sized(t, 140, 40)
	m.search.SetValue("#debug websocket")
	m.refresh()
	m.pickTags()
	m.overlayKey(press("esc"))
	if m.ov.active() {
		t.Fatal("Esc 应关闭浮层")
	}
	if got := m.search.Value(); got != "#debug websocket" {
		t.Fatalf("取消不该动查询，got %q", got)
	}
}

func TestPickTagsReplacesOnlyTagTokens(t *testing.T) {
	m := sized(t, 140, 40)
	m.search.SetValue("#old websocket project:notes-api")
	m.refresh()
	m.pickTags()
	m.ov.checked = map[string]bool{"debug": true}
	m.overlayKey(press("enter"))

	got := m.search.Value()
	if strings.Contains(got, "#old") {
		t.Errorf("旧标签应被换掉，got %q", got)
	}
	for _, want := range []string{"websocket", "project:notes-api", "#debug"} {
		if !strings.Contains(got, want) {
			t.Errorf("查询串里应保留/加入 %q，got %q", want, got)
		}
	}
}

func TestNavigationSkipsGroupHeaders(t *testing.T) {
	m := sized(t, 140, 40)
	for i := 0; i < len(m.rows)+2; i++ {
		if m.current() == nil {
			t.Fatalf("时间线光标停在了分组标题行（第 %d 行）", m.cursor)
		}
		m.move(1)
	}
	m.setView(viewProjects)
	if m.current() != nil || m.rows[m.cursor].group == "" || !m.rows[m.cursor].folded {
		t.Fatalf("项目视图应默认全折、光标停在第一个分组标题：cursor=%d %+v", m.cursor, m.rows[m.cursor])
	}
	for _, r := range m.rows {
		if r.rec != nil {
			t.Fatal("默认不该有展开的分组")
		}
	}
	if v := ansi.Strip(m.screen()); !strings.Contains(v, "Enter 展开") {
		t.Fatal("底栏应提示 Enter 展开")
	}
	m.Update(press("enter"))
	if m.rows[m.cursor].folded || m.rows[m.cursor+1].rec == nil {
		t.Fatal("Enter 应展开光标下的分组")
	}
	m.move(1)
	if m.current() == nil {
		t.Fatal("展开后 j 应进到第一条记录")
	}
}

func TestViewHotkeys(t *testing.T) {
	m := sized(t, 140, 40)
	key := func(k tea.KeyPressMsg) { m.Update(k) }
	key(press("shift+tab"))
	if m.view != viewLive {
		t.Fatalf("收藏页 Shift+Tab 应绕到最后一页 Agents：%v", m.view)
	}
	key(press("3"))
	if m.view != viewProjects {
		t.Fatalf("3 应跳到项目：%v", m.view)
	}
	key(press("；"))
	if m.chipFocus != 0 {
		t.Fatal("； 应进筛选行")
	}
}

func TestToggleArchiveRefreshes(t *testing.T) {
	m := sized(t, 140, 40)
	before, r := m.countRecs(), m.current()
	m.toggleArchive(m.current())
	if m.current() != r || m.countRecs() != before || m.notice == "" {
		t.Fatalf("刚归档的应还在光标下并有反馈：cur==r=%v %d -> %d", m.current() == r, before, m.countRecs())
	}
	m.toggleArchive(m.current())
	if r.Archived() {
		t.Fatal("再按 a 取消归档的应是同一条")
	}
	m.toggleStatus(m.current(), fav.StatusDone)
	if r.Status != fav.StatusDoing {
		t.Fatalf("默认是已完成，x 应改成进行中：%s", r.Status)
	}
	m.toggleArchive(m.current())
	if !r.Archived() || r.Status != fav.StatusDoing {
		t.Fatalf("归档不动看板状态：%s archived=%v", r.Status, r.Archived())
	}
	m.toggleArchive(m.current())
	if r.Archived() || r.Status != fav.StatusDoing {
		t.Fatalf("取消归档也不动看板状态：%s", r.Status)
	}
	m.toggleStatus(m.current(), fav.StatusDone)
	if !r.Done() {
		t.Fatalf("x 再按回到已完成：%s", r.Status)
	}
	m.toggleArchive(m.current())
	m.move(1)
	m.refresh()
	if got := m.countRecs(); got != before-1 {
		t.Fatalf("光标离开后应从默认列表消失：%d -> %d", before, got)
	}
}

func TestFoldAll(t *testing.T) {
	m := sized(t, 120, 40)
	m.setView(viewProjects)
	open := func() int {
		n := 0
		for _, r := range m.rows {
			if r.rec != nil {
				n++
			}
		}
		return n
	}
	if open() != 0 {
		t.Fatal("默认应全折")
	}
	m.navKey(press("z"))
	before := open()
	if before == 0 {
		t.Fatal("z 应全展")
	}
	m.navKey(press("z"))
	if open() != 0 {
		t.Fatalf("再按 z 应全折，还剩 %d 条可见", open())
	}
	m.navKey(press("-"))
	if open() != 0 {
		t.Fatal("- 应全折")
	}
	m.navKey(press("="))
	if open() != before {
		t.Fatal("= 应全展")
	}
}

func TestDatePicker(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.Local) // a Monday
	for _, c := range []struct{ in, want string }{
		{"09-01", "last:2026-09-01"}, // open-ended: active since that day
		{"9-1..9-15", "after:2026-09-01 before:2026-09-16"},
		{"..09-15", "before:2026-09-16"},
		{"7d", "last:7d"},
		{"2w", "last:2w"},
		{"下周", ""},
	} {
		it, ok := dateItem(c.in, now)
		if ok != (c.want != "") || it.name != c.want {
			t.Errorf("dateItem(%q) = %q, %v; want %q", c.in, it.name, ok, c.want)
		}
	}
	byLabel := map[string]string{}
	for _, it := range datePresets(now) {
		byLabel[it.label] = it.name
	}
	if byLabel["昨天"] != "after:2026-09-20 before:2026-09-21" || byLabel["上周"] != "after:2026-09-14 before:2026-09-21" || byLabel["上月"] != "after:2026-08-01 before:2026-09-01" {
		t.Errorf("预设区间不对：%v", byLabel)
	}
	if byLabel["今天"] != "last:2026-09-21" || byLabel["本周"] != "last:2026-09-21" {
		t.Errorf("open-ended presets mean active since: %v", byLabel)
	}

	m := newModel(t, fixture(t), 80, 24)
	m.pickDate()
	m.ov.filter.SetValue("9-1..9-15")
	m.Update(press("enter"))
	if got := m.search.Value(); !strings.Contains(got, "after:2026-09-01") || !strings.Contains(got, "before:2026-09-16") {
		t.Errorf("手输区间没进查询串：%q", got)
	}
	m.pickDate()
	if vis := m.ov.visible(); vis[m.ov.cursor].name != "after:2026-09-01 before:2026-09-16" || !strings.HasPrefix(vis[0].label, "当前 ") {
		t.Errorf("再开时间框应把当前值列在最前并停在上面：%d %v", m.ov.cursor, vis[0])
	}
	m.ov.filter.SetValue("本")
	if vis := m.ov.visible(); len(vis) != 2 || vis[0].label != "本周" {
		t.Errorf("按标签找预设：%v", vis)
	}
}

func TestPickerClear(t *testing.T) {
	m := newModel(t, fixture(t), 80, 24)
	m.search.SetValue("geo #pagination last:7d")
	m.pickTags()
	m.ov.filter.SetValue("x")
	m.Update(press("ctrl+u"))
	m.Update(press("backspace"))
	if !m.ov.active() || m.ov.filter.Value() != "" {
		t.Fatalf("多选框里 ^U 只清输入、空着退格不关框（勾选不能误丢）：ov=%v %q", m.ov.active(), m.ov.filter.Value())
	}
	m.clearPicker()
	if m.ov.active() || m.search.Value() != "geo last:7d" {
		t.Fatalf("清空按钮应只清掉标签：ov=%v %q", m.ov.active(), m.search.Value())
	}
	m.pickDate()
	m.ov.filter.SetValue("本")
	m.Update(press("backspace"))
	if !m.ov.active() || m.ov.filter.Value() != "" {
		t.Fatalf("搜索框有字时退格只删字：ov=%v %q", m.ov.active(), m.ov.filter.Value())
	}
	m.Update(press("backspace"))
	if m.ov.active() || m.search.Value() != "geo" {
		t.Fatalf("空着再退格应清掉时间：ov=%v %q", m.ov.active(), m.search.Value())
	}
}

func TestConventionalKeys(t *testing.T) {
	m := sized(t, 140, 40)
	key := func(k string) { m.Update(press(k)) }
	key("l")
	if m.pane != paneChat {
		t.Fatal("l 应把焦点切到右栏")
	}
	key("/")
	if !m.typing || m.chat.typing {
		t.Fatal("/ 在哪都是搜会话，右栏有焦点时也一样")
	}
	m.Update(press("esc"))
	key(">")
	if !m.typing || !m.msgMode() {
		t.Fatal("> 打开搜消息")
	}
	m.search.SetValue("")
	m.refresh()
	m.Update(press("esc"))
	key("l")
	key("h")
	m.cursor = 0
	m.clampCursor()
	before := m.cursor
	m.Update(press("ctrl+d"))
	if m.cursor <= before {
		t.Fatalf("ctrl+d 应往下走半页：%d", m.cursor)
	}
}

// switching to the Agents page (one chip fewer) must not leave the chip focus out of range; status:live works on the favorites page too
func TestViewSwitchClampsChip(t *testing.T) {
	m := sized(t, 140, 40)
	m.chipFocus = 4
	m.setView(viewLive)
	m.Update(press("enter"))
	if m.chipFocus != 3 {
		t.Fatalf("chipFocus=%d", m.chipFocus)
	}
	m.setView(viewFavorites)
	m.search.SetValue("status:live")
	m.refresh()
	if m.countRecs() != 0 {
		t.Fatal("没有在跑的会话时应为空，而不是全部")
	}
}

// project view sort change: no group lost, the cursor stays on the same group at the same screen row, the header counts all records
func TestProjectsSortKeepsGroups(t *testing.T) {
	m := sized(t, 140, 40)
	m.setView(viewProjects)
	groups := func() map[string]bool {
		g := map[string]bool{}
		for _, r := range m.rows {
			if r.group != "" {
				g[r.group] = true
			}
		}
		return g
	}
	before, total := groups(), m.countRecs()
	if total == 0 {
		t.Fatal("全折着也该数出记录数")
	}
	m.move(1)
	name := m.rows[m.cursor].group
	row := m.rowTop(m.cursor) - m.scroll
	for range 4 {
		m.Update(press("o"))
		if g := groups(); len(g) != len(before) {
			t.Fatalf("%s：分组数 %d → %d", m.sortBy.label(), len(before), len(g))
		}
		if m.rows[m.cursor].group != name || m.rowTop(m.cursor)-m.scroll != row {
			t.Fatalf("%s：光标应留在「%s」原屏幕行 %d，实际「%s」%d", m.sortBy.label(), name, row, m.rows[m.cursor].group, m.rowTop(m.cursor)-m.scroll)
		}
		if m.countRecs() != total {
			t.Fatalf("记录数变了：%d → %d", total, m.countRecs())
		}
	}
}

func TestTitledBorderCornerAligned(t *testing.T) {
	for _, w := range []int{30, 57, 80} {
		top := ansi.Strip(titledTopBorder("项目 58 个 · 506 条", w))
		if ansi.StringWidth(top) != w || !strings.HasSuffix(top, "╮") {
			t.Fatalf("w=%d: 上边框应正好 w 列且以 ╮ 结尾：%q (%d)", w, top, ansi.StringWidth(top))
		}
	}
}

func TestProjectArrowsAndInfoPane(t *testing.T) {
	m := sized(t, 140, 40)
	m.setView(viewProjects)
	m.refresh()
	if m.current() != nil {
		t.Fatal("项目页起始应停在分组标题上")
	}
	g := m.rows[m.cursor].group
	v := ansi.Strip(m.screen())
	if !strings.Contains(v, "项目") || !strings.Contains(v, "最近会话") || !strings.Contains(v, "个会话") {
		t.Fatalf("右栏应显示项目信息\n%s", v)
	}
	m.Update(press("right"))
	if !m.open[g] || m.rows[m.cursor+1].rec == nil {
		t.Fatal("→ 应展开分组")
	}
	m.Update(press("down"))
	if m.current() == nil {
		t.Fatal("↓ 应停到子项")
	}
	m.Update(press("left"))
	if m.open[g] || m.rows[m.cursor].group != g {
		t.Fatalf("子项上 ← 应折叠并回到分组标题：open=%v row=%+v", m.open[g], m.rows[m.cursor])
	}
	m.Update(press("left"))
	if m.pane != paneList || m.rows[m.cursor].group != g {
		t.Fatal("折着的分组上再按 ← 什么都不做")
	}
}

func TestProjectClicks(t *testing.T) {
	m := sized(t, 140, 40)
	m.setView(viewProjects)
	m.refresh()
	first := m.rows[0].group
	m.Update(press("down"))
	if m.cursor == 0 {
		t.Fatal("要有第二个分组")
	}
	m.screen()
	for y := 0; y < m.h && !m.open[first]; y++ {
		if act := m.hit(3, y); act != nil {
			act(m)
		}
	}
	if m.cursor != 0 || !m.open[first] || m.rows[1].rec == nil {
		t.Fatalf("点第一个分组应展开它并把光标移过去：cursor=%d open=%v", m.cursor, m.open[first])
	}
	m.open[first] = false
	m.refresh()
	m.screen()
	target := m.groups[first][0]
	m.jumpTo(first, target)
	if m.current() != target || !m.open[first] {
		t.Fatal("右栏点会话应展开分组并选中它")
	}
}

func TestProjectKeyboardJump(t *testing.T) {
	m := sized(t, 140, 40)
	m.setView(viewProjects)
	m.refresh()
	g := m.rows[0].group
	right := press("right")
	m.Update(right)
	if !m.open[g] || m.pane != paneList {
		t.Fatal("第一下 → 展开")
	}
	m.Update(right)
	if !m.projectFocus() {
		t.Fatal("第二下 → 焦点到右栏")
	}
	m.Update(press("down"))
	m.Update(press("enter"))
	if m.pane != paneList || m.current() != m.groups[g][1] {
		t.Fatalf("↓ Enter 应跳到分组里第二条：%v", m.current())
	}
	if v := ansi.Strip(m.screen()); !strings.Contains(v, m.groups[g][1].Title[:6]) {
		t.Fatal("跳过去后右栏应是那条会话")
	}
}

func TestSearchBarClickPlacesCursor(t *testing.T) {
	m := sized(t, 140, 40)
	m.search.SetValue("webapp rbac")
	m.screen()
	x := 2 + ansi.StringWidth(m.search.Prompt) + len("webapp ")
	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: 1})
	if !m.typing || m.search.Position() != len("webapp ") {
		t.Fatalf("点搜索框应进入输入且光标落在点的那列：typing=%v pos=%d", m.typing, m.search.Position())
	}
}

func TestBrokenSessionOffersMoveOrDelete(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	r.Cwd = filepath.Join(t.TempDir(), "gone")
	m.refresh()
	if v := ansi.Strip(strings.Join(m.cardBox(r, true, m.listWidth()), "\n")); !strings.Contains(v, r.Title[:6]) || !strings.Contains(v, " !") {
		t.Fatalf("目录没了的卡片标题后应有 ! 小标：\n%s", v)
	}
	m.Update(press("space"))
	if m.ov.kind != ovResume || m.quitting {
		t.Fatalf("Space 不该直接恢复，应停在恢复框：kind=%d quit=%v", m.ov.kind, m.quitting)
	}
	m.screen()
	if v := ansi.Strip(m.screen()); strings.Contains(v, "Enter 恢复") || !strings.Contains(v, "M 移动") || !strings.Contains(v, "D 删除") {
		t.Fatalf("恢复框应只给移动 / 删除：\n%s", v)
	}
	m.Update(press("enter"))
	if m.ov.kind != ovPicker || m.ov.browse == nil {
		t.Fatalf("目录没了时 Enter 应进移动流程：kind=%d", m.ov.kind)
	}
}

// every level in must pair with a level out: → right pane, → full text, ← / Esc out; clicking the list refocuses it; ← also leaves the narrow detail
func TestDrillInAndBack(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	m.probes = map[*fav.Rec]*probe{r: {done: true, msgs: []capture.Message{{Role: "user", Text: "一句话"}}}}
	key := func(k string) { m.Update(press(k)) }
	key("right")
	if m.pane != paneChat {
		t.Fatal("→ 应进右栏")
	}
	key("right")
	if m.ov.kind != ovMessage {
		t.Fatal("右栏里 → 应打开整句全文")
	}
	key("esc")
	if m.ov.active() || m.pane != paneChat {
		t.Fatal("Esc 只退一层：回到右栏")
	}
	key("left")
	if m.pane != paneList {
		t.Fatal("← 应回左栏")
	}
	key("right")
	m.clickRow(1)
	if m.pane != paneList || m.cursor != 1 {
		t.Fatalf("点左栏焦点应回左栏：pane=%d cur=%d", m.pane, m.cursor)
	}

	m.w = 80 // narrow: the first Enter opens the detail, the second the dialog; ← and Esc both leave
	key("enter")
	if !m.detail || m.ov.active() {
		t.Fatal("窄屏第一次 Enter 应进详情，不直接起恢复框")
	}
	key("enter")
	if m.ov.kind != ovResume {
		t.Fatal("详情页里再按 Enter 应起恢复框")
	}
	key("esc")
	key("left")
	if m.detail {
		t.Fatal("窄屏详情 ← 应退回列表")
	}
}

func TestMain(m *testing.M) {
	copyText = func(string) error { return nil } // ⚠️ never the user's clipboard
	testkit.Main(m)
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

func TestNoticeExpires(t *testing.T) {
	m := sized(t, 140, 40)
	m.askResume()
	_, cmd := m.Update(press("y"))
	if !strings.HasPrefix(m.notice, i18n.F("resume.copied", "")) || cmd == nil {
		t.Fatalf("copying says so and schedules the notice's expiry: %q", m.notice)
	}
	seq := m.noticeSeq
	m.flash("newer")
	m.Update(noticeExpiredMsg{seq})
	if m.notice != "newer" {
		t.Fatal("an older expiry leaves a newer notice alone")
	}
	m.Update(noticeExpiredMsg{m.noticeSeq})
	if m.notice != "" {
		t.Fatal("the notice goes away when its time is up")
	}
}

// Space never starts anything: it switches to a session already in a Herdr tab, otherwise it only opens the dialog.
func TestSpaceOnlyOpensTheDialogOrSwitches(t *testing.T) {
	m := sized(t, 140, 40)
	capture.SetAppAvailable(fav.ProviderClaude, true)
	r := m.current()
	r.SessionID, r.Provider = "c5126b86-64bb-46a8-9a69-fc421c8f4f9a", fav.ProviderClaude
	appFiles(t, r)
	m.cfg.ResumeIn = fav.ResumeApp
	m.Update(press("space"))
	if m.quitting || m.ov.kind != ovResume || m.notice != "" {
		t.Fatalf("Space opens the dialog, like Enter: quitting=%v kind=%d notice=%q", m.quitting, m.ov.kind, m.notice)
	}
	m.closeOverlay()
	m.pane = paneChat
	m.Update(press("space"))
	if m.ov.active() || m.quitting {
		t.Fatal("in the chat Space pages, it does not open the dialog")
	}
	m.pane = paneList
	m.Update(liveMsg{herdr: map[string]capture.Live{r.SessionID: {TabID: "t1", Status: "working", Since: time.Now()}}})
	m.Update(press("space"))
	if m.ov.active() || m.quitting || m.notice != i18n.T("resume.switching") {
		t.Fatalf("in a Herdr tab Space switches to it, no dialog: ov=%v notice=%q", m.ov.kind, m.notice)
	}
}

func TestCompactEnterShowsTheDetail(t *testing.T) {
	m := sized(t, 50, 20)
	before := ansi.Strip(m.screen())
	m.Update(press("enter"))
	if !m.detail || ansi.Strip(m.screen()) == before {
		t.Fatal("under 60 columns Enter shows the detail")
	}
}

func TestSearchFromTheRightPaneMovesTheList(t *testing.T) {
	m := sized(t, 140, 40)
	m.pane = paneChat
	m.Update(press("/"))
	if !m.typing || m.pane != paneList {
		t.Fatal("/ from the right pane: the arrows select records")
	}
}

func TestIndexHeldWhileScrolling(t *testing.T) {
	m := sized(t, 140, 40)
	idx := m.idx
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 5, Y: 20})
	m.Update(indexMsg{idx: idx, changed: true})
	if m.heldIdx == nil {
		t.Fatal("滚动中收到的索引应先攥着")
	}
	m.lastWheel = time.Now().Add(-2 * time.Second)
	m.Update(storeTickMsg{})
	if m.heldIdx != nil {
		t.Fatal("停下来后应换上")
	}
}
