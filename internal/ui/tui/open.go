package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atotto/clipboard"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

// IDE defaults to Rebased (cfg.IDE): a bare name on macOS goes through open -a, on Windows rebased64.exe is looked up in
// %LOCALAPPDATA%\Programs\Rebased\bin when not on PATH; VS Code is code <dir>.

func defaultIDE() string {
	switch runtime.GOOS {
	case "darwin":
		return "Rebased"
	case "windows":
		return "rebased64.exe"
	}
	return "rebased"
}

func (m *Model) ideName() string {
	if m.cfg.IDE != "" {
		return m.cfg.IDE
	}
	return defaultIDE()
}

func openDir(app, dir string) error {
	if dir == "" {
		return errors.New(i18n.T("open.no_dir"))
	}
	if _, err := os.Stat(dir); err != nil {
		return errors.New(i18n.T("open.dir_missing") + dir)
	}
	var c *exec.Cmd
	switch {
	case runtime.GOOS == "darwin" && !strings.ContainsAny(app, `/\`) && !strings.HasSuffix(app, ".app") && lookPath(app) == "":
		c = exec.Command("open", "-a", app, dir)
		return c.Run()
	case runtime.GOOS == "windows" && strings.EqualFold(app, "rebased64.exe") && lookPath(app) == "":
		if p := filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Rebased", "bin", "rebased64.exe"); paths.Exists(p) {
			app = p
		}
	}
	c = exec.Command(app, dir)
	c.Dir = dir
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

func lookPath(app string) string {
	p, err := exec.LookPath(app)
	if err != nil {
		return ""
	}
	return p
}

func recDir(r *fav.Rec) string {
	if r.Cwd != "" {
		return r.Cwd
	}
	return r.GitRoot
}

func (m *Model) openIDE()   { m.openWith(m.ideName(), m.ideName()) }
func (m *Model) openCode()  { m.openWith("code", "code") }
func (m *Model) openFiles() { m.openWith(fileManager(), fileManagerName()) }

// fileManager opens a directory in the system's file manager.
func fileManager() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "windows":
		return "explorer"
	}
	return "xdg-open"
}

func fileManagerName() string {
	switch runtime.GOOS {
	case "darwin":
		return "Finder"
	case "windows":
		return i18n.T("open.explorer")
	}
	return i18n.T("open.files")
}

func (m *Model) openWith(app, name string) {
	r := m.ov.rec
	if r == nil {
		r = m.current()
	}
	if r == nil {
		return
	}
	dir := recDir(r)
	if err := openDir(app, dir); err != nil {
		m.flash(i18n.F("open.failed", name, err.Error()))
		return
	}
	m.closeOverlay()
	m.flash(i18n.F("open.done", name, render.Truncate(paths.Tilde(dir), 60)))
}

// copyText writes the system clipboard; tests replace it.
var copyText = clipboard.WriteAll
