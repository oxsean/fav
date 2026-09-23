package capture

import (
	"errors"
	"os"
	"os/exec"

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

// Display is the command without the cd, quoted for the shell fav was started from; never executed.
func (c CommandSpec) Display() string { return userShell().join(c.Argv()) }

// TerminalLine is the line the user copies into the shell fav was started from; it cds first when Cwd is set.
func (c CommandSpec) TerminalLine() string { return userShell().line(c) }

// ShellLine is the line typed into a Herdr pane (herdr pane run goes through a POSIX shell, not argv).
func (c CommandSpec) ShellLine() string { return posixShell.line(c) }

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

// contextNearlyFull: percent of the context window above which a resume starts by compacting.
const contextNearlyFull = 80

func Checks(r *fav.Rec) []Check {
	var out []Check

	if _, err := lookPath(r.Provider); err == nil {
		out = append(out, Check{OK: true, Text: i18n.T("resume.check.source") + sourceLabel(r)})
	} else {
		out = append(out, Check{Text: providerLabel(r.Provider) + i18n.T("resume.check.not_installed")})
	}
	if c := dirCheck(r.Cwd, r.GitRemote); !c.OK && !c.Warn && r.Repo != "" && r.Repo != r.Cwd {
		out = append(out, Check{Text: i18n.F("resume.check.worktree_gone", r.Cwd, r.Repo)})
	} else {
		out = append(out, c)
	}

	if TranscriptAlive(r) {
		out = append(out, Check{OK: true, Text: i18n.T("resume.check.transcript_ok")})
	} else {
		out = append(out, Check{Text: i18n.T("resume.check.transcript_gone")})
	}

	if p, ok := ReadPulse(r.TranscriptPath); ok && p.Window > 0 && p.Context*100 >= p.Window*contextNearlyFull {
		out = append(out, Check{Warn: true, Text: i18n.F("resume.check.context_full", p.Context*100/p.Window)})
	}

	if r.CodexArchived {
		out = append(out, Check{Warn: true, Text: i18n.F("resume.check.codex_archived", r.SessionID)})
	}

	if r.GitBranch != "" && r.Cwd != "" && dirExists(r.Cwd) {
		if cur := GitOut(r.Cwd, "rev-parse", "--abbrev-ref", "HEAD"); cur != "" && cur != r.GitBranch {
			out = append(out, Check{Warn: true, Text: i18n.F("resume.check.branch_differs", cur, r.GitBranch)})
		}
	}
	return out
}

// startChecks: a new session only needs its CLI and the directory.
func startChecks(provider, cwd string) []Check {
	c := Check{OK: true, Text: i18n.T("resume.check.cli_ok") + providerLabel(provider)}
	if !Installed(provider) {
		c = Check{Text: providerLabel(provider) + i18n.T("resume.check.not_installed")}
	}
	return []Check{c, dirCheck(cwd, "")}
}

func dirCheck(cwd, remote string) Check {
	switch {
	case cwd == "":
		return Check{Warn: true, Text: i18n.T("resume.check.no_cwd")}
	case dirExists(cwd):
		return Check{OK: true, Text: i18n.T("resume.check.dir_ok") + cwd}
	}
	hint := ""
	if remote != "" {
		hint = i18n.T("resume.check.remote_hint") + remote
	}
	return Check{Text: i18n.T("resume.check.dir_gone") + cwd + hint}
}

// sourceLabel names where r was started: the desktop app or the CLI.
func sourceLabel(r *fav.Rec) string {
	if !r.App {
		return providerLabel(r.Provider)
	}
	if r.Provider == fav.ProviderCodex {
		return "Codex App"
	}
	return "Claude App"
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
