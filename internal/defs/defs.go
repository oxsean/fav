// Package defs reads and writes agent definitions: Markdown with a YAML front matter, the shape Claude Code's
// subagents use, and compiles one into the agent profile a run is frozen with.
package defs

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/tend"
)

// Roles a definition plays in a workflow.
var Roles = []string{"planner", "implement", "review", "test", "any"}

// Efforts an agent may be run with.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// Outputs a definition may ask its runs for.
var Outputs = []string{"none", "verdict", "plan"}

// AgentDef is who runs a task, with what model, permissions, skills and hooks, and what it is told.
type AgentDef struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Role        string         `json:"role,omitempty" yaml:"role,omitempty"`
	Provider    string         `json:"provider,omitempty" yaml:"provider,omitempty"`
	Profile     string         `json:"profile,omitempty" yaml:"profile,omitempty"` // a configured profile it starts from
	Model       string         `json:"model,omitempty" yaml:"model,omitempty"`
	Effort      string         `json:"effort,omitempty" yaml:"effort,omitempty"`
	Permission  string         `json:"permission,omitempty" yaml:"permission,omitempty"`
	Skills      []string       `json:"skills,omitempty" yaml:"skills,omitempty"`
	Tools       Tools          `json:"tools,omitzero" yaml:"tools,omitempty"`
	MCP         []string       `json:"mcp,omitempty" yaml:"mcp,omitempty"`
	Hooks       map[string]any `json:"hooks,omitempty" yaml:"hooks,omitempty"`
	Machines    Machines       `json:"machines,omitzero" yaml:"machines,omitempty"`
	Output      string         `json:"output,omitempty" yaml:"output,omitempty"`
	Budget      Budget         `json:"budget,omitzero" yaml:"budget,omitempty"`
	Import      string         `json:"import,omitempty" yaml:"import,omitempty"` // a Claude Code subagent file it builds on
	Body        string         `json:"body,omitempty" yaml:"-"`                  // what its runs are told
	Extra       map[string]any `json:"extra,omitempty" yaml:"-"`                 // fields this version does not know, kept
}

type Tools struct {
	Allow []string `json:"allow,omitempty" yaml:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty" yaml:"deny,omitempty"`
}

type Machines struct {
	Prefer  []string `json:"prefer,omitempty" yaml:"prefer,omitempty"`
	Require []string `json:"require,omitempty" yaml:"require,omitempty"`
}

type Budget struct {
	USD     float64 `json:"usd,omitempty" yaml:"usd,omitempty"`
	Tokens  int64   `json:"tokens,omitempty" yaml:"tokens,omitempty"`
	Minutes int     `json:"minutes,omitempty" yaml:"minutes,omitempty"`
}

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	toolRe = regexp.MustCompile(`^[A-Za-z0-9_*:().-]{1,128}$`)
	known  = []string{"name", "description", "role", "provider", "profile", "model", "effort", "permission", "skills",
		"tools", "mcp", "hooks", "machines", "output", "budget", "import", "color"}
)

// Parse reads a definition from its Markdown. A front matter field it does not know is kept in Extra.
func Parse(text []byte) (AgentDef, error) {
	var d AgentDef
	head, body, err := split(text)
	if err != nil {
		return d, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(head, &node); err != nil {
		return d, fmt.Errorf("front matter: %w", err)
	}
	var raw map[string]any
	if err := node.Decode(&raw); err != nil {
		return d, fmt.Errorf("front matter: %w", err)
	}
	if t, ok := raw["tools"].(string); ok { // Claude Code: "Read, Grep, Glob"
		raw["tools"] = map[string]any{"allow": splitList(t)}
	}
	if t, ok := raw["tools"].([]any); ok {
		raw["tools"] = map[string]any{"allow": t}
	}
	b, _ := yaml.Marshal(raw)
	if err := yaml.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("front matter: %w", err)
	}
	for k, v := range raw {
		if !slices.Contains(known, k) {
			if d.Extra == nil {
				d.Extra = map[string]any{}
			}
			d.Extra[k] = v
		}
	}
	d.Body = strings.TrimSpace(string(body))
	for _, l := range []*[]string{&d.Skills, &d.Tools.Allow, &d.Tools.Deny, &d.MCP, &d.Machines.Prefer, &d.Machines.Require} {
		if len(*l) == 0 {
			*l = nil
		}
	}
	if len(d.Hooks) == 0 {
		d.Hooks = nil
	}
	return d, nil
}

func split(text []byte) (head, body []byte, err error) {
	text = bytes.TrimPrefix(text, []byte("\ufeff"))
	s := strings.ReplaceAll(string(text), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, nil, errors.New("no front matter: the file starts with ---")
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, nil, errors.New("front matter never ends: a line of --- closes it")
	}
	body = []byte(rest[end+4:])
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = nil
	}
	return []byte(rest[:end]), body, nil
}

