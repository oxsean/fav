// Package envcheck reads what an agent finds on a machine for a directory, what a session saw, and how the two compare:
// names, versions and hashes only, never file contents or the values of settings.
package envcheck

import "time"

// Print is one machine's environment for a directory ("" for the machine alone).
type Print struct {
	At       time.Time `json:"at"`
	Dir      string    `json:"dir,omitempty"`
	NoDir    bool      `json:"no_dir,omitempty"` // Dir is not on this machine
	CLIs     []CLI     `json:"clis,omitempty"`
	Git      Git       `json:"git"`
	Files    []File    `json:"files,omitempty"`     // instruction files and the files they @-include, one level
	Skills   []string  `json:"skills,omitempty"`    // Claude's; a plugin's under its name
	MCP      []string  `json:"mcp,omitempty"`       // Claude's server names
	CodexMCP []string  `json:"codex_mcp,omitempty"` // Codex's server names
	Project  Project   `json:"project"`
	Provider Provider  `json:"provider"`
	Unknown  []string  `json:"unknown,omitempty"` // what could not be read: compared as unknown, never as equal
	Seen     *Seen     `json:"seen,omitempty"`    // asked about a session: what it saw
}

type CLI struct {
	Name    string `json:"name"`
	Found   bool   `json:"found,omitempty"` // on PATH
	Version string `json:"version,omitempty"`
}

// Git is the checkout a directory is in.
type Git struct {
	Repo      bool     `json:"repo,omitempty"` // the directory is in a git checkout
	Branch    string   `json:"branch,omitempty"`
	Head      string   `json:"head,omitempty"`
	Dirty     int      `json:"dirty,omitzero"`       // uncommitted files
	DirtyList []string `json:"dirty_list,omitempty"` // the first 30 of them
	Unpushed  int      `json:"unpushed,omitzero"`    // commits
	Remote    string   `json:"remote,omitempty"`     // origin's URL, without credentials
	RemoteKey string   `json:"remote_key,omitempty"` // task.RemoteKey of it
}

// File is an instruction file, paired across machines by its kind and its name under the home or the directory.
type File struct {
	Kind string `json:"kind"`          // the root Name is under: claude, codex, home, dir; path when none (Name absolute)
	Name string `json:"name"`          // slash-separated
	SHA  string `json:"sha,omitempty"` // of its bytes; a session's copy has none
	Norm string `json:"norm"`          // index.NormSHA of its text
}

// Project is what the CLIs keep for the directory by its path.
type Project struct {
	AllowedTools []string `json:"allowed_tools,omitempty"` // tool names, their arguments left out
	MCPJSON      []string `json:"mcp_json,omitempty"`      // enabledMcpjsonServers
	Trusted      bool     `json:"trusted,omitempty"`
	CodexTrust   string   `json:"codex_trust,omitempty"` // trust_level
}

// Provider is where the CLIs send their requests: setting names and host names, never a value or a key.
type Provider struct {
	ClaudeEnv  []string `json:"claude_env,omitempty"`  // the names under env in Claude's settings
	ClaudeHost string   `json:"claude_host,omitempty"` // ANTHROPIC_BASE_URL's host
	Codex      string   `json:"codex,omitempty"`       // model_provider
	CodexHost  string   `json:"codex_host,omitempty"`  // its base_url's host
}

// Seen is what a session saw, from the index's summary of its transcript, its files named as Print names them.
type Seen struct {
	At       time.Time `json:"at,omitzero"`
	CLI      string    `json:"cli"` // the session's: claude | codex
	Version  string    `json:"version,omitempty"`
	Model    string    `json:"model,omitempty"`
	Provider string    `json:"provider,omitempty"` // Codex model_provider
	Approval string    `json:"approval,omitempty"`
	Sandbox  string    `json:"sandbox,omitempty"`
	Platform string    `json:"platform,omitempty"`
	Shell    string    `json:"shell,omitempty"`
	Worktree bool      `json:"worktree,omitempty"`
	Files    []File    `json:"files,omitempty"`
	Skills   []string  `json:"skills,omitempty"`
	Tools    []string  `json:"tools,omitempty"`
	MCP      []string  `json:"mcp,omitempty"`
	Agents   []string  `json:"agents,omitempty"`
	Used     []string  `json:"used,omitempty"`     // skills it invoked
	UsedMCP  []string  `json:"used_mcp,omitempty"` // MCP servers it called, as its tool names spell them
	Known    []string  `json:"known,omitempty"`    // which of files, skills, tools, mcp, agents it recorded at all
}
