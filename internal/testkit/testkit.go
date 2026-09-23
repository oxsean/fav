// Package testkit holds helpers tests share for cross-platform fixtures.
package testkit

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/oxsean/fav/internal/paths"
)

// JSONString is s as a JSON string literal, quotes included, as the CLIs write it (& < > not escaped): use it to put
// native paths (Windows backslashes) into hand-written transcript lines.
func JSONString(s string) string { return `"` + paths.JSON(s) + `"` }

// PosixOnly skips a test whose fixtures spell POSIX paths; internal/fixture covers the same behaviour with native paths.
func PosixOnly(t testing.TB) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-path fixture; internal/fixture covers Windows")
	}
}

var root string

// Shared: p is under the package-wide root of Main, not a test's own directory.
func Shared(p string) bool { return root != "" && paths.Under(p, root) }

// Main runs a package's tests with every per-user location (fav, Claude, Codex, home, OS config dirs) under a
// temporary root and always-failing herdr, claude and codex first on PATH; call it from TestMain.
func Main(m *testing.M) {
	var err error
	root, err = os.MkdirTemp("", "fav-test")
	if err != nil {
		panic(err)
	}
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0o755)
	for _, name := range []string{"herdr", "claude", "codex"} {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0o755)
		os.WriteFile(filepath.Join(bin, name+".cmd"), []byte("@exit /b 1\r\n"), 0o755)
	}
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for k, sub := range map[string]string{
		"HOME": "home", "USERPROFILE": "home", "XDG_CONFIG_HOME": "config", "APPDATA": "config", "LOCALAPPDATA": "local",
		"FAV_HOME": "fav", "CLAUDE_CONFIG_DIR": "claude", "CODEX_HOME": "codex",
	} {
		os.Setenv(k, filepath.Join(root, sub))
	}
	os.Setenv("HERDR_ENV", "")
	code := m.Run()
	os.RemoveAll(root)
	os.Exit(code)
}
