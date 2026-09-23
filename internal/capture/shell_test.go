package capture

import (
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestShellOf(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  shellKind
	}{
		{[]string{"pwsh.exe", "WindowsTerminal.exe"}, powerShell},
		{[]string{"go.exe", "cmd.exe", "powershell.exe"}, cmdShell},
		{[]string{"bash.exe", "mintty.exe"}, posixShell},
		{[]string{"explorer.exe"}, powerShell},
		{nil, powerShell},
	} {
		if got := shellOf(c.names); got != c.want {
			t.Errorf("shellOf(%v) = %d, want %d", c.names, got, c.want)
		}
	}
}

func TestTerminalLinePerShell(t *testing.T) {
	spec := CommandSpec{Exec: "claude", Args: []string{"--resume", "abc", "--name", `it's "quoted" 50%`}, Cwd: `C:\work\it's here`}
	for k, want := range map[shellKind]string{
		posixShell: `cd 'C:\work\it'\''s here' && claude --resume abc --name 'it'\''s "quoted" 50%'`,
		powerShell: `Set-Location -LiteralPath 'C:\work\it''s here'; claude --resume abc --name 'it''s "quoted" 50%'`,
		cmdShell:   `cd /d "C:\work\it's here" && claude --resume abc --name "it's ""quoted"" 50%"`,
	} {
		if got := k.line(spec); got != want {
			t.Errorf("shell %d:\ngot  %s\nwant %s", k, got, want)
		}
	}
	if got := cmdShell.quote(`C:\dir with space\`); got != `"C:\dir with space\\"` {
		t.Errorf("a trailing backslash must not escape the closing quote: %s", got)
	}
	if got := powerShell.join([]string{`C:\Program Files\claude.exe`, "-r"}); got != `& 'C:\Program Files\claude.exe' -r` {
		t.Errorf("a quoted command name is invoked with &: %s", got)
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
	out, err := exec.Command(ps, "-NoProfile", "-Command", "& { $args | ForEach-Object { $_ } } "+powerShell.join(args)[len("--name "):]).Output()
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
