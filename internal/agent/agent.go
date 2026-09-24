// Package agent knows how each coding-agent CLI is started, resumed and forked, and what it can do. Everything
// provider-specific about launching lives here; reading transcripts stays in capture, per format.
package agent

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/shell"
)

// CommandSpec is a structured command. ⚠️ Never hand an unescaped string to a shell.
type CommandSpec struct {
	Exec string
	Args []string
	Cwd  string
}

func (c CommandSpec) Argv() []string { return append([]string{c.Exec}, c.Args...) }

// Display is the command without the cd, quoted for the shell tend was started from; never executed.
func (c CommandSpec) Display() string { return shell.User().Join(c.Argv()) }

// TerminalLine is the line the user copies into the shell tend was started from; it cds first when Cwd is set.
func (c CommandSpec) TerminalLine() string { return shell.User().Line(c.Cwd, c.Argv()) }

// ShellLine is the line typed into a Herdr pane (herdr pane run goes through a POSIX shell, not argv).
func (c CommandSpec) ShellLine() string { return shell.POSIX.Line(c.Cwd, c.Argv()) }

// Caps is what a provider supports.
type Caps struct {
	Resume        bool // resume a session by id
	Fork          bool // a new session carrying another's history
	Headless      bool // runs a prompt without a terminal
	PresetSession bool // the session id can be chosen before launch
	Sessions      bool // its sessions are indexed (transcripts on disk that capture reads)
}

// Profile is one way to run an agent (config `agents`).
type Profile = fav.AgentProfile

// LaunchSpec is one run's start.
type LaunchSpec struct {
	Profile    Profile
	Dir        string
	Prompt     string // the first message
	PromptFile string // where the task brief is; the prompt refers to it
	Headless   bool
	SessionID  string // preset session id, when the provider takes one
	Name       string // short name shown by the agent (claude --name)
}

// Provider starts, resumes and forks one CLI's sessions.
type Provider interface {
	Name() string
	Caps() Caps
	Installed() bool
	Launch(s LaunchSpec) (CommandSpec, error)
	Resume(r *fav.Rec, name string) (CommandSpec, error)
	Fork(r *fav.Rec) (CommandSpec, error)
	Start(cwd, prompt string) (CommandSpec, error)
}

var providers = map[string]Provider{}

func register(p Provider) { providers[p.Name()] = p }

// Get is the provider called name.
func Get(name string) (Provider, bool) {
	p, ok := providers[name]
	return p, ok
}

// Sessions are the providers whose sessions tend indexes, in display order.
func Sessions() []string { return []string{fav.ProviderClaude, fav.ProviderCodex} }

// Installed: name is a known provider whose CLI is on PATH.
func Installed(name string) bool {
	p, ok := Get(name)
	return ok && p.Installed()
}

func onPath(exe string) bool {
	_, err := exec.LookPath(exe)
	return err == nil
}

func noSession() error { return errors.New(i18n.T("resume.check.no_session")) }

func unknown(provider string) error { return i18n.E("resume.unknown_provider", provider) }

// ResumeOf builds r's resume command.
func ResumeOf(r *fav.Rec, name string) (CommandSpec, error) {
	p, ok := Get(r.Provider)
	if !ok || !p.Caps().Resume {
		return CommandSpec{}, unknown(r.Provider)
	}
	if r.SessionID == "" {
		return CommandSpec{}, noSession()
	}
	return p.Resume(r, name)
}

// ForkOf builds the command for a new session carrying r's history.
func ForkOf(r *fav.Rec) (CommandSpec, error) {
	p, ok := Get(r.Provider)
	if !ok || !p.Caps().Fork {
		return CommandSpec{}, unknown(r.Provider)
	}
	if r.SessionID == "" {
		return CommandSpec{}, noSession()
	}
	return p.Fork(r)
}

// StartOf builds the command for a new session of provider in cwd whose first message is prompt ("" = none).
func StartOf(provider, cwd, prompt string) (CommandSpec, error) {
	p, ok := Get(provider)
	if !ok {
		return CommandSpec{}, unknown(provider)
	}
	return p.Start(cwd, prompt)
}

// LaunchOf builds a run's command from its profile.
func LaunchOf(s LaunchSpec) (CommandSpec, error) {
	p, ok := Get(s.Profile.Provider)
	if !ok {
		return CommandSpec{}, unknown(s.Profile.Provider)
	}
	return p.Launch(s)
}

// expand fills a command template: {prompt_file} {model} {dir}; an argument that is only a placeholder whose value
// is empty is dropped. The brief itself never goes into argv (ps shows it): {prompt_file} or stdin.
func expand(tmpl []string, s LaunchSpec) []string {
	vals := map[string]string{"{prompt_file}": s.PromptFile, "{model}": s.Profile.Model, "{dir}": s.Dir}
	var out []string
	for _, a := range tmpl {
		if v, ok := vals[a]; ok && v == "" {
			continue
		}
		for k, v := range vals {
			a = strings.ReplaceAll(a, k, v)
		}
		out = append(out, a)
	}
	return out
}

func withModel(args []string, flag, model string) []string {
	if model == "" {
		return args
	}
	return append(args, flag, model)
}

// SessionProvider is whose sessions a provider's runs leave behind: fake writes claude transcripts; command leaves none.
func SessionProvider(name string) string {
	switch name {
	case ProviderFake:
		return fav.ProviderClaude
	case ProviderCommand:
		return ""
	}
	return name
}

// AttachOf builds the command that attaches to a Claude background session (they cannot be resumed).
func AttachOf(r *fav.Rec, backgroundID string) CommandSpec {
	return CommandSpec{Exec: "claude", Args: []string{"attach", backgroundID}, Cwd: r.Cwd}
}

// Bypass: p runs with every permission prompt skipped.
func Bypass(p Profile) bool {
	switch p.Permission {
	case "bypassPermissions", "danger-full-access":
		return true
	}
	for _, a := range p.Args {
		switch a {
		case "--dangerously-skip-permissions", "--dangerously-bypass-approvals-and-sandbox", "--yolo":
			return true
		}
	}
	return false
}
