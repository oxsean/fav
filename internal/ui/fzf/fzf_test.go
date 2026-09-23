package fzf

import (
	"runtime"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

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

func TestSwitchReloads(t *testing.T) {
	t.Setenv(bindShellEnv, "cmd")
	if s := Switch(TabSessions); !strings.Contains(s, "reload(") {
		t.Errorf("tab switch: %s", s)
	}
}

func TestTabLivesInThePrompt(t *testing.T) {
	for _, lang := range []string{"en", "zh"} {
		i18n.Set(lang)
		for t0 := range Tab(len(tabNames)) {
			if got := TabOf(t0.prompt()); got != t0 {
				t.Errorf("%s: TabOf(%q) = %v", lang, t0.prompt(), got)
			}
			if t0.Next(1).Next(-1) != t0 || t0.Next(len(tabNames)) != t0 {
				t.Errorf("Next wraps around from %v", t0)
			}
		}
	}
	i18n.Set("en")
}
