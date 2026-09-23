package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/index"
)

func TestFrameLinesFillWidth(t *testing.T) {
	st := demoStore(t)
	for _, size := range []struct{ w, h int }{{120, 34}, {80, 24}, {56, 20}} {
		for _, ov := range []string{"", "picker", "help", "resume", "resume-edit"} {
			m := New(st, noIndex(t), fav.DefaultConfig(), "")
			m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			openOverlay(m, ov)
			for i, line := range strings.Split(m.View(), "\n") {
				if got := ansi.StringWidth(line); got != size.w {
					t.Errorf("%dx%d ov=%q 第 %d 行宽 %d：%q",
						size.w, size.h, ov, i+1, got, ansi.Strip(line))
				}
			}
		}
	}
}

func openOverlay(m *Model, kind string) {
	switch kind {
	case "picker":
		m.pickTags()
	case "help":
		m.ov = overlay{kind: ovHelp}
	case "resume":
		m.askResume()
	case "resume-edit":
		m.askResume()
		m.editTitle()
	case "settings":
		m.openSettings()
	}
}

func TestClickZones(t *testing.T) {
	m := New(demoStore(t), noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m.View()

	x, y := findText(m.View(), "项目")
	if x < 0 {
		t.Fatal("画面上找不到「项目」标签页")
	}
	click(m, x, y)
	if m.view != viewProjects {
		t.Errorf("点「项目」标签页后视图仍是 %v", m.view)
	}

	m.setView(viewSessions)
	m.View()
	first := m.current()
	x, y = findText(m.View(), "WebApp 分支栈")
	if x < 0 {
		t.Fatal("画面上找不到第二张卡片")
	}
	click(m, x, y)
	if m.current() == nil || m.current() == first {
		t.Errorf("点第二张卡片后选中项没变")
	}

	m.View()
	x, y = findText(m.View(), "标签 全部")
	click(m, x, y)
	if m.ov.kind != ovPicker {
		t.Errorf("点标签 chip 没有打开选择器，kind=%v", m.ov.kind)
	}
}

func click(m *Model, x, y int) {
	m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

func findText(s, want string) (int, int) {
	for y, line := range strings.Split(s, "\n") {
		plain := ansi.Strip(line)
		if i := strings.Index(plain, want); i >= 0 {
			return ansi.StringWidth(plain[:i]), y
		}
	}
	return -1, -1
}

// FAV_DUMP=120x34 go test ./internal/ui/tui -run TestDumpFrame -v prints a real frame.
func TestDumpFrame(t *testing.T) {
	spec := os.Getenv("FAV_DUMP")
	if spec == "" {
		t.Skip("设置 FAV_DUMP=WxH 查看画面")
	}
	var w, h int
	if _, err := fmt.Sscanf(spec, "%dx%d", &w, &h); err != nil {
		t.Fatalf("FAV_DUMP 应形如 120x34：%v", err)
	}
	// lipgloss drops colours without a TTY
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := New(demoStore(t), noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	if os.Getenv("FAV_DUMP_VIEW") == "real" {
		idx, _ := index.Open()
		m.Update(m.pollLive()())
		m.applyIndex(idx)
	}
	openOverlay(m, os.Getenv("FAV_DUMP_OV"))
	fmt.Println(m.View())
}

func demoStore(t *testing.T) *fav.Store {
	t.Helper()
	st, err := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct {
		title, summary, project, work string
		tags                          []string
		provider, branch, ws          string
		done                          bool
	}{
		{"notes-api WebSocket 断线重连排障", "弱网下心跳超时后不重连，定位到 backoff 计时器被 close 事件重置。", "notes-api", "排障", []string{"notes-api", "debug", "websocket"}, fav.ProviderClaude, "fix/ws-reconnect", "notes-api", false},
		{"WebApp 分支栈 rebase 自动化", "把手工 rebase 流程收敛成 make 目标，处理 worktree 隔离与冲突恢复。", "webapp", "重构", []string{"webapp", "git", "tooling"}, fav.ProviderCodex, "feat/stack-rebase", "webapp", false},
		{"Fav Session Manager 实现", "去掉 SQLite 改 JSONL，FZF 与原生 TUI 双前端。", "fav", "实现", []string{"fav", "golang", "tui"}, fav.ProviderClaude, "main", "dev", false},
		{"标签合并规则重写", "合并优先级与生效时间窗的边界条件梳理，补了 14 条表驱动测试。", "notes-api", "实现", []string{"notes-api", "tags"}, fav.ProviderClaude, "feat/geo-override", "", true},
		{"Codex rollout 文件反查会话 id", "没有环境变量可用，只能按 mtime + cwd 从首行反查，多命中时报错不猜。", "fav", "调研", []string{"fav", "codex"}, fav.ProviderCodex, "main", "", true},
	} {
		r := &fav.Rec{
			ID: fav.NewID(), Provider: d.provider, SessionID: d.title,
			Title: d.title, Summary: d.summary, Project: d.project, WorkType: d.work,
			Tags: d.tags, GitBranch: d.branch, HerdrWorkspace: d.ws,
			Cwd: "/Users/me/work/" + d.project, Status: fav.StatusDone,
		}
		if d.done {
			r.Status = fav.StatusDone
		}
		r.Tags = fav.Normalize(r.Tags)
		r.FavoritedAt = ptr(time.Now().Add(-time.Duration(len(d.title)) * time.Hour))
		if err := st.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestOverlayBackdropIsDarkerThanFrame(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	back := lum(lipgloss.NewStyle().Foreground(cBackdrop).Render("x"))
	for name, c := range map[string]lipgloss.AdaptiveColor{"cMuted": cMuted, "cText": cText, "cAccent": cAccent} {
		if got := lum(lipgloss.NewStyle().Foreground(c).Render("x")); back >= got {
			t.Errorf("底图色亮度 %d 不低于 %s 的 %d", back, name, got)
		}
	}
	if got := lum(lipgloss.NewStyle().Foreground(cFrame).Render("x")); back > got+24 {
		t.Errorf("底图色亮度 %d 明显高于框线色 %d，开浮层时边框会变亮", back, got)
	}
}

func lum(s string) int {
	m := regexp.MustCompile(`38;2;(\d+);(\d+);(\d+)`).FindStringSubmatch(s)
	if m == nil {
		return -1
	}
	n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
	return (n(1)*299 + n(2)*587 + n(3)*114) / 1000
}

func TestResumeDialogEditsTitle(t *testing.T) {
	st := demoStore(t)
	m := New(st, noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m.askResume()
	rec := m.ov.rec
	old := rec.Title
	rec.HerdrWorkspace, rec.Cwd = "", t.TempDir()
	rec.TranscriptPath = filepath.Join(rec.Cwd, "s.jsonl")
	os.WriteFile(rec.TranscriptPath, []byte("{}\n"), 0o644)
	m.ov.plan, _ = capture.PlanResume(rec, nil, false)

	m.View()
	x, y := findText(m.View(), "n 改标题")
	if x < 0 {
		t.Fatal("恢复框里没有改标题的入口")
	}
	click(m, x, y)
	if !m.ov.editing {
		t.Fatal("点了标题行没有进入编辑")
	}

	m.ov.edit.SetValue("改过的标题")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.editing {
		t.Fatal("Enter 没有结束编辑")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if !m.quitting || m.Result().Resume == nil {
		t.Fatal("第二次 Enter 没有触发恢复")
	}
	if rec.Title != "改过的标题" {
		t.Errorf("记录标题还是 %q", rec.Title)
	}
	reloaded, err := fav.OpenAt(st.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reloaded.All() {
		if r.ID == rec.ID && r.Title != "改过的标题" {
			t.Errorf("盘上还是旧标题 %q（原 %q）", r.Title, old)
		}
	}
}

func TestSessionsViewFavorites(t *testing.T) {
	st := demoStore(t)
	m := New(st, noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	rec := &fav.Rec{Provider: fav.ProviderClaude, SessionID: "sess-x", Title: "第一句话",
		Cwd: t.TempDir(), FavoritedAt: ptr(time.Now()), Status: fav.StatusDone}
	rec.Attach(5, 10, time.Now(), "第一句话\n再来一句\n")
	short := &fav.Rec{Provider: fav.ProviderClaude, SessionID: "sess-short", Title: "hi",
		Cwd: t.TempDir(), FavoritedAt: ptr(time.Now()), Status: fav.StatusDone}
	short.Attach(1, 2, time.Now(), "hi\n")
	m.unfav = []*fav.Rec{rec, short}
	m.setView(viewSessions)
	if m.current() != rec {
		t.Fatalf("最新的未收藏会话应排在最前：%+v", m.current())
	}
	base := len(st.Query(fav.Parse("")))
	if n := m.countRecs(); n != base+1 {
		t.Fatalf("1 轮的会话默认应藏起来：列了 %d 条", n)
	}
	m.search.SetValue("turns:1")
	m.refresh()
	if n := m.countRecs(); n != base+2 {
		t.Fatalf("turns:1 应把短会话也列出来：%d 条", n)
	}
	m.search.SetValue("再来一句")
	m.refresh()
	if m.countRecs() != 1 || m.current() != rec {
		t.Fatal("应能按索引里的提示语搜到未收藏会话")
	}
	m.search.SetValue("")
	codex := &fav.Rec{Provider: fav.ProviderCodex, SessionID: "sess-cx", Title: "codex 的会话", Cwd: t.TempDir(), FavoritedAt: ptr(time.Now().Add(-time.Hour)), Status: fav.StatusDone}
	codex.Attach(5, 10, time.Now().Add(-time.Hour), "")
	m.unfav = append(m.unfav, codex)
	m.setView(viewSessions)
	var seen []string
	for i := 0; i < 3; i++ {
		m.cycleProvider()
		seen = append(seen, m.search.Value())
	}
	if !strings.Contains(strings.Join(seen, ","), "provider:"+fav.ProviderCodex) || seen[2] != "" {
		t.Fatalf("来源轮换应经过没收藏的 Codex 会话再回到全部：%v", seen)
	}
	m.setView(viewFavorites)
	for _, r := range m.rows {
		if r.rec == rec {
			t.Fatal("「收藏」视图不该出现未收藏的会话")
		}
	}
	m.setView(viewSessions)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if rec.ID == "" || st.BySession(fav.ProviderClaude, "sess-x") == nil {
		t.Fatal("按 f 后没有落库")
	}
	for _, r := range m.unfav {
		if r == rec {
			t.Errorf("落库后仍留在未收藏那批里")
		}
	}
}

func TestArrowNavigation(t *testing.T) {
	m := New(demoStore(t), noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	key := func(s string) {
		if len(s) == 1 {
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
			return
		}
		m.Update(tea.KeyMsg{Type: map[string]tea.KeyType{"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "enter": tea.KeyEnter, "esc": tea.KeyEsc}[s]})
	}
	key("up")
	if m.chipFocus != -1 {
		t.Fatalf("列表顶上再往上应停住、不进 chip 行，chipFocus=%d", m.chipFocus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(";")})
	if m.chipFocus != 0 {
		t.Fatalf("; 应进 chip 行，chipFocus=%d", m.chipFocus)
	}
	key("right")
	key("enter")
	if m.ov.kind != ovPicker || m.ov.title != "筛选标签" {
		t.Fatalf("chip 行第二个是标签，Enter 应打开标签选择器：kind=%v title=%q", m.ov.kind, m.ov.title)
	}
	m.View()
	key("right")
	if m.ov.focus != 1 {
		t.Fatalf("第一下方向键应从主按钮出发，focus=%d", m.ov.focus)
	}
	key("right")
	key("enter")
	if m.ov.active() {
		t.Fatal("焦点在「取消」上按 Enter 应关闭选择器")
	}
	key("down")
	if m.chipFocus != -1 {
		t.Fatalf("chip 行往下应回列表，chipFocus=%d", m.chipFocus)
	}
	key("right")
	if m.pane != paneChat {
		t.Fatalf("列表里 → 应把焦点切到右栏，pane=%v", m.pane)
	}
	key("left")
	if m.pane != paneList {
		t.Fatalf("← 应回到列表，pane=%v", m.pane)
	}
}

func TestFavoriteToggle(t *testing.T) {
	st := demoStore(t)
	m := New(st, noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m.setView(viewSessions)
	first := m.current()
	if first == nil || !first.Favorite() {
		t.Fatal("demo 库的第一条应是收藏")
	}
	title, nFav := first.Title, m.nFav
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if first.Favorite() || m.current() != first || m.nFav != nFav-1 {
		t.Fatalf("f 应取消收藏、记录留在原地、收藏总数减一：fav=%v cur==first=%v nFav=%d", first.Favorite(), m.current() == first, m.nFav)
	}
	if !strings.Contains(ansi.Strip(m.View()), "未收藏 · f 收藏") {
		t.Fatal("取消后详情应提示可再收藏")
	}
	m.setView(viewFavorites)
	for _, r := range m.rows {
		if r.rec == first {
			t.Fatal("取消过收藏的不该出现在「收藏」页")
		}
	}
	m.setView(viewSessions)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	got := st.BySession(first.Provider, first.SessionID)
	if got == nil || !got.Favorite() || got.Title != title || got.ID != first.ID || m.nFav != nFav {
		t.Fatalf("再按 f 应原样恢复原记录：%+v", got)
	}
}

func TestStateOnPlainSession(t *testing.T) {
	st := demoStore(t)
	m := New(st, noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.setView(viewSessions)
	sess := &fav.Rec{Provider: "claude", SessionID: "plain-1", Title: "没收藏的会话", Cwd: "/tmp", Status: fav.StatusDone}
	sess.Attach(9, 20, time.Now(), "")
	m.unfav = append(m.unfav, sess)
	m.recount()
	m.refresh()
	nAll := m.nAll
	for i, r := range m.rows {
		if r.rec == sess {
			m.cursor = i
		}
	}
	key := func(k string) { m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}) }
	key("a")
	got := st.BySession("claude", "plain-1")
	if got != sess || !got.Archived() || got.Favorite() || len(m.unfav) != 0 || m.nAll != nAll {
		t.Fatalf("a 应把普通会话入库并归档、不算收藏、总数不变：%+v unfav=%d nAll=%d", got, len(m.unfav), m.nAll)
	}
	if m.current() != sess {
		t.Fatal("刚归档的应还在光标下")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.refresh()
	for _, r := range m.rows {
		if r.rec == sess {
			t.Fatal("光标离开后默认筛选里不该再有它")
		}
	}
	key("s")
	m.ov.filter.SetValue("archived")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.search.Value(), "status:archived") {
		t.Fatalf("s 选已归档：%q", m.search.Value())
	}
	found := false
	for i, r := range m.rows {
		if r.rec == sess {
			found, m.cursor = true, i
		}
	}
	if !found {
		t.Fatal("status:archived 下应看到它")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "已归档") || !strings.Contains(v, "a 取消归档") {
		t.Fatal("卡片和底栏应标出已归档 / a 取消归档")
	}
	key("a")
	if sess.Archived() {
		t.Fatal("再按 a 应取消归档")
	}
	key("s")
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.search.Value() != "" {
		t.Fatalf("选择器里退格清空应去掉 status:：%q", m.search.Value())
	}
	key("a")
	m.setView(viewFavorites)
	for _, r := range m.rows {
		if r.rec == sess {
			t.Fatal("只标过状态的会话不该出现在「收藏」页")
		}
	}
}

func TestCursorFollowsRecordOnRefresh(t *testing.T) {
	m := New(demoStore(t), noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.move(1)
	m.move(1)
	cur := m.current()
	cur.Attach(cur.Turns, cur.Msgs, time.Now().Add(time.Hour), "")
	m.refresh()
	if m.current() != cur || m.cursor != 1 {
		t.Fatalf("重排后光标应还在同一条上：cursor=%d same=%v", m.cursor, m.current() == cur)
	}
}
