// Package envcheck reads what an agent finds on a machine for a directory, what a session saw, and how the two compare:
// names, versions and hashes only, never file contents or the values of settings.
package envcheck

// Print is one machine's environment for a directory.
type Print struct {
	CLIs     []CLI    `json:"clis,omitempty"`
	Git      Git      `json:"git"`
	Files    []File   `json:"files,omitempty"`  // instruction files and the files they @-include, one level
	Skills   []string `json:"skills,omitempty"` // a plugin's under its name
	MCP      []string `json:"mcp,omitempty"`    // server names
	Project  Project  `json:"project"`
	Provider Provider `json:"provider"`
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
	Remote    string   `json:"remote,omitempty"`     // origin's URL
	RemoteKey string   `json:"remote_key,omitempty"` // task.RemoteKey of it
}

// File is an instruction file, paired across machines by its kind and its name under the home or the directory.
type File struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	SHA  string `json:"sha"`  // of its bytes
	Norm string `json:"norm"` // of its text with LF line ends
}

// Project is what the CLIs keep for the directory by its path.
type Project struct {
	AllowedTools []string `json:"allowed_tools,omitempty"`
	MCPJSON      []string `json:"mcp_json,omitempty"` // enabledMcpjsonServers
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
