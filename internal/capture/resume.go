package capture

import (
	"slices"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/tend"
)

// Installed: provider is a known agent whose CLI is on PATH.
func Installed(provider string) bool { return agent.Installed(provider) }

type Check struct {
	OK   bool
	Warn bool
	Text string
}

// contextNearlyFull: percent of the context window above which a resume starts by compacting.
const contextNearlyFull = 80

func Checks(r *tend.Rec) []Check {
	var out []Check

	if Installed(r.Provider) {
		out = append(out, Check{OK: true, Text: i18n.F("resume.check.source", sourceLabel(r))})
	} else {
		out = append(out, Check{Text: i18n.F("resume.check.not_installed", tend.ProviderLabel(r.Provider))})
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
	c := Check{OK: true, Text: i18n.F("resume.check.cli_ok", tend.ProviderLabel(provider))}
	if !Installed(provider) {
		c = Check{Text: i18n.F("resume.check.not_installed", tend.ProviderLabel(provider))}
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
func sourceLabel(r *tend.Rec) string {
	if !r.App {
		return tend.ProviderLabel(r.Provider)
	}
	if r.Provider == tend.ProviderCodex {
		return "Codex App"
	}
	return "Claude App"
}
