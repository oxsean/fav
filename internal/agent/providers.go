package agent

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
)

func init() {
	register(claude{})
	register(codex{})
	register(fake{})
	register(command{})
}

// ProviderFake runs `tend _fake-agent`, which writes a Claude-shaped transcript; its sessions are claude sessions.
const (
	ProviderFake    = "fake"
	ProviderCommand = "command"
)

type claude struct{}

func (claude) Name() string { return tend.ProviderClaude }
func (claude) Caps() Caps {
	return Caps{Resume: true, Fork: true, Headless: true, Continue: true, PresetSession: true, Sessions: true, Stream: true}
}
func (claude) Installed() bool { return onPath("claude") }

// Resume keeps the original session id (no --fork-session); --name makes the terminal title and /resume use it.
func (claude) Resume(r *tend.Rec, name string) (CommandSpec, error) {
	args := []string{"--resume", r.SessionID}
	if name != "" {
		args = append(args, "--name", name)
	}
	return CommandSpec{Exec: "claude", Args: args, Cwd: r.Cwd}, nil
}

func (claude) Fork(r *tend.Rec) (CommandSpec, error) {
	return CommandSpec{Exec: "claude", Args: []string{"--resume", r.SessionID, "--fork-session"}, Cwd: r.Cwd}, nil
}

func (claude) Start(cwd, prompt string) (CommandSpec, error) {
	var args []string
	if prompt != "" {
		args = []string{prompt}
	}
	return CommandSpec{Exec: "claude", Args: args, Cwd: cwd}, nil
}

func (claude) Launch(s LaunchSpec) (CommandSpec, error) {
	var args []string
	switch {
	case s.Stream: // permission prompts and questions come as control requests on stdout, answered on stdin; each
		// message it takes in comes back on stdout with its uuid
		args = append(args, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
			"--permission-prompt-tool", "stdio", "--replay-user-messages")
	case s.Headless:
		// ⚠️ no one answers a question in the background: the run convention makes it the final message
		args = append(args, "-p", "--output-format", "stream-json", "--verbose", "--disallowedTools", "AskUserQuestion")
	}
	switch {
	case s.Resume != "":
		args = append(args, "--resume", s.Resume)
	case s.SessionID != "":
		args = append(args, "--session-id", s.SessionID)
	}
	if s.Name != "" && !s.Headless {
		args = append(args, "--name", s.Name)
	}
	args = withModel(args, "--model", s.Profile.Model)
	if s.Profile.Permission != "" {
		args = append(args, "--permission-mode", s.Profile.Permission)
	}
	if s.Profile.Effort != "" {
		args = append(args, "--effort", s.Profile.Effort)
	}
	if len(s.Profile.Deny) > 0 {
		args = append(args, "--disallowedTools", strings.Join(s.Profile.Deny, ","))
	}
	if s.Settings != "" {
		args = append(args, "--settings", s.Settings)
	}
	if s.MCPConfig != "" {
		args = append(args, "--mcp-config", s.MCPConfig)
	}
	args = append(args, s.Profile.Args...)
	if s.Prompt != "" { // -p reads the prompt from stdin without it
		args = append(args, s.Prompt)
	}
	return CommandSpec{Exec: "claude", Args: args, Cwd: s.Dir}, nil
}

type codex struct{}

func (codex) Name() string { return tend.ProviderCodex }
func (codex) Caps() Caps {
	return Caps{Resume: true, Fork: true, Headless: true, Continue: true, Sessions: true, Stream: true}
}
func (codex) Installed() bool { return onPath("codex") }

func (codex) Resume(r *tend.Rec, _ string) (CommandSpec, error) {
	return CommandSpec{Exec: "codex", Args: []string{"resume", r.SessionID}, Cwd: r.Cwd}, nil
}

func (codex) Fork(r *tend.Rec) (CommandSpec, error) {
	return CommandSpec{Exec: "codex", Args: []string{"fork", r.SessionID}, Cwd: r.Cwd}, nil
}

func (codex) Start(cwd, prompt string) (CommandSpec, error) {
	var args []string
	if prompt != "" {
		args = []string{prompt}
	}
	return CommandSpec{Exec: "codex", Args: args, Cwd: cwd}, nil
}

