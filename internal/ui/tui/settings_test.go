package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/render"
)

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

func TestOpenInIDE(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 120, 40)
	if m.ideName() != capture.DefaultIDE() {
		t.Fatalf("默认 IDE 应是 %s", capture.DefaultIDE())
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
	if m.ov.editing || m.cfg.IDE != capture.DefaultIDE()+"/no/such/ide" && m.cfg.IDE != "/no/such/ide" || fav.LoadConfig().IDE != m.cfg.IDE {
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
