package capture

import (
	"strings"
	"sync"
)

type shellKind int

const (
	posixShell shellKind = iota
	powerShell
	cmdShell
)

var userShell = sync.OnceValue(func() shellKind { return shellOf(ancestors()) })

// shellOf: the nearest ancestor that is a known shell; PowerShell when none is (the Windows default).
func shellOf(names []string) shellKind {
	for _, n := range names {
		switch strings.TrimSuffix(strings.ToLower(n), ".exe") {
		case "pwsh", "powershell":
			return powerShell
		case "cmd":
			return cmdShell
		case "bash", "sh", "zsh", "fish", "dash", "ksh":
			return posixShell
		}
	}
	return powerShell
}

func (k shellKind) line(c CommandSpec) string {
	line := k.join(c.Argv())
	if c.Cwd == "" {
		return line
	}
	switch k {
	case powerShell:
		return "Set-Location -LiteralPath " + k.quote(c.Cwd) + "; " + line
	case cmdShell:
		return "cd /d " + k.quote(c.Cwd) + " && " + line
	}
	return k.join([]string{"cd", c.Cwd}) + " && " + line
}

func (k shellKind) join(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = k.quote(a)
	}
	if k == powerShell && len(parts) > 0 && parts[0] != argv[0] {
		parts[0] = "& " + parts[0] // a quoted command name is a string to PowerShell until invoked
	}
	return strings.Join(parts, " ")
}

func (k shellKind) quote(s string) string {
	switch k {
	case powerShell:
		if s != "" && !strings.ContainsAny(s, " \t\n'\"`$&|;<>(){}@#,%") {
			return s
		}
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	case cmdShell:
		if s != "" && !strings.ContainsAny(s, " \t\"&|<>^()%!") {
			return s
		}
		// ⚠️ backslashes before a quote are doubled (argv parsing); "" is a literal quote inside quotes
		var b strings.Builder
		b.WriteByte('"')
		slashes := 0
		for _, r := range s {
			switch r {
			case '\\':
				slashes++
				continue
			case '"':
				b.WriteString(strings.Repeat(`\`, slashes*2))
				b.WriteString(`""`)
			default:
				b.WriteString(strings.Repeat(`\`, slashes))
				b.WriteRune(r)
			}
			slashes = 0
		}
		b.WriteString(strings.Repeat(`\`, slashes*2))
		b.WriteByte('"')
		return b.String()
	}
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
