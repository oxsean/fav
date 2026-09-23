package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/index"
)

func ptr(t time.Time) *time.Time { return &t }

func fixture(t *testing.T) *fav.Store {
	t.Helper()
	s, err := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	seed := []struct {
		title, project, provider string
		tags                     []string
		ago                      time.Duration
		done                     bool
	}{
		{"notes-api 搜索分页游标漂移排障", "notes-api", fav.ProviderClaude, []string{"notes-api", "pagination", "debug"}, time.Hour, false},
		{"SaaS 公共能力盘点补全", "webapp", fav.ProviderCodex, []string{"saas", "design"}, 4 * time.Hour, false},
		{"WebSocket launch regression", "notes-api", fav.ProviderClaude, []string{"notes-api", "debug", "websocket"}, 9 * time.Hour, false},
		{"RBAC 数据范围设计", "webapp", fav.ProviderCodex, []string{"rbac", "design"}, 30 * time.Hour, true},
	}
	for _, x := range seed {
		r := &fav.Rec{
			ID: fav.NewID(), Provider: x.provider, SessionID: fav.NewID(),
			Title: x.title, Summary: "排查搜索接口游标分页重复返回同一条的问题。确认 region-specific toggle 未创建时后端回退到 embedded YAML default。",
			Project: x.project, Tags: x.tags, Status: fav.StatusDone,
			Cwd: "/tmp", GitBranch: "feature/cursor-pagination",
			FavoritedAt: ptr(base.Add(-x.ago)),
		}
		if x.done {
			r.Status = fav.StatusDone
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

func sized(t *testing.T, w, h int) *Model {
	t.Helper()
	if os.Getenv("FAV_HOME") == "" || !strings.HasPrefix(os.Getenv("FAV_HOME"), os.TempDir()) {
		t.Setenv("FAV_HOME", t.TempDir()) // ⚠️ never the real ~/.agent/fav
	}
	m := New(fixture(t), noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func viewLines(m *Model) []string {
	return strings.Split(m.View(), "\n")
}

func TestNoLineExceedsTerminalWidth(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {100, 30}, {80, 24}, {60, 20}, {50, 16}} {
		w, h := size[0], size[1]
		m := sized(t, w, h)

		states := map[string]func(){
			"列表":   func() {},
			"详情":   func() { m.detail = true },
			"项目视图": func() { m.detail = false; m.view = viewProjects; m.refresh() },
			"标签浮层": func() { m.view = viewSessions; m.refresh(); m.pickTags() },
			"恢复浮层": func() { m.closeOverlay(); m.askResume() },
			"帮助浮层": func() { m.ov = overlay{kind: ovHelp} },
		}
		for name, setup := range states {
			setup()
			for i, line := range viewLines(m) {
				if got := ansi.StringWidth(line); got > w {
					t.Errorf("%dx%d %s 第 %d 行宽 %d 列，超出 %d：%q",
						w, h, name, i, got, w, ansi.Strip(line))
				}
			}
			m.closeOverlay()
		}
	}
}

func TestViewHeightMatchesTerminal(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {80, 24}, {50, 16}} {
		m := sized(t, size[0], size[1])
		if got := len(viewLines(m)); got != size[1] {
			t.Errorf("%dx%d 渲染出 %d 行，应为 %d", size[0], size[1], got, size[1])
		}
	}
}

func TestLayoutSwitchesOnWidth(t *testing.T) {
	wide := sized(t, 140, 40)
	if !wide.twoColumn() {
		t.Error("140 列应走双栏")
	}
	if !strings.Contains(ansi.Strip(wide.View()), "恢复目标") {
		t.Error("双栏应在右侧同屏显示预览")
	}

	narrow := sized(t, 80, 24)
	if narrow.twoColumn() {
		t.Error("80 列应退为单栏")
	}
	if strings.Contains(ansi.Strip(narrow.View()), "恢复目标") {
		t.Error("单栏列表页不应并排显示预览")
	}
}

func TestNarrowEnterGoesToDetailFirst(t *testing.T) {
	m := sized(t, 80, 24)
	m.navKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.detail {
		t.Fatal("窄屏第一次 Enter 应进入详情页")
	}
	if m.ov.active() {
		t.Fatal("窄屏第一次 Enter 不应直接起恢复确认")
	}
	m.navKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.kind != ovResume {
		t.Fatal("详情页里再按 Enter 应起恢复确认")
	}
}

func TestKeysAreTextWhileTyping(t *testing.T) {
	m := sized(t, 140, 40)
	m.navKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.typing {
		t.Fatal("/ 应聚焦搜索框")
	}
	for _, r := range "tpsdx" {
		m.searchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.ov.active() {
		t.Fatal("输入状态下的快捷键字母不应触发筛选浮层")
	}
	if got := m.search.Value(); got != "tpsdx" {
		t.Fatalf("应被当作文字输入，got %q", got)
	}
}

func TestTabKeepsFilters(t *testing.T) {
	m := sized(t, 140, 40)
	m.search.SetValue("#debug")
	m.refresh()
	before := m.countRecs()

	m.navKey(tea.KeyMsg{Type: tea.KeyTab})
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
	m.overlayKey(tea.KeyMsg{Type: tea.KeyEsc})
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
	m.overlayKey(tea.KeyMsg{Type: tea.KeyEnter})

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
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Enter 展开") {
		t.Fatal("底栏应提示 Enter 展开")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	key := func(k tea.KeyMsg) { m.Update(k) }
	key(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.view != viewLive {
		t.Fatalf("收藏页 Shift+Tab 应绕到最后一页 Agents：%v", m.view)
	}
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if m.view != viewProjects {
		t.Fatalf("3 应跳到项目：%v", m.view)
	}
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("；")})
	if m.chipFocus != 0 {
		t.Fatal("； 应进筛选行")
	}
}

func TestToggleArchiveRefreshes(t *testing.T) {
	m := sized(t, 140, 40)
	before, r := m.countRecs(), m.current()
	m.toggleArchive()
	if m.current() != r || m.countRecs() != before || m.notice == "" {
		t.Fatalf("刚归档的应还在光标下并有反馈：cur==r=%v %d -> %d", m.current() == r, before, m.countRecs())
	}
	m.toggleArchive()
	if r.Archived() {
		t.Fatal("再按 a 取消归档的应是同一条")
	}
	m.toggleStatus(fav.StatusDone)
	if r.Status != fav.StatusDoing {
		t.Fatalf("默认是已完成，x 应改成进行中：%s", r.Status)
	}
	m.toggleArchive()
	if !r.Archived() || r.Status != fav.StatusDoing {
		t.Fatalf("归档不动看板状态：%s archived=%v", r.Status, r.Archived())
	}
	m.toggleArchive()
	if r.Archived() || r.Status != fav.StatusDoing {
		t.Fatalf("取消归档也不动看板状态：%s", r.Status)
	}
	m.toggleStatus(fav.StatusDone)
	if !r.Done() {
		t.Fatalf("x 再按回到已完成：%s", r.Status)
	}
	m.toggleArchive()
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
	m.navKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	before := open()
	if before == 0 {
		t.Fatal("z 应全展")
	}
	m.navKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if open() != 0 {
		t.Fatalf("再按 z 应全折，还剩 %d 条可见", open())
	}
	m.navKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	if open() != 0 {
		t.Fatal("- 应全折")
	}
	m.navKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("=")})
	if open() != before {
		t.Fatal("= 应全展")
	}
}

func TestDatePicker(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.Local) // a Monday
	for _, c := range []struct{ in, want string }{
		{"09-01", "after:2026-09-01"},
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

	m := New(fixture(t), noIndex(t), fav.DefaultConfig(), "")
	m.pickDate()
	m.ov.filter.SetValue("9-1..9-15")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	m := New(fixture(t), noIndex(t), fav.DefaultConfig(), "")
	m.search.SetValue("geo #pagination last:7d")
	m.pickTags()
	m.ov.filter.SetValue("x")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if !m.ov.active() || m.ov.filter.Value() != "" {
		t.Fatalf("多选框里 ^U 只清输入、空着退格不关框（勾选不能误丢）：ov=%v %q", m.ov.active(), m.ov.filter.Value())
	}
	m.clearPicker()
	if m.ov.active() || m.search.Value() != "geo last:7d" {
		t.Fatalf("清空按钮应只清掉标签：ov=%v %q", m.ov.active(), m.search.Value())
	}
	m.pickDate()
	m.ov.filter.SetValue("本")
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if !m.ov.active() || m.ov.filter.Value() != "" {
		t.Fatalf("搜索框有字时退格只删字：ov=%v %q", m.ov.active(), m.ov.filter.Value())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.ov.active() || m.search.Value() != "geo" {
		t.Fatalf("空着再退格应清掉时间：ov=%v %q", m.ov.active(), m.search.Value())
	}
}

func TestConventionalKeys(t *testing.T) {
	m := sized(t, 140, 40)
	key := func(k string) { m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}) }
	key("/")
	if !m.typing {
		t.Fatal("列表里 / 应聚焦搜索框")
	}
	before := m.cursor
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.cursor <= before || !m.typing {
		t.Fatalf("搜索框里 ctrl+n 应选下一条且不离开搜索框：cursor=%d typing=%v", m.cursor, m.typing)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	key("l")
	if m.pane != paneChat {
		t.Fatal("l 应把焦点切到右栏")
	}
	key("/")
	if !m.typing || m.chat.typing {
		t.Fatal("/ 在哪都是搜会话，右栏有焦点时也一样")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	key(">")
	if !m.typing || !m.msgMode() {
		t.Fatal("> 打开搜消息")
	}
	m.search.SetValue("")
	m.refresh()
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	key("l")
	key("h")
	m.cursor = 0
	m.clampCursor()
	before = m.cursor
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.cursor <= before {
		t.Fatalf("ctrl+d 应往下走半页：%d", m.cursor)
	}
}

// switching to the Agents page (one chip fewer) must not leave the chip focus out of range; status:live works on the favorites page too
func TestViewSwitchClampsChip(t *testing.T) {
	m := sized(t, 140, 40)
	m.chipFocus = 4
	m.setView(viewLive)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	for i := 0; i < 4; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
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

// wheel up after expanding must reach the first group header
func TestWheelReachesFirstGroup(t *testing.T) {
	m := sized(t, 140, 40)
	m.setView(viewProjects)
	m.move(1)
	m.toggleGroup()
	for i := 0; i < 20; i++ {
		m.wheel(1, 0)
	}
	for i := 0; i < 200; i++ {
		m.wheel(-1, 0)
	}
	m.View()
	if m.cursor != 0 || m.scroll != 0 {
		t.Fatalf("应回到顶上：cursor=%d scroll=%d", m.cursor, m.scroll)
	}
}

func TestStatusPickerAndTrash(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 140, 40)
	m.pickStatus()
	if !m.ov.active() || m.ov.visible()[m.ov.cursor].name != fav.StatusOpen {
		t.Fatalf("选择器应打开并停在当前值：%+v", m.ov.cursor)
	}
	m.ov.filter.SetValue("回收")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.search.Value() != "status:trash" || !m.inTrash() {
		t.Fatalf("选回收站应写进查询串：%q", m.search.Value())
	}
	if len(m.rows) != 0 {
		t.Fatalf("回收站应是空的：%d", len(m.rows))
	}
	m.search.SetValue("")
	m.refresh()
	r := m.current()
	if r == nil {
		t.Fatal("要有一条当前记录")
	}
	transcript := filepath.Join(t.TempDir(), r.SessionID+".jsonl")
	os.WriteFile(transcript, []byte("{}\n"), 0o644)
	r.PinnedPath = transcript
	m.store.Put(r)
	key := func(k string) { m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}) }
	key("D")
	if m.ov.kind != ovConfirm {
		t.Fatal("D 应先确认")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.ov.active() || m.store.Get(r.ID) == nil {
		t.Fatal("Esc 应取消，不删")
	}
	key("D")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.store.Get(r.ID) != nil {
		t.Fatal("确认后记录应墓碑")
	}
	if _, err := os.Stat(transcript); !os.IsNotExist(err) {
		t.Fatal("钉住的文件应已挪走")
	}
	m.search.SetValue("status:trash")
	m.refresh()
	if m.countRecs() != 1 || m.current() == nil || m.current().SessionID != r.SessionID {
		t.Fatalf("回收站应列出刚删的：%d", m.countRecs())
	}
	key("f")
	if m.store.Get(r.ID) != nil {
		t.Fatal("回收站里 f 不该复活记录")
	}
	key("D")
	if got := m.store.Get(r.ID); got == nil || got.Deleted {
		t.Fatal("还原后记录应回来")
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Fatal("文件应回到原处")
	}
	if len(m.rows) != 0 {
		t.Fatalf("还原后回收站应空：%d", len(m.rows))
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
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "项目") || !strings.Contains(v, "最近会话") || !strings.Contains(v, "个会话") {
		t.Fatalf("右栏应显示项目信息\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if !m.open[g] || m.rows[m.cursor+1].rec == nil {
		t.Fatal("→ 应展开分组")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.current() == nil {
		t.Fatal("↓ 应停到子项")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.open[g] || m.rows[m.cursor].group != g {
		t.Fatalf("子项上 ← 应折叠并回到分组标题：open=%v row=%+v", m.open[g], m.rows[m.cursor])
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.pane != paneList || m.rows[m.cursor].group != g {
		t.Fatal("折着的分组上再按 ← 什么都不做")
	}
}

func TestProjectClicks(t *testing.T) {
	m := sized(t, 140, 40)
	m.setView(viewProjects)
	m.refresh()
	first := m.rows[0].group
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor == 0 {
		t.Fatal("要有第二个分组")
	}
	m.View()
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
	m.View()
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
	right := tea.KeyMsg{Type: tea.KeyRight}
	m.Update(right)
	if !m.open[g] || m.pane != paneList {
		t.Fatal("第一下 → 展开")
	}
	m.Update(right)
	if !m.projectFocus() {
		t.Fatal("第二下 → 焦点到右栏")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pane != paneList || m.current() != m.groups[g][1] {
		t.Fatalf("↓ Enter 应跳到分组里第二条：%v", m.current())
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, m.groups[g][1].Title[:6]) {
		t.Fatal("跳过去后右栏应是那条会话")
	}
}

func TestMoveProjectFromTUI(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("FAV_HOME", filepath.Join(root, "fav"))
	old, dst := filepath.Join(root, "work", "proj"), filepath.Join(root, "dev", "proj")
	os.MkdirAll(dst, 0o755)
	pdir := index.ClaudeProjectDir(old)
	os.MkdirAll(pdir, 0o755)
	line := func(i int, cwd string) string {
		return `{"type":"user","timestamp":"2026-09-10T01:00:0` + strconv.Itoa(i) + `Z","cwd":"` + cwd + `","message":{"content":"提示 ` + strconv.Itoa(i) + ` 做点什么事情"}}` + "\n"
	}
	os.WriteFile(filepath.Join(pdir, "s1.jsonl"), []byte(line(0, old)+line(1, old)+line(2, old)), 0o644)
	os.WriteFile(filepath.Join(pdir, "s2.jsonl"), []byte(line(0, old)+line(1, old)+line(2, old)), 0o644)
	idx, _ := index.OpenAt(filepath.Join(root, "sessions.jsonl"))
	idx, _ = idx.Refresh()
	s, _ := fav.OpenAt(filepath.Join(root, "records.jsonl"))
	m := New(s, idx, fav.DefaultConfig(), "")
	m.w, m.h = 140, 40
	m.setView(viewProjects)
	m.refresh()
	if m.current() != nil || m.groupUnderCursor() != "proj" {
		t.Fatalf("应停在 proj 分组上：%+v", m.rows[:1])
	}
	key := func(k string) { m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}) }

	m.live = map[string]capture.Live{"s2": {}}
	key("M")
	if m.ov.active() || !strings.Contains(m.notice, "在跑") {
		t.Fatalf("有在跑的会话应拒绝：ov=%v notice=%q", m.ov.active(), m.notice)
	}

	m.live = nil
	key("M")
	if m.ov.kind != ovPicker || m.ov.browse == nil || m.ov.filter.Value() != dst {
		t.Fatalf("旧目录不在了、唯一猜到去向，应预填它：%+v", m.ov.filter.Value())
	}
	m.ov.filter.SetValue(filepath.Join(root, "dev"))
	vis := m.ov.visible()
	if len(vis) < 2 || vis[1].name != dst {
		t.Fatalf("列出 dev 下的子目录：%+v", vis)
	}
	m.ov.cursor = 1
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.kind != ovPicker || m.ov.filter.Value() != dst+string(filepath.Separator) || m.ov.cursor != 0 {
		t.Fatalf("子目录上 Enter 应进入而不是选定：%q cur=%d", m.ov.filter.Value(), m.ov.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.kind != ovConfirm || !strings.Contains(strings.Join(m.ov.lines, "\n"), "2 个会话（Claude 2 · Codex 0）") {
		t.Fatalf("「就是这个目录」上 Enter 应弹确认框：kind=%d %v", m.ov.kind, m.ov.lines)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.kind != ovPicker || m.ov.browse == nil || m.ov.filter.Value() != dst+string(filepath.Separator) || m.notice != "" {
		t.Fatalf("确认框默认焦点在取消，Enter 应回到目录选择器：kind=%d %q notice=%q", m.ov.kind, m.ov.filter.Value(), m.notice)
	}
	key("M") // M inside the picker = move to the listed directory
	key("y")
	if m.ov.active() || !strings.Contains(m.notice, "已移动 2") {
		t.Fatalf("y 应搬完：notice=%q", m.notice)
	}
	if _, err := os.Stat(filepath.Join(index.ClaudeProjectDir(dst), "s1.jsonl")); err != nil {
		t.Fatal("会话文件应到新项目目录")
	}
	if _, err := os.Stat(pdir); !os.IsNotExist(err) {
		t.Fatal("旧项目目录应清掉")
	}
	m.refresh()
	found := false
	for _, r := range m.unfav {
		found = found || r.Cwd == dst
	}
	if !found {
		t.Fatal("索引应立刻认到新目录")
	}
}

func TestDirPickerClickPlacesCursor(t *testing.T) {
	m := sized(t, 140, 40)
	m.openDirPicker("t", "/Users/x/dev/", func(*Model, string) {})
	m.ov.focus = 0
	m.View()
	var z *zone
	for i := range m.zones {
		if m.zones[i].x2-m.zones[i].x1 > 40 {
			z = &m.zones[i]
			break
		}
	}
	if z == nil {
		t.Fatalf("输入框应登记点击区：%+v", m.zones)
	}
	x := z.x1 + 2 + 2 + len("/Users/x/") // border+padding, prompt 2 columns: the click lands on the d of dev
	m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: x, Y: z.y})
	if m.ov.filter.Position() != len("/Users/x/") || m.ov.focus != -1 {
		t.Fatalf("点哪光标落哪、焦点回列表：pos=%d focus=%d", m.ov.filter.Position(), m.ov.focus)
	}
}

func TestSearchBarClickPlacesCursor(t *testing.T) {
	m := sized(t, 140, 40)
	m.search.SetValue("webapp rbac")
	m.View()
	x := 2 + ansi.StringWidth(m.search.Prompt) + len("webapp ")
	m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: x, Y: 1})
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
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if m.ov.kind != ovResume || m.quitting {
		t.Fatalf("Space 不该直接恢复，应停在恢复框：kind=%d quit=%v", m.ov.kind, m.quitting)
	}
	m.View()
	if v := ansi.Strip(m.View()); strings.Contains(v, "Enter 恢复") || !strings.Contains(v, "M 移动") || !strings.Contains(v, "D 删除") {
		t.Fatalf("恢复框应只给移动 / 删除：\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.kind != ovPicker || m.ov.browse == nil {
		t.Fatalf("目录没了时 Enter 应进移动流程：kind=%d", m.ov.kind)
	}
}

// every level in must pair with a level out: → right pane, → full text, ← / Esc out; clicking the list refocuses it; ← also leaves the narrow detail
func TestDrillInAndBack(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	m.probes = map[*fav.Rec]*probe{r: {done: true, msgs: []capture.Message{{Role: "user", Text: "一句话"}}}}
	key := func(k tea.KeyType) { m.Update(tea.KeyMsg{Type: k}) }
	key(tea.KeyRight)
	if m.pane != paneChat {
		t.Fatal("→ 应进右栏")
	}
	key(tea.KeyRight)
	if m.ov.kind != ovMessage {
		t.Fatal("右栏里 → 应打开整句全文")
	}
	key(tea.KeyEsc)
	if m.ov.active() || m.pane != paneChat {
		t.Fatal("Esc 只退一层：回到右栏")
	}
	key(tea.KeyLeft)
	if m.pane != paneList {
		t.Fatal("← 应回左栏")
	}
	key(tea.KeyRight)
	m.clickRow(1)
	if m.pane != paneList || m.cursor != 1 {
		t.Fatalf("点左栏焦点应回左栏：pane=%d cur=%d", m.pane, m.cursor)
	}

	m.w = 80 // narrow: Enter opens the detail, ← and Esc both leave
	key(tea.KeyEnter)
	if !m.detail {
		t.Fatal("窄屏 Enter 应进详情")
	}
	key(tea.KeyLeft)
	if m.detail {
		t.Fatal("窄屏详情 ← 应退回列表")
	}
}
