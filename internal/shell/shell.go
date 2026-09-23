// Package shell writes command lines for a shell to read: POSIX sh, PowerShell or cmd, each with its own quoting. Every
// command string that a shell parses — a line the user copies, a line sent to a Herdr pane, an fzf binding — is built here.
package shell

import (
	"os"
	"strings"
	"sync"
)

type Kind int

const (
	POSIX Kind = iota
	PowerShell
	Cmd
)

// Env names the user's shell for child processes (fzf's children run under fzf's shell, not the user's); set it to
// posix, powershell or cmd to override the detection.
const Env = "FAV_SHELL"

var names = [...]string{"posix", "powershell", "cmd"}

func (k Kind) Name() string { return names[k] }

// User is the shell fav was started from: $FAV_SHELL, else the nearest known shell among fav's parent processes
// (PowerShell when none is found; POSIX systems report sh).
var User = sync.OnceValue(func() Kind {
	if k, ok := Named(os.Getenv(Env)); ok {
		return k
	}
	for _, exe := range ancestors() {
		if k, ok := OfExe(exe); ok {
			return k
		}
	}
	return PowerShell
})

// Named is the Kind whose Name is n.
func Named(n string) (Kind, bool) {
	for i, name := range names {
		if n == name {
			return Kind(i), true
		}
	}
	return POSIX, false
}

// OfExe is the Kind of a shell executable (a name or a path, .exe or not).
func OfExe(exe string) (Kind, bool) {
	name := strings.ToLower(exe)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	switch strings.TrimSuffix(name, ".exe") {
	case "pwsh", "powershell":
		return PowerShell, true
	case "cmd":
		return Cmd, true
	case "bash", "sh", "zsh", "fish", "dash", "ksh":
		return POSIX, true
	}
	return POSIX, false
}

// Editor: $VISUAL else $EDITOR, split on whitespace ("code -w"); nil when neither is set.
func Editor() []string {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	return strings.Fields(ed)
}

// Line is argv run in dir: a cd first when dir is set.
func (k Kind) Line(dir string, argv []string) string {
	line := k.Join(argv)
	if dir == "" {
		return line
	}
	switch k {
	case PowerShell:
		return "Set-Location -LiteralPath " + k.Quote(dir) + "; " + line
	case Cmd:
		return "cd /d " + k.Quote(dir) + " && " + line
	}
	return k.Join([]string{"cd", dir}) + " && " + line
}

func (k Kind) Join(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = k.Quote(a)
	}
	if k == PowerShell && len(parts) > 0 && parts[0] != argv[0] {
		parts[0] = "& " + parts[0] // a quoted command name is a string to PowerShell until invoked
	}
	return strings.Join(parts, " ")
}

func (k Kind) Quote(s string) string {
	switch k {
	case PowerShell:
		if s != "" && !strings.ContainsAny(s, " \t\n'\"`$&|;<>(){}@#,%") {
			return s
		}
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	case Cmd:
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
