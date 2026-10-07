package tui

import (
	"cmp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/skin"
	"github.com/oxsean/fav/internal/tend"
)

func TestSettingsPanel(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	defer func() { render.RelativeTime = true; tend.DefaultTurns = 3 }()
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
	if m.cfg.MinTurns != 5 || tend.DefaultTurns != 5 {
		t.Fatalf("阈值没生效：cfg=%d global=%d", m.cfg.MinTurns, tend.DefaultTurns)
	}
	if got := tend.LoadConfig(); got.RelativeTime || got.MinTurns != 5 {
		t.Fatalf("没落盘：%+v", got)
	}
	m.Update(press("esc"))
	if m.ov.active() {
		t.Fatal("Esc 应关闭设置面板")
	}
}

func TestOpenInIDE(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	m := sized(t, 120, 40)
	if m.ideName() != capture.DefaultIDE() {
		t.Fatalf("默认 IDE 应是 %s", capture.DefaultIDE())
	}
	m.Update(press(","))
	for i, s := range settingsTable() {
		if s.text != nil && s.label == "IDE" {
			m.ov.cursor = i
		}
	}
	m.Update(press("enter"))
	if !m.ov.editing {
		t.Fatal("Enter 应进编辑")
	}
	m.Update(press("/no/such/ide"))
	m.Update(press("enter"))
	if m.ov.editing || m.cfg.IDE != capture.DefaultIDE()+"/no/such/ide" && m.cfg.IDE != "/no/such/ide" || tend.LoadConfig().IDE != m.cfg.IDE {
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
	if err := capture.OpenDir("code", "/definitely/not/here"); err == nil || !strings.Contains(err.Error(), "目录不存在") {
		t.Fatalf("目录不存在应直接报：%v", err)
	}
}

func TestSkinAccentAndContrastApplyAtOnce(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	defer func() { setTheme(true); useSkin(tend.Config{}) }()
	m := sized(t, 120, 40)
	m.Update(press(","))
	find := func(label string) int {
		for i, s := range settingsTable() {
			if s.label == label {
				return i
			}
		}
		t.Fatalf("no %s", label)
		return 0
	}
	before := curSkin.Dark["accent"]
	m.ov.cursor = find(i18n.T("settings.skin"))
	m.Update(press("right"))
	if m.cfg.Skin != "forest" || curSkin.Dark["accent"] == before || tend.LoadConfig().Skin != "forest" {
		t.Fatalf("the next skin at once: %q %s", m.cfg.Skin, curSkin.Dark["accent"])
	}
	m.ov.cursor = find(i18n.T("settings.accent"))
	for _, c := range []struct{ typed, want string }{{"#C03030", "#c03030"}, {"red", ""}} {
		m.Update(press("enter"))
		m.ov.edit.SetValue(c.typed)
		m.Update(press("enter"))
		if m.cfg.Accent != c.want || curSkin.Input.Accent != cmp.Or(c.want, skin.Presets[1].Input.Accent) {
			t.Fatalf("%q: accent %q, skin %s", c.typed, m.cfg.Accent, curSkin.Input.Accent)
		}
	}
	m.ov.cursor = find(i18n.T("settings.contrast"))
	m.Update(press("right"))
	if !m.cfg.HighContrast || !curSkin.Input.High {
		t.Fatal("high contrast at once")
	}
}
