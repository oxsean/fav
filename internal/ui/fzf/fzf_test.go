package fzf

import (
	"runtime"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/shell"
)

func TestBindShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		for sh, want := range map[string]shell.Kind{"": shell.Cmd, `c:\windows\system32\cmd.exe`: shell.Cmd, `C:\Program Files\Git\bin\bash.exe`: shell.POSIX} {
			t.Setenv("SHELL", sh)
			if flags, k := bindShell(0, 74); flags != nil || k != want {
				t.Errorf("SHELL=%q: fzf's own shell, never --with-shell: %v %v", sh, flags, k)
			}
		}
		return
	}
	if flags, k := bindShell(0, 74); k != shell.POSIX || len(flags) != 2 || flags[1] != "sh -c" {
		t.Errorf("fzf 0.74 runs bindings with sh: %v %v", flags, k)
	}
	if flags, k := bindShell(0, 46); flags != nil || k != shell.POSIX {
		t.Errorf("before 0.51 fzf's default shell: %v %v", flags, k)
	}
}

func TestSwitchUsesTheBindShell(t *testing.T) {
	t.Setenv(bindShellEnv, "cmd")
	s := Switch(TabSessions)
	if !strings.Contains(s, "reload(") || strings.Contains(s, "cat ") || strings.Contains(s, "/dev/null") {
		t.Errorf("tab switch: %s", s)
	}
	if self := selfIn(shell.Cmd); strings.HasPrefix(self, "'") {
		t.Errorf("cmd does not read single quotes: %s", self)
	}
}
