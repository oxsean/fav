package capture

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

func lookPath(provider string) (string, error) {
	switch provider {
	case fav.ProviderClaude:
		return exec.LookPath("claude")
	case fav.ProviderCodex:
		return exec.LookPath("codex")
	}
	return "", i18n.E("resume.unknown_provider", provider)
}

// CommandSpec is a structured command. ⚠️ Never hand an unescaped string to a shell.
type CommandSpec struct {
	Exec string
	Args []string
	Cwd  string
}

func (c CommandSpec) Argv() []string { return append([]string{c.Exec}, c.Args...) }

// for display only, never executed
func (c CommandSpec) Display() string { return ShellJoin(c.Argv()) }

// ShellLine is the line typed into a shell (herdr pane run goes through a shell, not argv).
func (c CommandSpec) ShellLine() string { return ShellJoin(c.Argv()) }

func ShellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Claude keeps the original session id (no --fork-session).
func BuildResume(r *fav.Rec) (CommandSpec, error) {
	if r.SessionID == "" {
		return CommandSpec{}, errors.New(i18n.T("resume.check.no_session"))
	}
	switch r.Provider {
	case fav.ProviderClaude:
		// --name: the terminal title and the /resume list use fav's short label
		args := []string{"--resume", r.SessionID}
		if name := TabLabel(r); name != "" {
			args = append(args, "--name", name)
		}
		return CommandSpec{Exec: "claude", Args: args, Cwd: r.Cwd}, nil
	case fav.ProviderCodex:
		return CommandSpec{Exec: "codex", Args: []string{"resume", r.SessionID}, Cwd: r.Cwd}, nil
	default:
		return CommandSpec{}, i18n.E("resume.unknown_provider", r.Provider)
	}
}

type Check struct {
	OK   bool
	Warn bool
	Text string
}

func Checks(r *fav.Rec) []Check {
	var out []Check

	if _, err := lookPath(r.Provider); err == nil {
		out = append(out, Check{OK: true, Text: i18n.T("resume.check.source") + providerLabel(r.Provider)})
	} else {
		out = append(out, Check{Text: providerLabel(r.Provider) + i18n.T("resume.check.not_installed")})
	}

	switch {
	case r.Cwd == "":
		out = append(out, Check{Warn: true, Text: i18n.T("resume.check.no_cwd")})
	case dirExists(r.Cwd):
		out = append(out, Check{OK: true, Text: i18n.T("resume.check.dir_ok") + r.Cwd})
	default:
		hint := ""
		if r.GitRemote != "" {
			hint = i18n.T("resume.check.remote_hint") + r.GitRemote
		}
		out = append(out, Check{Text: i18n.T("resume.check.dir_gone") + r.Cwd + hint})
	}

	if TranscriptAlive(r) {
		out = append(out, Check{OK: true, Text: i18n.T("resume.check.transcript_ok")})
	} else {
		out = append(out, Check{Text: i18n.T("resume.check.transcript_gone")})
	}

	if r.GitBranch != "" && r.Cwd != "" && dirExists(r.Cwd) {
		if cur := GitOut(r.Cwd, "rev-parse", "--abbrev-ref", "HEAD"); cur != "" && cur != r.GitBranch {
			out = append(out, Check{Warn: true, Text: i18n.F("resume.check.branch_differs", cur, r.GitBranch)})
		}
	}
	return out
}

func providerLabel(p string) string {
	switch p {
	case fav.ProviderClaude:
		return "Claude Code"
	case fav.ProviderCodex:
		return "Codex CLI"
	}
	return p
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
