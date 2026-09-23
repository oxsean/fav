package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
)

func TestResumeCommand(t *testing.T) {
	m := sized(t, 120, 40)
	m.askResume()
	cmd := m.resumeCommand()
	r := m.ov.rec
	if i, j := strings.Index(cmd, r.Cwd), strings.Index(cmd, "claude --resume "+r.SessionID); i < 0 || j < i {
		t.Fatalf("恢复命令不对：%q", cmd)
	}
}

func TestResumeDialogOffersTheDesktopApp(t *testing.T) {
	m := sized(t, 140, 40)
	capture.SetAppAvailable(fav.ProviderClaude, true)
	capture.SetAppAvailable(fav.ProviderCodex, true)
	r := m.current()
	r.SessionID, r.Provider = "c5126b86-64bb-46a8-9a69-fc421c8f4f9a", fav.ProviderClaude
	appFiles(t, r)
	m.cfg.ResumeIn = fav.ResumeOrigin

	r.App = false
	m.askResume()
	if m.ov.app || !strings.Contains(ansi.Strip(m.screen()), "Claude") {
		t.Fatal("a terminal session: the app is offered, not first")
	}
	m.closeOverlay()

	r.App = true
	m.askResume()
	if !m.ov.app {
		t.Fatal("started in the app, the setting follows the origin: the app comes first")
	}
	for l := range strings.SplitSeq(m.screen(), "\n") {
		if w := ansi.StringWidth(l); w > 140 {
			t.Fatalf("line wider than the terminal (%d): %q", w, ansi.Strip(l))
		}
	}
	_, cmd := m.Update(press("enter"))
	if m.ov.active() || cmd == nil || !strings.Contains(m.notice, "Claude") {
		t.Fatalf("Enter hands it to the app: %q", m.notice)
	}
}

func TestAppButtonAppearsWhenTheLookupAnswers(t *testing.T) {
	m := sized(t, 140, 40)
	capture.ForgetAppAvailable(fav.ProviderClaude)
	t.Cleanup(func() { capture.ForgetAppAvailable(fav.ProviderClaude) })
	r := m.current()
	r.SessionID, r.Provider = "c5126b86-64bb-46a8-9a69-fc421c8f4f9a", fav.ProviderClaude
	appFiles(t, r)

	m.askResume()
	if m.pending == nil {
		t.Fatal("the lookup starts in the background")
	}
	btn := i18n.F("resume.btn_app", "Claude")
	if strings.Contains(ansi.Strip(m.screen()), btn) {
		t.Fatal("no button yet")
	}
	m.pending = nil
	capture.SetAppAvailable(fav.ProviderClaude, true)
	m.Update(appProbedMsg{})
	if !strings.Contains(ansi.Strip(m.screen()), btn) {
		t.Fatal("the button appears once the app is found")
	}
	m.closeOverlay()
	m.askResume()
	if m.pending != nil {
		t.Fatal("a known answer is not looked up again")
	}
}

// appFiles puts r's transcript where the desktop apps read it, under a temporary HOME, with an existing cwd.
func appFiles(t *testing.T, r *fav.Rec) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".claude", "projects", "-p")
	if r.Provider == fav.ProviderCodex {
		dir = filepath.Join(home, ".codex", "sessions")
	}
	os.MkdirAll(dir, 0o755)
	r.TranscriptPath, r.Cwd = filepath.Join(dir, r.SessionID+".jsonl"), home
	os.WriteFile(r.TranscriptPath, []byte("{}\n"), 0o644)
}

func TestResumeDialogOpensTheFileManager(t *testing.T) {
	m := sized(t, 140, 40)
	m.askResume()
	if !strings.Contains(ansi.Strip(m.screen()), "o "+capture.FileManagerName()) {
		t.Fatal("the project row offers the file manager")
	}
}

