package capture

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
)

// DefaultIDE is Rebased: open -a on macOS; on Windows OpenDir also looks in %LOCALAPPDATA%\Programs\Rebased\bin.
func DefaultIDE() string {
	switch runtime.GOOS {
	case "darwin":
		return "Rebased"
	case "windows":
		return "rebased64.exe"
	}
	return "rebased"
}

func FileManager() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "windows":
		return "explorer"
	}
	return "xdg-open"
}

func FileManagerName() string {
	switch runtime.GOOS {
	case "darwin":
		return "Finder"
	case "windows":
		return i18n.T("open.explorer")
	}
	return i18n.T("open.files")
}

// OpenDir starts app on dir without waiting for it.
func OpenDir(app, dir string) error {
	if dir == "" {
		return errors.New(i18n.T("open.no_dir"))
	}
	if _, err := os.Stat(dir); err != nil {
		return i18n.E("open.dir_missing", dir)
	}
	_, onPath := exec.LookPath(app)
	switch {
	case runtime.GOOS == "darwin" && !strings.ContainsAny(app, `/\`) && !strings.HasSuffix(app, ".app") && onPath != nil:
		return exec.Command("open", "-a", app, dir).Run()
	case runtime.GOOS == "windows" && strings.EqualFold(app, "rebased64.exe") && onPath != nil:
		if p := filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Rebased", "bin", "rebased64.exe"); paths.Exists(p) {
			app = p
		}
	}
	c := exec.Command(app, dir)
	c.Dir = dir
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}
