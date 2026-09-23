package shell

import (
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestOfExe(t *testing.T) {
	for exe, want := range map[string]Kind{
		"pwsh.exe": PowerShell, `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`: PowerShell,
		`c:\windows\system32\cmd.exe`: Cmd, "/usr/bin/bash": POSIX, "fish": POSIX,
	} {
		if got, ok := OfExe(exe); !ok || got != want {
			t.Errorf("OfExe(%q) = %v %v", exe, got, ok)
		}
	}
	if _, ok := OfExe("explorer.exe"); ok {
		t.Error("not a shell")
	}
}

func TestTerminalLinePerShell(t *testing.T) {
	spec := struct {
		dir  string
		argv []string
	}{`C:\work\it's here`, []string{"claude", "--resume", "abc", "--name", `it's "quoted" 50%`}}
	for k, want := range map[Kind]string{
		POSIX:      `cd 'C:\work\it'\''s here' && claude --resume abc --name 'it'\''s "quoted" 50%'`,
		PowerShell: `Set-Location -LiteralPath 'C:\work\it''s here'; claude --resume abc --name 'it''s "quoted" 50%'`,
		Cmd:        `cd /d "C:\work\it's here" && claude --resume abc --name "it's ""quoted"" 50%"`,
	} {
		if got := k.Line(spec.dir, spec.argv); got != want {
			t.Errorf("shell %d:\ngot  %s\nwant %s", k, got, want)
		}
	}
	if got := Cmd.Quote(`C:\dir with space\`); got != `"C:\dir with space\\"` {
		t.Errorf("a trailing backslash must not escape the closing quote: %s", got)
	}
	if got := PowerShell.Join([]string{`C:\Program Files\claude.exe`, "-r"}); got != `& 'C:\Program Files\claude.exe' -r` {
		t.Errorf("a quoted command name is invoked with &: %s", got)
	}
}

func TestPOSIXSplitsArgsBack(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell")
	}
	args := []string{"--name", `geo 排障 it's "quoted" $HOME; & 50%`, `C:\a b\`}
	out, err := exec.Command(sh, "-c", `for a in `+POSIX.Join(args)+`; do printf '%s\n' "$a"; done`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimRight(string(out), "\n"), "\n"); !slices.Equal(got, args) {
		t.Errorf("sh split %q, want %q", got, args)
	}
}

func TestPowerShellSplitsArgsBack(t *testing.T) {
	ps := ""
	for _, name := range []string{"pwsh", "powershell"} {
		if p, err := exec.LookPath(name); err == nil {
			ps = p
			break
		}
	}
	if ps == "" {
		t.Skip("no PowerShell")
	}
	args := []string{"--name", `it's "quoted" $HOME; & 50%`, `C:\a b\`}
	out, err := exec.Command(ps, "-NoProfile", "-Command", "& { $args | ForEach-Object { $_ } } "+PowerShell.Join(args)[len("--name "):]).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n"); !slices.Equal(got, args[1:]) {
		t.Errorf("PowerShell split %q, want %q", got, args[1:])
	}
}

func TestAncestorsSeen(t *testing.T) {
	if runtime.GOOS == "windows" && len(ancestors()) == 0 {
		t.Error("no parent processes read")
	}
}
