// Package agent knows how each coding-agent CLI is started, resumed and forked, and what it can do. Everything
// provider-specific about launching lives here; reading transcripts stays in capture, per format.
package agent

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
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
	Continue      bool // runs a prompt without a terminal in an existing session
	PresetSession bool // the session id can be chosen before launch
	Sessions      bool // its sessions are indexed (transcripts on disk that capture reads)
	Stream        bool // a headless run talks both ways on stdin / stdout: answers, messages while it runs
}

// Profile is one way to run an agent (config `agents`).
type Profile = tend.AgentProfile

// LaunchSpec is one run's start.
type LaunchSpec struct {
	Profile    Profile
	Dir        string
	Prompt     string // the first message
	PromptFile string // where the task brief is; the prompt refers to it
	Headless   bool
	Stream     bool   // headless and both ways: the prompt comes on stdin as the first message
	SessionID  string // preset session id, when the provider takes one
	Resume     string // the session a headless run continues
	Name       string // short name shown by the agent (claude --name)
	Settings   string // claude: a settings file with the definition's hooks
	MCPConfig  string // claude: the definition's MCP servers
}

// Provider starts, resumes and forks one CLI's sessions.
type Provider interface {
	Name() string
	Caps() Caps
	Installed() bool
	Launch(s LaunchSpec) (CommandSpec, error)
	Resume(r *tend.Rec, name string) (CommandSpec, error)
	Fork(r *tend.Rec) (CommandSpec, error)
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
func Sessions() []string { return []string{tend.ProviderClaude, tend.ProviderCodex} }

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
func ResumeOf(r *tend.Rec, name string) (CommandSpec, error) {
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
func ForkOf(r *tend.Rec) (CommandSpec, error) {
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
		return tend.ProviderClaude
	case ProviderCommand:
		return ""
	}
	return name
}

// CanContinue: prof can go on with a session of provider on machine in the background: its CLI continues sessions,
// they are provider's, and prof is bound to no other machine.
func CanContinue(prof Profile, provider, machine string) bool {
	p, ok := Get(prof.Provider)
	return ok && p.Caps().Continue && SessionProvider(prof.Provider) == provider && (prof.Machine == "" || prof.Machine == machine)
}

// CodexQuiet: a Codex session written to more recently than this counts as running even without a thread lock (codex
// exec and app-server threads hold none that capture.LocalLive counts).
const CodexQuiet = 15 * time.Second

// AttachOf builds the command that attaches to a Claude background session (they cannot be resumed).
func AttachOf(r *tend.Rec, backgroundID string) CommandSpec {
	return CommandSpec{Exec: "claude", Args: []string{"attach", backgroundID}, Cwd: r.Cwd}
}

// BypassArgv: the command line runs its agent with every permission prompt skipped or without a sandbox, however the
// flag is spelled.
func BypassArgv(argv []string) (bypass bool) {
	for i, a := range argv {
		k, v, joined := strings.Cut(a, "=")
		if len(a) > 2 && a[0] == '-' && a[1] != '-' { // clap's -sVALUE
			k, v, joined = a[:2], a[2:], true
		}
		if !joined && i+1 < len(argv) {
			v = argv[i+1]
		}
		switch k {
		case "--settings", "--allowedTools", "--allowed-tools", "--permission-prompts", "--mcp-config", "--plugin-url", "-p", "--profile":
			if k != "-p" || argv[0] == "codex" { // claude's -p is --print; codex's names a config profile
				bypass = true
			}
		case "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "--dangerously-bypass-approvals-and-sandbox", "--yolo":
			bypass = true
		case "--permission-mode":
			bypass = bypass || v == "bypassPermissions"
		case "-s", "--sandbox":
			bypass = bypass || v == "danger-full-access"
		case "-c", "--config":
			bypass = bypass || strings.Contains(v, "danger-full-access")
		}
	}
	return bypass
}

// Profiles are the agents one can run: the built-in ones, then config's (a config profile replaces a built-in one of
// the same name).
func Profiles(config []Profile) []Profile {
	out := []Profile{
		{Name: tend.ProviderClaude, Provider: tend.ProviderClaude},
		{Name: tend.ProviderCodex, Provider: tend.ProviderCodex},
		{Name: ProviderFake, Provider: ProviderFake},
	}
	for _, p := range config {
		if i := slices.IndexFunc(out, func(q Profile) bool { return q.Name == p.Name }); i >= 0 {
			out[i] = p
		} else {
			out = append(out, p)
		}
	}
	return out
}