func (codex) Launch(s LaunchSpec) (CommandSpec, error) {
	if s.Stream { // JSON-RPC on stdin / stdout: the supervisor starts the thread in s.Dir and each turn
		args := []string{"app-server", "-c", "approval_policy=on-request"} // ⚠️ bare TOML values: codex.cmd refuses quotes
		if s.Profile.Model != "" {
			args = append(args, "-c", "model="+s.Profile.Model)
		}
		if s.Profile.Permission != "" {
			args = append(args, "-c", "sandbox_mode="+s.Profile.Permission)
		}
		if s.Profile.Effort != "" {
			args = append(args, "-c", "model_reasoning_effort="+codexEffort(s.Profile.Effort))
		}
		return CommandSpec{Exec: "codex", Args: append(args, s.Profile.Args...), Cwd: s.Dir}, nil
	}
	var args []string
	if s.Headless {
		args = append(args, "exec", "--json", "--skip-git-repo-check")
	}
	if s.Dir != "" {
		args = append(args, "-C", s.Dir)
	}
	args = withModel(args, "-m", s.Profile.Model)
	if s.Profile.Permission != "" {
		args = append(args, "--sandbox", s.Profile.Permission)
	}
	args = append(args, s.Profile.Args...)
	if s.Headless && s.Resume != "" {
		args = append(args, "resume", s.Resume)
	}
	switch {
	case s.Prompt != "":
		args = append(args, s.Prompt)
	case s.Headless: // exec reads the prompt from stdin given "-"
		args = append(args, "-")
	}
	return CommandSpec{Exec: "codex", Args: args, Cwd: s.Dir}, nil
}

// fake stands in for claude in tests and end-to-end runs: no model, no quota.
type fake struct{}

func (fake) Name() string { return ProviderFake }
func (fake) Caps() Caps {
	return Caps{Headless: true, Continue: true, PresetSession: true, Stream: true}
}
func (fake) Installed() bool { return true }
func (fake) Resume(r *tend.Rec, name string) (CommandSpec, error) {
	return CommandSpec{}, unknown(ProviderFake)
}
func (fake) Fork(*tend.Rec) (CommandSpec, error)       { return CommandSpec{}, unknown(ProviderFake) }
func (fake) Start(string, string) (CommandSpec, error) { return CommandSpec{}, unknown(ProviderFake) }
func (fake) Launch(s LaunchSpec) (CommandSpec, error) {
	self, err := os.Executable()
	if err != nil {
		return CommandSpec{}, err
	}
	args := []string{"_fake-agent", "--session", cmp.Or(s.Resume, s.SessionID), "--dir", s.Dir}
	if s.PromptFile != "" {
		args = append(args, "--prompt-file", s.PromptFile)
	}
	if s.Stream {
		args = append(args, "--stream")
	}
	args = append(args, s.Profile.Args...)
	return CommandSpec{Exec: self, Args: args, Cwd: s.Dir}, nil
}

// command runs any CLI from a template (pi, OpenCode, Grok, Gemini …): only launching, no sessions.
type command struct{}

func (command) Name() string    { return ProviderCommand }
func (command) Caps() Caps      { return Caps{Headless: true} }
func (command) Installed() bool { return true }
func (command) Resume(*tend.Rec, string) (CommandSpec, error) {
	return CommandSpec{}, unknown(ProviderCommand)
}
func (command) Fork(*tend.Rec) (CommandSpec, error) { return CommandSpec{}, unknown(ProviderCommand) }
func (command) Start(string, string) (CommandSpec, error) {
	return CommandSpec{}, unknown(ProviderCommand)
}
func (command) Launch(s LaunchSpec) (CommandSpec, error) {
	argv := expand(s.Profile.Command, s)
	if len(argv) == 0 {
		return CommandSpec{}, unknown(ProviderCommand)
	}
	argv = append(argv, s.Profile.Args...)
	exe := argv[0]
	if !filepath.IsAbs(exe) && !onPath(exe) {
		return CommandSpec{}, i18n.E("resume.check.not_installed", exe)
	}
	return CommandSpec{Exec: exe, Args: slices.Clone(argv[1:]), Cwd: s.Dir}, nil
}

// codexEffort is effort as codex names it: it has no max.
func codexEffort(e string) string {
	if e == "max" {
		return "xhigh"
	}
	return e
}
