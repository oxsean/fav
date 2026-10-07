package index

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/tend"
)

// Env is what a session saw of its environment, folded from its transcript as the scan reads on: Claude's attachments
// and the version on its lines, Codex's session_meta and turn_context. Names, versions and hashes, never a content.
type Env struct {
	At       time.Time `json:"at"`                 // the newest line it came from
	Version  string    `json:"version,omitempty"`  // the CLI's
	Model    string    `json:"model,omitempty"`    // Claude's model attachment, Codex's turn_context
	Provider string    `json:"provider,omitempty"` // Codex model_provider
	Approval string    `json:"approval,omitempty"` // Codex approval_policy
	Sandbox  string    `json:"sandbox,omitempty"`  // Codex sandbox_policy
	Mode     string    `json:"mode,omitempty"`     // Claude permissionMode: a permission-mode line's or a prompt's
	Platform string    `json:"platform,omitempty"` // Claude's environment snapshot
	Shell    string    `json:"shell,omitempty"`
	Worktree bool      `json:"worktree,omitempty"`
	Files    []EnvFile `json:"files,omitempty"`    // instruction files loaded, as the last change left them
	Skills   []string  `json:"skills,omitempty"`   // listed to it
	Tools    []string  `json:"tools,omitempty"`    // deferred tools announced
	MCP      []string  `json:"mcp,omitempty"`      // MCP servers whose instructions it got
	Agents   []string  `json:"agents,omitempty"`   // sub-agent types listed
	Used     []string  `json:"used,omitempty"`     // skills it invoked
	UsedMCP  []string  `json:"used_mcp,omitempty"` // MCP servers whose tools it called
	Known    []string  `json:"known,omitempty"`    // which of the sets above a line set: an empty one is then known empty
}

// EnvFile is an instruction file a session loaded.
type EnvFile struct {
	Path string `json:"path"`
	Type string `json:"type,omitempty"` // Claude's: User, Project, Local, Managed
	Norm string `json:"norm"`           // NormSHA of the text it loaded
}

// NormSHA is the sha256 of text with LF line ends and no surrounding white space: a file on disk and Claude's copy of
// it compare by it.
func NormSHA(text []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(bytes.ReplaceAll(text, []byte("\r\n"), []byte("\n"))))
	return hex.EncodeToString(sum[:])
}

const (
	envNamesCap = 300 // names kept per set
	envFilesCap = 64
)

// Env is the summary of session id's newest transcript that has one; nil when none does.
func (idx *Index) Env(provider, sessionID string) *Env {
	var hit *File
	for _, f := range idx.files {
		if f.Provider == provider && f.SessionID == sessionID && f.Env != nil && (hit == nil || f.ModTime.After(hit.ModTime)) {
			hit = f
		}
	}
	if hit == nil {
		return nil
	}
	return hit.Env.clone()
}

func (e *Env) clone() *Env {
	if e == nil {
		return nil
	}
	cp := *e
	cp.Files = slices.Clone(e.Files)
	for _, s := range []*[]string{&cp.Skills, &cp.Tools, &cp.MCP, &cp.Agents, &cp.Used, &cp.UsedMCP, &cp.Known} {
		*s = slices.Clone(*s)
	}
	return &cp
}

func (f *File) env(at time.Time) *Env {
	if f.Env == nil {
		f.Env = &Env{}
	}
	if at.After(f.Env.At) {
		f.Env.At = at
	}
	return f.Env
}

// envKinds are the attachments Env reads; every other one (session_context, hooks, files, queued prompts …) carries
// personal or secret values and is never decoded.
var envKinds = []string{"instructions", "skill_listing", "invoked_skills", "deferred_tools_delta", "mcp_instructions_delta",
	"agent_listing_delta", "environment", "model"}

var attachmentObject, attachmentLine, typeKey = []byte(`"attachment":{`), []byte(`"type":"attachment"`), []byte(`"type":"`)

