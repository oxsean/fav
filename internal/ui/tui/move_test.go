package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/testkit"
)

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
		return `{"type":"user","timestamp":"2026-09-10T01:00:0` + strconv.Itoa(i) + `Z","cwd":` + testkit.JSONString(cwd) + `,"message":{"content":"提示 ` + strconv.Itoa(i) + ` 做点什么事情"}}` + "\n"
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
	key := func(k string) { m.Update(press(k)) }

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
	m.Update(press("enter"))
	if m.ov.kind != ovPicker || m.ov.filter.Value() != dst+string(filepath.Separator) || m.ov.cursor != 0 {
		t.Fatalf("子目录上 Enter 应进入而不是选定：%q cur=%d", m.ov.filter.Value(), m.ov.cursor)
	}
	m.Update(press("enter"))
	if m.ov.kind != ovConfirm || !strings.Contains(strings.Join(m.ov.lines, "\n"), "2 个会话（Claude 2 · Codex 0）") {
		t.Fatalf("「就是这个目录」上 Enter 应弹确认框：kind=%d %v", m.ov.kind, m.ov.lines)
	}
	m.Update(press("enter"))
	if m.ov.kind != ovPicker || m.ov.browse == nil || m.ov.filter.Value() != dst+string(filepath.Separator) || m.notice != "" {
		t.Fatalf("确认框默认焦点在取消，Enter 应回到目录选择器：kind=%d %q notice=%q", m.ov.kind, m.ov.filter.Value(), m.notice)
	}
	key("M")
	if m.ov.filter.Value() != dst+string(filepath.Separator)+"M" {
		t.Fatalf("M types into the path: %q", m.ov.filter.Value())
	}
	m.ov.filter.SetValue(dst + string(filepath.Separator))
	m.ov.cursor = 0
	m.Update(press("enter")) // the first row = this directory
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
	m.screen()
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
	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: z.y})
	if m.ov.filter.Position() != len("/Users/x/") || m.ov.focus != -1 {
		t.Fatalf("点哪光标落哪、焦点回列表：pos=%d focus=%d", m.ov.filter.Position(), m.ov.focus)
	}
}
