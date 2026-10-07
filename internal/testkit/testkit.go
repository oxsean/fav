// Package testkit holds helpers tests share for cross-platform fixtures.
package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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

// Main runs a package's tests with every per-user location (tend, Claude, Codex, home, OS config dirs) under a
// temporary root and always-failing herdr, claude and codex first on PATH; call it from TestMain.
func Main(m *testing.M) {
	AnswerAsCLI()
	var err error
	root, err = os.MkdirTemp("", "tend-test")
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
		"TEND_HOME": "tend", "CLAUDE_CONFIG_DIR": "claude", "CODEX_HOME": "codex",
	} {
		os.Setenv(k, filepath.Join(root, sub))
	}
	os.Setenv("HERDR_ENV", "")
	code := m.Run()
	os.RemoveAll(root)
	os.Exit(code)
}

// fakeVersion + a CLI's name is what a test binary linked as that CLI prints for `<name> --version`.
const fakeVersion = "TEND_TEST_FAKE_VERSION_"

// AnswerAsCLI (Main calls it; a TestMain that runs something else when an environment variable is set calls it first) makes a test binary run as herdr, claude or codex (by argv[0], when LinkCLIs linked it so) answer like one:
// the version LinkCLIs set for `--version`, otherwise a failure.
func AnswerAsCLI() {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if name != "herdr" && name != "claude" && name != "codex" {
		return
	}
	if out, ok := os.LookupEnv(fakeVersion + name); ok && slices.Equal(os.Args[1:], []string{"--version"}) {
		fmt.Println(out)
		os.Exit(0)
	}
	fmt.Fprintln(os.Stderr, "tend test stub: "+name+" is not available here")
	os.Exit(1)
}

// LinkCLIs puts links to this test binary into dir, one per key of versions, replacing what is there: each answers
// `--version` with its value and fails at anything else; an empty value is a CLI that always fails. ⚠️ Never a new
// script instead: macOS checks it the first time it runs, for seconds under load, and the callers run it under a timeout.
func LinkCLIs(t testing.TB, dir string, versions map[string]string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ext := filepath.Ext(self)
	if ext != ".exe" {
		ext = ""
	}
	for name, out := range versions {
		link := filepath.Join(dir, name+ext)
		os.Remove(link)
		if err := os.Link(self, link); err != nil {
			if err := os.Symlink(self, link); err != nil {
				t.Fatal(err)
			}
		}
		if out != "" {
			t.Setenv(fakeVersion+name, out)
		}
	}
}