// isEnv is a cheap pre-filter for the attachments Env reads: one pass over a line that is none, and the attachment's
// type read where Claude writes it, first in its object.
func isEnv(b []byte) bool {
	i := bytes.Index(b, attachmentObject)
	if i < 0 {
		return false
	}
	if rest, ok := bytes.CutPrefix(b[i+len(attachmentObject):], typeKey); ok {
		if end := bytes.IndexByte(rest, '"'); end >= 0 {
			return slices.Contains(envKinds, string(rest[:end]))
		}
	}
	if !bytes.Contains(b, attachmentLine) {
		return false
	}
	for _, k := range envKinds {
		if bytes.Contains(b, []byte(`"`+k+`"`)) {
			return true
		}
	}
	return false
}

func (f *File) takeEnv(b []byte) {
	var l struct {
		Type       string    `json:"type"`
		Timestamp  time.Time `json:"timestamp"`
		Version    string    `json:"version"`
		Attachment struct {
			Type  string `json:"type"`
			Files []struct {
				Path    string `json:"path"`
				Type    string `json:"type"`
				Content string `json:"content"`
			} `json:"files"`
			Removed   []string `json:"removed"`
			Changed   bool     `json:"changed"`
			Names     []string `json:"names"`
			IsInitial bool     `json:"isInitial"`
			Skills    []struct {
				Name string `json:"name"`
			} `json:"skills"`
			AddedNames   []string `json:"addedNames"`
			RemovedNames []string `json:"removedNames"`
			AddedTypes   []string `json:"addedTypes"`
			RemovedTypes []string `json:"removedTypes"`
			Snapshot     *struct {
				Platform   string `json:"platform"`
				Shell      string `json:"shell"`
				IsWorktree bool   `json:"isWorktree"`
			} `json:"snapshot"`
			Identity struct {
				ModelID string `json:"modelId"`
			} `json:"identity"`
		} `json:"attachment"`
	}
	if json.Unmarshal(b, &l) != nil || l.Type != "attachment" {
		return
	}
	a := &l.Attachment
	var e *Env
	switch a.Type {
	case "instructions":
		e = f.env(l.Timestamp)
		if !a.Changed {
			e.Files = nil
		}
		for _, p := range a.Removed {
			e.Files = slices.DeleteFunc(e.Files, func(x EnvFile) bool { return x.Path == p })
		}
		for _, x := range a.Files {
			if strings.HasSuffix(x.Type, "Mem") { // AutoMem, TeamMem: memory, not instructions
				continue
			}
			nf := EnvFile{Path: x.Path, Type: x.Type, Norm: NormSHA([]byte(x.Content))}
			if i := slices.IndexFunc(e.Files, func(y EnvFile) bool { return y.Path == x.Path }); i >= 0 {
				e.Files[i] = nf
			} else if len(e.Files) < envFilesCap {
				e.Files = append(e.Files, nf)
			}
		}
		e.known("files")
	case "skill_listing":
		if a.Names == nil {
			return
		}
		e = f.env(l.Timestamp)
		if a.IsInitial {
			e.Skills = nil
		}
		e.Skills = addNames(e.Skills, a.Names...)
		e.known("skills")
	case "invoked_skills":
		e = f.env(l.Timestamp)
		for _, s := range a.Skills {
			e.Used = addNames(e.Used, s.Name)
		}
	case "deferred_tools_delta":
		e = f.env(l.Timestamp)
		e.Tools = dropNames(addNames(e.Tools, a.AddedNames...), a.RemovedNames)
		e.known("tools")
	case "mcp_instructions_delta":
		e = f.env(l.Timestamp)
		e.MCP = dropNames(addNames(e.MCP, a.AddedNames...), a.RemovedNames)
		e.known("mcp")
	case "agent_listing_delta":
		e = f.env(l.Timestamp)
		if a.IsInitial {
			e.Agents = nil
		}
		e.Agents = dropNames(addNames(e.Agents, a.AddedTypes...), a.RemovedTypes)
		e.known("agents")
	case "environment":
		if a.Snapshot == nil {
			return
		}
		e = f.env(l.Timestamp)
		e.Platform, e.Shell, e.Worktree = a.Snapshot.Platform, a.Snapshot.Shell, a.Snapshot.IsWorktree
	case "model":
		if a.Identity.ModelID == "" {
			return
		}
		e = f.env(l.Timestamp)
		e.Model = a.Identity.ModelID
	default:
		return
	}
	if l.Version != "" {
		e.Version = l.Version
	}
}

