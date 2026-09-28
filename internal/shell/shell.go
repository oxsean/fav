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
const Env = "TEND_SHELL"

var names = [...]string{"posix", "powershell", "cmd"}

func (k Kind) Name() string { return names[k] }

// User is the shell tend was started from: $TEND_SHELL, else the nearest known shell among tend's parent processes
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

// Split reads a POSIX line back into its words, only when the shell would run them as they are: plain words, single
// quotes, backslash escapes, and double quotes without expansion. Anything else (operators, redirections, expansions,
// globs, an assignment before the command, an unquoted newline) is refused.
func (k Kind) Split(line string) ([]string, bool) {
	if k != POSIX {
		return nil, false
	}
	var words []string
	var w strings.Builder
	inWord := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			if inWord {
				words, inWord = append(words, w.String()), false
				w.Reset()
			}
			continue
		case c == '\'':
			j := strings.IndexByte(line[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			w.WriteString(line[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			i++
			for ; i < len(line) && line[i] != '"'; i++ {
				switch line[i] {
				case '$', '`':
					return nil, false
				case '\\':
					if i+1 < len(line) && strings.IndexByte(`"\$`+"`", line[i+1]) >= 0 {
						i++
					}
				}
				w.WriteByte(line[i])
			}
			if i >= len(line) {
				return nil, false
			}
		case c == '\\':
			if i+1 >= len(line) || line[i+1] == '\n' {
				return nil, false
			}
			i++
			w.WriteByte(line[i])
		case strings.IndexByte(";&|<>()$`\n*?[]{}#", c) >= 0, c == '~' && !inWord, c == '=' && len(words) == 0:
			return nil, false
		default:
			w.WriteByte(c)
		}
		inWord = true
	}
	if inWord {
		words = append(words, w.String())
	}
	return words, len(words) > 0
}
