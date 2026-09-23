package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/oxsean/fav/internal/shell"
)

// WriteLaunchers: fav.cmd / fav.sh set the dataset's environment and pass their arguments to fav, so nothing needs quoting over ssh.
func (d *Dataset) WriteLaunchers(bin string) error {
	if runtime.GOOS == "windows" {
		var b strings.Builder
		b.WriteString("@echo off\r\n")
		for _, e := range d.Env() {
			fmt.Fprintf(&b, "set \"%s\"\r\n", e)
		}
		if bin == "" {
			bin = `%~dp0bin\fav.exe`
		}
		fmt.Fprintf(&b, "if exist \"%s\" (\"%s\" %%*) else (fav %%*)\r\n", bin, bin)
		return os.WriteFile(filepath.Join(d.Root, "fav.cmd"), []byte(b.String()), 0o755)
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for _, e := range d.Env() {
		k, v, _ := strings.Cut(e, "=")
		fmt.Fprintf(&b, "export %s=%s\n", k, shell.POSIX.Quote(v))
	}
	if bin == "" {
		bin = `$(dirname "$0")/bin/fav`
	} else {
		bin = shell.POSIX.Quote(bin)
	}
	fmt.Fprintf(&b, "if [ -x %s ]; then exec %s \"$@\"; else exec fav \"$@\"; fi\n", bin, bin)
	return os.WriteFile(filepath.Join(d.Root, "fav.sh"), []byte(b.String()), 0o755)
}