func (e *Env) known(set string) { e.Known = addNames(e.Known, set) }

var useNames = [][]byte{[]byte(`"name":"Skill"`), []byte(`"name":"mcp__`)}

// isUse is a cheap pre-filter, on a line with a tool call, for one that invoked a skill or an MCP tool.
func isUse(b []byte) bool {
	return bytes.Contains(b, useNames[0]) || bytes.Contains(b, useNames[1])
}

// takeUses reads the skill names and MCP servers of a Claude assistant line's tool calls, never their other input.
func (f *File) takeUses(b []byte) {
	var l struct {
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Version   string    `json:"version"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(b, &l) != nil || l.Type != "assistant" || len(l.Message.Content) == 0 || l.Message.Content[0] != '[' {
		return
	}
	var blocks []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Input struct {
			Skill string `json:"skill"`
		} `json:"input"`
	}
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return
	}
	for _, bl := range blocks {
		if bl.Type != "tool_use" {
			continue
		}
		if bl.Name == "Skill" && bl.Input.Skill != "" {
			e := f.env(l.Timestamp)
			e.Used = addNames(e.Used, bl.Input.Skill)
		} else if server, _, ok := strings.Cut(strings.TrimPrefix(bl.Name, "mcp__"), "__"); ok && strings.HasPrefix(bl.Name, "mcp__") {
			e := f.env(l.Timestamp)
			e.UsedMCP = addNames(e.UsedMCP, server)
		}
	}
	if f.Env != nil && l.Version != "" {
		f.Env.Version = l.Version
	}
}

// takeMode keeps the permission mode a Claude permission-mode line or prompt carries.
func (f *File) takeMode(l *line) {
	if l.PermissionMode != "" && f.Provider == tend.ProviderClaude {
		f.env(l.Timestamp).Mode = l.PermissionMode
	}
}

// Permission is the mode the session last recorded.
func (e *Env) Permission() tend.Permission {
	if e == nil {
		return tend.Permission{}
	}
	return tend.Permission{Mode: e.Mode, Approval: e.Approval, Sandbox: e.Sandbox}
}

// takeVersion keeps the CLI version a Claude line carries.
func (f *File) takeVersion(l *line) {
	if l.Version != "" && f.Provider == tend.ProviderClaude {
		f.env(l.Timestamp).Version = l.Version
	}
}

// takeCodex reads the CLI, provider, model and policies of a Codex session_meta or turn_context line.
func (f *File) takeCodex(l *line) {
	p := &l.Payload
	switch l.Type {
	case "session_meta":
		if p.CLIVersion == "" && p.ModelProvider == "" {
			return
		}
		e := f.env(l.Timestamp)
		e.Version, e.Provider = p.CLIVersion, p.ModelProvider
	case "turn_context":
		e := f.env(l.Timestamp)
		if p.Model != "" {
			e.Model = p.Model
		}
		e.Approval, e.Sandbox = p.ApprovalPolicy, sandboxMode(p.SandboxPolicy)
	}
}

// sandboxMode is a sandbox_policy's mode: a string, or an object's type (older: mode).
func sandboxMode(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct{ Type, Mode string }
	json.Unmarshal(raw, &o)
	if o.Type != "" {
		return o.Type
	}
	return o.Mode
}

// addNames is set (sorted, unique) with names added, up to envNamesCap.
func addNames(set []string, names ...string) []string {
	for _, n := range names {
		if n == "" {
			continue
		}
		i, found := slices.BinarySearch(set, n)
		if !found && len(set) < envNamesCap {
			set = slices.Insert(set, i, n)
		}
	}
	return set
}

func dropNames(set, names []string) []string {
	return slices.DeleteFunc(set, func(n string) bool { return slices.Contains(names, n) })
}
