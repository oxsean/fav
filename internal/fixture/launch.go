package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/oxsean/fav/internal/shell"
)

// WriteLaunchers: tend.cmd / tend.sh set the dataset's environment and pass their arguments to tend, so nothing needs quoting over ssh.
func (d *Dataset) WriteLaunchers(bin string) error {
	if runtime.GOOS == "windows" {
		var b strings.Builder
		b.WriteString("@echo off\r\n")
		for _, e := range d.Env() {
			fmt.Fprintf(&b, "set \"%s\"\r\n", e)
		}
		if bin == "" {
			bin = `%~dp0bin\tend.exe`
		}
		fmt.Fprintf(&b, "if exist \"%s\" (\"%s\" %%*) else (tend %%*)\r\n", bin, bin)
		return os.WriteFile(filepath.Join(d.Root, "tend.cmd"), []byte(b.String()), 0o755)
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for _, e := range d.Env() {
		k, v, _ := strings.Cut(e, "=")
		fmt.Fprintf(&b, "export %s=%s\n", k, shell.POSIX.Quote(v))
	}
	if bin == "" {
		bin = `$(dirname "$0")/bin/tend`
	} else {
		bin = shell.POSIX.Quote(bin)
	}
	fmt.Fprintf(&b, "if [ -x %s ]; then exec %s \"$@\"; else exec tend \"$@\"; fi\n", bin, bin)
	return os.WriteFile(filepath.Join(d.Root, "tend.sh"), []byte(b.String()), 0o755)
}