func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// Format writes d back as Markdown; Parse(Format(d)) is d.
func Format(d AgentDef) []byte {
	var head map[string]any
	b, _ := yaml.Marshal(d)
	yaml.Unmarshal(b, &head)
	for k, v := range d.Extra {
		head[k] = v
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	enc.Encode(ordered(head))
	enc.Close()
	buf.WriteString("---\n")
	if d.Body != "" {
		buf.WriteString(d.Body + "\n")
	}
	return buf.Bytes()
}

// ordered is m as a YAML mapping with the known fields first, in their order, then the rest by name.
func ordered(m map[string]any) *yaml.Node {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		if i := slices.Index(known, k); i >= 0 {
			return i
		}
		return len(known)
	}
	sort.Slice(keys, func(i, j int) bool {
		if rank(keys[i]) != rank(keys[j]) {
			return rank(keys[i]) < rank(keys[j])
		}
		return keys[i] < keys[j]
	})
	n := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range keys {
		var v yaml.Node
		v.Encode(m[k])
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &v)
	}
	return n
}

// Check finds what is wrong with d (errors) and what will not take effect (warnings).
func Check(d AgentDef) (errs, warnings []string) {
	bad := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if !nameRe.MatchString(d.Name) {
		bad("name %q: lowercase letters, digits, . _ -, at most 64", d.Name)
	}
	if d.Role != "" && !slices.Contains(Roles, d.Role) {
		bad("role %q: one of %s", d.Role, strings.Join(Roles, ", "))
	}
	if d.Provider == "" && d.Profile == "" {
		bad("provider or profile is needed")
	}
	if d.Effort != "" && !slices.Contains(Efforts, d.Effort) {
		bad("effort %q: one of %s", d.Effort, strings.Join(Efforts, ", "))
	}
	if d.Output != "" && !slices.Contains(Outputs, d.Output) {
		bad("output %q: one of %s", d.Output, strings.Join(Outputs, ", "))
	}
	for _, t := range append(slices.Clone(d.Tools.Allow), d.Tools.Deny...) {
		if !toolRe.MatchString(t) {
			bad("tool %q", t)
		}
	}
	if len(d.Body) > 64<<10 {
		bad("the instructions exceed 64 KiB")
	}
	if len(d.Tools.Allow) > 0 {
		warnings = append(warnings, "tools.allow is not applied: it would widen what the node allows")
	}
	if len(d.Skills) > 0 || len(d.MCP) > 0 || len(d.Hooks) > 0 {
		warnings = append(warnings, "skills, mcp and hooks apply to claude only: skills must be installed on the machine, mcp names its node.mcp servers, hooks need node.allow_hooks")
	}
	if d.Import != "" {
		warnings = append(warnings, "import is read when the definition is imported, not later")
	}
	for k := range d.Extra {
		if k != "color" {
			warnings = append(warnings, "unknown field "+k+" is kept")
		}
	}
	sort.Strings(warnings)
	return errs, warnings
}

// Import fills what d leaves out from the Claude Code subagent file it names (import:), read from disk now.
func Import(d AgentDef, readFile func(string) ([]byte, error)) (AgentDef, error) {
	if d.Import == "" {
		return d, nil
	}
	b, err := readFile(paths.Expand(d.Import))
	if err != nil {
		return d, err
	}
	base, err := Parse(b)
	if err != nil {
		return d, fmt.Errorf("%s: %w", d.Import, err)
	}
	if d.Description == "" {
		d.Description = base.Description
	}
	if d.Model == "" && base.Model != "inherit" {
		d.Model = base.Model
	}
	if len(d.Tools.Allow) == 0 && len(d.Tools.Deny) == 0 {
		d.Tools = base.Tools
	}
	if d.Body == "" {
		d.Body = base.Body
	}
	if d.Provider == "" && d.Profile == "" {
		d.Provider = tend.ProviderClaude
	}
	return d, nil
}

// Compile is the profile a run of d is frozen with: d's settings over the profile it starts from (base finds a
// configured one). The instructions go with the brief.
func Compile(d AgentDef, base func(string) (tend.AgentProfile, bool)) (tend.AgentProfile, error) {
	p := tend.AgentProfile{Provider: d.Provider}
	if d.Profile != "" {
		b, ok := base(d.Profile)
		if !ok {
			return p, fmt.Errorf("profile %s", d.Profile)
		}
		p = b
		if d.Provider != "" && d.Provider != b.Provider {
			return p, fmt.Errorf("provider %s differs from profile %s's %s", d.Provider, d.Profile, b.Provider)
		}
	}
	p.Name = d.Name
	if d.Model != "" {
		p.Model = d.Model
	}
	if d.Permission != "" {
		p.Permission = d.Permission
	}
	if d.Effort != "" {
		p.Effort = d.Effort
	}
	if len(d.Tools.Deny) > 0 {
		p.Deny = slices.Clone(d.Tools.Deny)
	}
	if len(d.Machines.Require) == 1 {
		p.Machine = d.Machines.Require[0]
	}
	p.Hooks, p.MCP, p.Skills = d.Hooks, slices.Clone(d.MCP), slices.Clone(d.Skills)
	return p, nil
}

// Split cuts a definition file into its YAML front matter and its Markdown body.
func Split(text []byte) (head, body []byte, err error) { return split(text) }
