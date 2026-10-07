package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/oxsean/fav/internal/shell"
)

const stubNote = "%s: disabled in a fixture dataset"

// WriteLaunchers: tend.cmd / tend.sh set the dataset's environment and pass their arguments to tend, so nothing needs quoting over ssh.
// They put stubs/ first on PATH, where herdr, claude and codex fail, so nothing run through them reaches the real ones.
func (d *Dataset) WriteLaunchers(bin string) error {
	stubs := filepath.Join(d.Root, "stubs")
	if err := writeStubs(stubs, runtime.GOOS); err != nil {
		return err
	}
	name, text := launcher(d.Env(), stubs, bin, runtime.GOOS)
	return os.WriteFile(filepath.Join(d.Root, name), []byte(text), 0o755)
}

func writeStubs(dir, goos string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"herdr", "claude", "codex"} {
		note := fmt.Sprintf(stubNote, name)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho "+shell.POSIX.Quote(note)+" >&2\nexit 1\n"), 0o755); err != nil {
			return err
		}
		if goos == "windows" {
			if err := os.WriteFile(filepath.Join(dir, name+".cmd"), []byte("@echo "+note+" 1>&2\r\n@exit /b 1\r\n"), 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

func launcher(env []string, stubs, bin, goos string) (name, text string) {
	var b strings.Builder
	if goos == "windows" {
		b.WriteString("@echo off\r\n")
		for _, e := range env {
			fmt.Fprintf(&b, "set \"%s\"\r\n", e)
		}
		fmt.Fprintf(&b, "set \"PATH=%s;%%PATH%%\"\r\n", stubs)
		if bin == "" {
			bin = `%~dp0bin\tend.exe`
		}
		fmt.Fprintf(&b, "if exist \"%s\" (\"%s\" %%*) else (tend %%*)\r\n", bin, bin)
		return "tend.cmd", b.String()
	}
	b.WriteString("#!/bin/sh\n")
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		fmt.Fprintf(&b, "export %s=%s\n", k, shell.POSIX.Quote(v))
	}
	fmt.Fprintf(&b, "export PATH=%s:\"$PATH\"\n", shell.POSIX.Quote(stubs))
	if bin == "" {
		bin = `$(dirname "$0")/bin/tend`
	} else {
		bin = shell.POSIX.Quote(bin)
	}
	fmt.Fprintf(&b, "if [ -x %s ]; then exec %s \"$@\"; else exec tend \"$@\"; fi\n", bin, bin)
	return "tend.sh", b.String()
}
