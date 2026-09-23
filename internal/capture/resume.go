package capture

import (
	"errors"
	"os/exec"
	"slices"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/shell"
)

func known(provider string) bool {
	return provider == fav.ProviderClaude || provider == fav.ProviderCodex
}

func Installed(provider string) bool {
	if !known(provider) {
		return false
	}
	_, err := exec.LookPath(provider)
	return err == nil
}

// CommandSpec is a structured command. ⚠️ Never hand an unescaped string to a shell.
type CommandSpec struct {
	Exec string
	Args []string
	Cwd  string
}

func (c CommandSpec) Argv() []string { return append([]string{c.Exec}, c.Args...) }

// Display is the command without the cd, quoted for the shell fav was started from; never executed.
func (c CommandSpec) Display() string { return shell.User().Join(c.Argv()) }

// TerminalLine is the line the user copies into the shell fav was started from; it cds first when Cwd is set.
func (c CommandSpec) TerminalLine() string { return shell.User().Line(c.Cwd, c.Argv()) }

// ShellLine is the line typed into a Herdr pane (herdr pane run goes through a POSIX shell, not argv).
func (c CommandSpec) ShellLine() string { return shell.POSIX.Line(c.Cwd, c.Argv()) }

// buildResume: Claude keeps the original session id (no --fork-session).
func buildResume(r *fav.Rec) (CommandSpec, error) {
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

	if Installed(r.Provider) {
		out = append(out, Check{OK: true, Text: i18n.F("resume.check.source", sourceLabel(r))})
	} else {
		out = append(out, Check{Text: i18n.F("resume.check.not_installed", fav.ProviderLabel(r.Provider))})
	}
	if c := dirCheck(r.Cwd, r.GitRemote); !c.OK && !c.Warn && r.Repo != "" && !paths.Same(r.Repo, r.Cwd) {
		out = append(out, Check{Text: i18n.F("resume.check.worktree_gone", r.Cwd, r.Repo)})
	} else {
		out = append(out, c)
	}

	if slices.ContainsFunc(r.Transcripts(), paths.Exists) {
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

	if r.GitBranch != "" && r.Cwd != "" && paths.IsDir(r.Cwd) {
		if cur := GitOut(r.Cwd, "rev-parse", "--abbrev-ref", "HEAD"); cur != "" && cur != r.GitBranch {
			out = append(out, Check{Warn: true, Text: i18n.F("resume.check.branch_differs", cur, r.GitBranch)})
		}
	}
	return out
}

// startChecks: a new session only needs its CLI and the directory.
func startChecks(provider, cwd string) []Check {
	c := Check{OK: true, Text: i18n.F("resume.check.cli_ok", fav.ProviderLabel(provider))}
	if !Installed(provider) {
		c = Check{Text: i18n.F("resume.check.not_installed", fav.ProviderLabel(provider))}
	}
	return []Check{c, dirCheck(cwd, "")}
}

func dirCheck(cwd, remote string) Check {
	switch {
	case cwd == "":
		return Check{Warn: true, Text: i18n.T("resume.check.no_cwd")}
	case paths.IsDir(cwd):
		return Check{OK: true, Text: i18n.F("resume.check.dir_ok", cwd)}
	}
	if remote != "" {
		return Check{Text: i18n.F("resume.check.dir_gone_remote", cwd, remote)}
	}
	return Check{Text: i18n.F("resume.check.dir_gone", cwd)}
}

// sourceLabel names where r was started: the desktop app or the CLI.
func sourceLabel(r *fav.Rec) string {
	if !r.App {
		return fav.ProviderLabel(r.Provider)
	}
	if r.Provider == fav.ProviderCodex {
		return "Codex App"
	}
	return "Claude App"
}