func TestAppKeyIsP(t *testing.T) {
	m := sized(t, 140, 40)
	capture.SetAppAvailable(fav.ProviderClaude, true)
	r := m.current()
	r.SessionID, r.Provider = "c5126b86-64bb-46a8-9a69-fc421c8f4f9a", fav.ProviderClaude
	appFiles(t, r)
	m.askResume()
	if !strings.Contains(ansi.Strip(m.screen()), "p Claude App") {
		t.Fatal("the app button is labelled p")
	}
	m.Update(press("p"))
	if !m.ov.active() || !strings.HasPrefix(m.ov.btns[m.ov.focus].label, "p ") {
		t.Fatal("the first p focuses the app button")
	}
	_, cmd := m.Update(press("p"))
	if m.ov.active() || cmd == nil {
		t.Fatal("p again hands the session to the app")
	}
}

func TestButtonKeysStandOut(t *testing.T) {
	for l, key := range map[string]string{"f 收藏": "f", "Enter 恢复": "Enter", "Esc 取消": "Esc", "Ctrl+S 保存": "Ctrl+S", "p ChatGPT App": "p", "j/k · Esc close": "j/k"} {
		if k, _, ok := labelKey(l); !ok || k != key {
			t.Errorf("%q: key %q, want %q", l, k, key)
		}
	}
	for _, l := range []string{"Confirm", "Open in App", "确认 删除"} {
		if _, _, ok := labelKey(l); ok || keyedLabel(l) != l {
			t.Errorf("%q has no key: unchanged", l)
		}
	}
}

func TestResumeDialogEditKeys(t *testing.T) {
	m := sized(t, 140, 40)
	m.askResume()
	m.Update(press("n"))
	if m.ov.kind != ovResume || !m.ov.editing {
		t.Fatal("n edits the title in place")
	}
	m.closeOverlay()
	m.askResume()
	m.Update(press("e"))
	if m.ov.kind != ovEdit {
		t.Fatal("e opens the full editor, like the e button")
	}
}

func TestNoCopyButtonWithoutACommand(t *testing.T) {
	m := sized(t, 140, 40)
	m.askResume()
	if !strings.Contains(ansi.Strip(m.screen()), i18n.T("resume.btn_copy")) {
		t.Fatal("a resumable session offers the command")
	}
	m.ov.plan.Spec = capture.CommandSpec{}
	if strings.Contains(ansi.Strip(m.screen()), i18n.T("resume.btn_copy")) {
		t.Fatal("nothing to copy (a session running in Herdr): no copy button")
	}
}

func TestResumeAsksWhichWorkspace(t *testing.T) {
	m := sized(t, 140, 40)
	appFiles(t, m.current())
	m.askResume()
	m.ov.plan.Ws = nil
	m.ov.plan.WsChoices = []herdr.Workspace{{WorkspaceID: "a", Label: "api"}, {WorkspaceID: "b", Label: "web"}}
	m.doResume(false)
	if m.ov.kind != ovPicker || len(m.ov.items) != 3 {
		t.Fatalf("two workspaces fit: the user picks (or this terminal): kind=%v items=%d", m.ov.kind, len(m.ov.items))
	}
	m.ov.cursor = 1
	_, cmd := m.Update(press("enter"))
	if cmd == nil || !strings.Contains(m.notice, "web") || m.quitting {
		t.Fatalf("picking web opens the tab there: notice=%q", m.notice)
	}
}

func TestResumeRefusedWhileRunningElsewhere(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	r.Cwd = t.TempDir()
	r.TranscriptPath = r.Cwd + "/s.jsonl"
	os.WriteFile(r.TranscriptPath, []byte("{}\n"), 0o644)
	m.live = map[string]capture.Live{r.SessionID: {Status: "idle"}} // another terminal, not Herdr
	m.askResume()
	v := ansi.Strip(m.screen())
	if !strings.Contains(v, "在别的终端里运行") || !strings.Contains(v, "在跑") || !strings.Contains(v, "H 暂缓") || strings.Contains(v, "X 关掉 tab") {
		t.Fatalf("the check says why, and the running row has the actions that apply:\n%s", v)
	}
	m.Update(press("enter"))
	if m.quitting || !strings.Contains(m.notice, "别的终端") {
		t.Fatalf("resuming a second copy is refused: quitting=%v notice=%q", m.quitting, m.notice)
	}
}
