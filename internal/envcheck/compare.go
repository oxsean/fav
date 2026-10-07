package envcheck

import (
	"cmp"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// How much a difference matters: a block stops a migration, an unequal one changes what the AI finds, a hint is
// worth knowing.
const (
	LevelBlock   = "block"
	LevelUnequal = "unequal"
	LevelHint    = "hint"
)

// Where an item's source side comes from: what the session saw, the source's settings now, or nothing readable.
const (
	EvidenceSession = "session"
	EvidenceConfig  = "config"
	EvidenceUnknown = "unknown"
)

// Item is one difference between the source and the target.
type Item struct {
	Dim      string    `json:"dim"` // session | cli | code | files | skills | mcp | project | provider | unknown
	Level    string    `json:"level"`
	Name     string    `json:"name"`            // what it is about: a CLI, a file's kind:name, a skill, a server, a setting
	Here     string    `json:"here,omitempty"`  // the source's value
	There    string    `json:"there,omitempty"` // the target's
	Evidence string    `json:"evidence"`
	At       time.Time `json:"at,omitzero"` // when the evidence was taken
	What     string    `json:"what"`
	Fix      string    `json:"fix,omitempty"` // what to do by hand; tend does none of it
}

// Report is Compare's answer, most severe first.
type Report struct {
	Items   []Item `json:"items"`
	Block   int    `json:"block"`
	Unequal int    `json:"unequal"`
	Hint    int    `json:"hint"`
}

func (r Report) Blocked() bool { return r.Block > 0 }

// Summary is the one line a dialog shows before the items.
func (r Report) Summary() string { return i18n.F("envcheck.summary", r.Block, r.Unequal, r.Hint) }

var (
	levelKeys    = map[string]string{LevelBlock: "envcheck.level_block", LevelUnequal: "envcheck.level_unequal", LevelHint: "envcheck.level_hint"}
	evidenceKeys = map[string]string{EvidenceSession: "envcheck.evidence_session", EvidenceConfig: "envcheck.evidence_config",
		EvidenceUnknown: "envcheck.evidence_unknown"}
)

// LevelText and EvidenceText are how a level and an evidence are shown.
func LevelText(level string) string       { return i18n.T(levelKeys[level]) }
func EvidenceText(evidence string) string { return i18n.T(evidenceKeys[evidence]) }

var (
	levelOrder = []string{LevelBlock, LevelUnequal, LevelHint}
	dimOrder   = []string{"session", "cli", "code", "files", "skills", "mcp", "project", "provider", "unknown"}
)

// Compare is what an agent would find different on dst (the target, at to) from src (the source, at from, with Seen
// when asked about a session): what the session saw first, the source's settings when it recorded nothing; a side
// that could not be read is unknown, never equal. Windows and WSL on one machine share their files, as one directory
// does with itself: the code is not compared.
func Compare(src, dst Print, from, to pathmap.End) Report {
	c := &comparison{src: src, dst: dst, seen: src.Seen, items: []Item{}}
	provider := ""
	if c.seen != nil {
		provider = c.seen.CLI
	}
	claude, codex := provider != tend.ProviderCodex, provider != tend.ProviderClaude
	c.session()
	c.clis(provider)
	if !neighbours(from, to) && (from != to || src.Dir != dst.Dir) {
		c.code()
	}
	c.files(from, to)
	if claude {
		c.skills()
		c.mcp(src.MCP, dst.MCP, "", true)
	}
	if codex {
		c.mcp(src.CodexMCP, dst.CodexMCP, "codex:", false)
	}
	c.project(claude, codex)
	c.provider(claude, codex)
	c.unknowns()
	r := Report{Items: c.items}
	slices.SortStableFunc(r.Items, func(a, b Item) int {
		if d := slices.Index(levelOrder, a.Level) - slices.Index(levelOrder, b.Level); d != 0 {
			return d
		}
		return slices.Index(dimOrder, a.Dim) - slices.Index(dimOrder, b.Dim)
	})
	for _, it := range r.Items {
		switch it.Level {
		case LevelBlock:
			r.Block++
		case LevelUnequal:
			r.Unequal++
		default:
			r.Hint++
		}
	}
	return r
}

type comparison struct {
	src, dst Print
	seen     *Seen
	items    []Item
}

func (c *comparison) add(it Item) {
	if it.Evidence == "" {
		it.Evidence = EvidenceConfig
	}
	if it.At.IsZero() && it.Evidence == EvidenceConfig {
		it.At = c.src.At
	}
	c.items = append(c.items, it)
}

// neighbours: Windows and a WSL distro on the same machine.
func neighbours(a, b pathmap.End) bool {
	return a.Host != "" && a.Host == b.Host && a.WSL != b.WSL && (a.OS == "windows" || b.OS == "windows")
}

func (c *comparison) session() {
	if s := c.seen; s != nil && len(s.Known) == 0 && s.Version == "" {
		c.add(Item{Dim: "session", Level: LevelHint, Name: s.CLI, Evidence: EvidenceUnknown, What: i18n.T("envcheck.session_unknown")})
	}
}

func cliOf(cs []CLI, name string) CLI {
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	return CLI{Name: name}
}

func (c *comparison) clis(provider string) {
	var names []string
	if provider != "" {
		names = []string{provider}
	}
	for _, x := range c.src.CLIs {
		if provider == "" && x.Found {
			names = append(names, x.Name)
		}
	}
	for _, name := range names {
		s, d := cliOf(c.src.CLIs, name), cliOf(c.dst.CLIs, name)
		if !d.Found {
			c.add(Item{Dim: "cli", Level: LevelBlock, Name: name, Here: s.Version, At: c.dst.At,
				What: i18n.F("envcheck.cli_missing", name), Fix: i18n.F("envcheck.cli_missing_fix", name)})
			continue
		}
		here, evidence, at := s.Version, EvidenceConfig, c.src.At
		if c.seen != nil && c.seen.CLI == name && c.seen.Version != "" {
			here, evidence, at = c.seen.Version, EvidenceSession, c.seen.At
		}
		switch {
		case d.Version == "":
			c.add(Item{Dim: "cli", Level: LevelHint, Name: name, Here: here, Evidence: EvidenceUnknown, What: i18n.F("envcheck.cli_version_unknown", name)})
		case here == "":
			c.add(Item{Dim: "cli", Level: LevelHint, Name: name, There: d.Version, Evidence: EvidenceUnknown,
				What: i18n.F("envcheck.cli_version_unknown_here", name)})
		case older(d.Version, here):
			c.add(Item{Dim: "cli", Level: LevelUnequal, Name: name, Here: here, There: d.Version, Evidence: evidence, At: at,
				What: i18n.F("envcheck.cli_older", name, d.Version, here), Fix: i18n.F("envcheck.cli_older_fix", name)})
		}
	}
}

// older: version a comes before b, compared number by number.
func older(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func short(sha string) string { return sha[:min(len(sha), 8)] }

// remoteKey is g's origin in the form both of its spellings share; a tend that left RemoteKey out sent the URL alone.
func remoteKey(g Git) string { return cmp.Or(g.RemoteKey, task.RemoteKey(g.Remote)) }

func (c *comparison) code() {
	s, d := c.src.Git, c.dst.Git
	if c.src.Dir == "" || slices.Contains(c.src.Unknown, "git") || slices.Contains(c.dst.Unknown, "git") {
		return
	}
	switch {
	case c.dst.NoDir:
		level := LevelUnequal
		if s.Repo {
			level = LevelBlock
		}
		c.add(Item{Dim: "code", Level: level, Name: "dir", There: c.dst.Dir, At: c.dst.At,
			What: i18n.F("envcheck.no_dir", c.dst.Dir), Fix: i18n.T("envcheck.no_dir_fix")})
		return
	case !s.Repo:
		return
	case !d.Repo:
		c.add(Item{Dim: "code", Level: LevelBlock, Name: "repo", There: c.dst.Dir, At: c.dst.At,
			What: i18n.F("envcheck.not_repo", c.dst.Dir), Fix: i18n.T("envcheck.no_dir_fix")})
		return
	}
	if here, there := remoteKey(s), remoteKey(d); here != "" && here != there {
		c.add(Item{Dim: "code", Level: LevelUnequal, Name: "remote", Here: here, There: there, At: c.dst.At,
			What: i18n.F("envcheck.remote", here, orNone(there)), Fix: i18n.T("envcheck.remote_fix")})
	}
	if s.Branch != d.Branch {
		c.add(Item{Dim: "code", Level: LevelUnequal, Name: "branch", Here: s.Branch, There: d.Branch,
			What: i18n.F("envcheck.branch", s.Branch, d.Branch), Fix: i18n.F("envcheck.branch_fix", s.Branch)})
	}
	if s.Head != d.Head {
		c.add(Item{Dim: "code", Level: LevelUnequal, Name: "head", Here: s.Head, There: d.Head,
			What: i18n.F("envcheck.head", short(s.Head), short(d.Head)), Fix: i18n.T("envcheck.head_fix")})
	}
	if s.Dirty > 0 {
		c.add(Item{Dim: "code", Level: LevelUnequal, Name: "dirty", Here: strings.Join(s.DirtyList, " "),
			What: i18n.F("envcheck.dirty", s.Dirty), Fix: i18n.T("envcheck.dirty_fix")})
	}
	if s.Unpushed > 0 {
		c.add(Item{Dim: "code", Level: LevelUnequal, Name: "unpushed", Here: strconv.Itoa(s.Unpushed),
			What: i18n.F("envcheck.unpushed", s.Unpushed), Fix: i18n.T("envcheck.unpushed_fix")})
	}
	if d.Dirty > 0 {
		c.add(Item{Dim: "code", Level: LevelUnequal, Name: "dirty_there", There: strings.Join(d.DirtyList, " "), At: c.dst.At,
			What: i18n.F("envcheck.dirty_there", d.Dirty), Fix: i18n.T("envcheck.dirty_there_fix")})
	}
}

func fileKey(f File) string { return f.Kind + ":" + f.Name }

// label is how a file is shown: under the CLI's directory, the home, or the project.
func label(f File) string {
	switch f.Kind {
	case KindClaude:
		return ".claude/" + f.Name
	case KindCodex:
		return ".codex/" + f.Name
	case KindHome:
		return filepath.ToSlash(filepath.Join("~", f.Name))
	}
	return f.Name
}

func (c *comparison) files(from, to pathmap.End) {
	dirGone := c.dst.NoDir // the code's block says so: its files are not listed one by one
	there := map[string]File{}
	for _, f := range c.dst.Files {
		there[fileKey(f)] = f
	}
	seen := map[string]bool{}
	if c.seen.knows("files") {
		for _, f := range c.seen.Files {
			seen[fileKey(f)] = true
		}
	}
	paired := map[string]bool{}
	for _, s := range c.src.Files {
		if dirGone && s.Kind == KindDir {
			continue
		}
		k := fileKey(s)
		if s.Kind == KindPath {
			if p, ok := pathmap.Map(s.Name, from, to); ok {
				k = KindPath + ":" + p
			}
		}
		evidence, at := EvidenceConfig, c.src.At
		if seen[fileKey(s)] {
			evidence, at = EvidenceSession, c.seen.At
		}
		d, ok := there[k]
		paired[k] = true
		switch {
		case !ok:
			c.add(Item{Dim: "files", Level: LevelUnequal, Name: fileKey(s), Here: s.Norm, Evidence: evidence, At: at,
				What: i18n.F("envcheck.file_missing", label(s)), Fix: i18n.T("envcheck.file_missing_fix")})
		case s.Norm != d.Norm:
			c.add(Item{Dim: "files", Level: LevelUnequal, Name: fileKey(s), Here: s.Norm, There: d.Norm,
				What: i18n.F("envcheck.file_differs", label(s)), Fix: i18n.T("envcheck.file_differs_fix")})
		case s.SHA != d.SHA:
			c.add(Item{Dim: "files", Level: LevelHint, Name: fileKey(s), Here: s.SHA, There: d.SHA, Evidence: evidence, At: at,
				What: i18n.F("envcheck.file_line_ends", label(s))})
		}
	}
	for _, d := range c.dst.Files {
		if !paired[fileKey(d)] {
			c.add(Item{Dim: "files", Level: LevelUnequal, Name: fileKey(d), There: d.Norm, At: c.dst.At,
				What: i18n.F("envcheck.file_extra", label(d)), Fix: i18n.T("envcheck.file_extra_fix")})
		}
	}
}

func (c *comparison) skills() {
	for _, name := range c.src.Skills {
		if slices.Contains(c.dst.Skills, name) {
			continue
		}
		used := c.seen != nil && slices.Contains(c.seen.Used, name)
		level, evidence, at := LevelHint, EvidenceConfig, c.src.At
		if used {
			level = LevelUnequal
		}
		if used || c.seen.knows("skills") && slices.Contains(c.seen.Skills, name) {
			evidence, at = EvidenceSession, c.seen.At
		}
		c.add(Item{Dim: "skills", Level: level, Name: name, Here: name, Evidence: evidence, At: at,
			What: i18n.F("envcheck.skill_missing", name), Fix: i18n.T("envcheck.skill_missing_fix")})
	}
}

// toolName is a server's name as Claude spells it in its tools' names (mcp__<server>__<tool>).
func toolName(server string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, server)
}

func (c *comparison) mcp(here, there []string, prefix string, claude bool) {
	var used []string
	if claude && c.seen != nil {
		used = c.seen.UsedMCP
	}
	for _, name := range here {
		if slices.Contains(there, name) {
			continue
		}
		u := slices.Contains(used, toolName(name))
		level, evidence, at := LevelHint, EvidenceConfig, c.src.At
		if u {
			level = LevelUnequal
		}
		if u || claude && c.seen.knows("mcp") && slices.Contains(c.seen.MCP, name) {
			evidence, at = EvidenceSession, c.seen.At
		}
		c.add(Item{Dim: "mcp", Level: level, Name: prefix + name, Here: name, Evidence: evidence, At: at,
			What: i18n.F("envcheck.mcp_missing", name), Fix: i18n.T("envcheck.mcp_missing_fix")})
	}
	for _, u := range used {
		if !slices.ContainsFunc(here, func(n string) bool { return toolName(n) == u }) {
			c.add(Item{Dim: "mcp", Level: LevelHint, Name: u, Evidence: EvidenceUnknown, What: i18n.F("envcheck.mcp_unseen", u)})
		}
	}
}

// missing is what a has and b has not.
func missing(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return i18n.T("envcheck.none")
	}
	return s
}

func (c *comparison) project(claude, codex bool) {
	s, d := c.src.Project, c.dst.Project
	if c.src.Dir == "" || c.dst.NoDir {
		return
	}
	if claude {
		if m := missing(s.AllowedTools, d.AllowedTools); len(m) > 0 {
			c.add(Item{Dim: "project", Level: LevelUnequal, Name: "allowed_tools", Here: strings.Join(m, " "),
				What: i18n.F("envcheck.allowed_tools", strings.Join(m, ", ")), Fix: i18n.T("envcheck.allowed_tools_fix")})
		}
		if m := missing(s.MCPJSON, d.MCPJSON); len(m) > 0 {
			c.add(Item{Dim: "project", Level: LevelUnequal, Name: "mcp_json", Here: strings.Join(m, " "),
				What: i18n.F("envcheck.mcp_json", strings.Join(m, ", ")), Fix: i18n.T("envcheck.allowed_tools_fix")})
		}
		if s.Trusted && !d.Trusted {
			c.add(Item{Dim: "project", Level: LevelUnequal, Name: "trusted", What: i18n.T("envcheck.trusted"), Fix: i18n.T("envcheck.trusted_fix")})
		}
	}
	if codex && s.CodexTrust != "" && s.CodexTrust != d.CodexTrust {
		c.add(Item{Dim: "project", Level: LevelUnequal, Name: "codex_trust", Here: s.CodexTrust, There: d.CodexTrust,
			What: i18n.F("envcheck.codex_trust", s.CodexTrust, orNone(d.CodexTrust)), Fix: i18n.T("envcheck.trusted_fix")})
	}
}

// codexProvider is a model_provider as Codex reads it: unset is openai.
func codexProvider(p string) string { return cmp.Or(p, "openai") }

func (c *comparison) provider(claude, codex bool) {
	s, d := c.src.Provider, c.dst.Provider
	if claude {
		if m := missing(s.ClaudeEnv, d.ClaudeEnv); len(m) > 0 {
			c.add(Item{Dim: "provider", Level: LevelUnequal, Name: "claude_env", Here: strings.Join(m, " "),
				What: i18n.F("envcheck.claude_env", strings.Join(m, ", ")), Fix: i18n.T("envcheck.claude_env_fix")})
		}
		if s.ClaudeHost != d.ClaudeHost {
			c.add(Item{Dim: "provider", Level: LevelUnequal, Name: "claude_host", Here: s.ClaudeHost, There: d.ClaudeHost,
				What: i18n.F("envcheck.claude_host", orNone(s.ClaudeHost), orNone(d.ClaudeHost)), Fix: i18n.T("envcheck.provider_fix")})
		}
	}
	if !codex {
		return
	}
	here, evidence, at := codexProvider(s.Codex), EvidenceConfig, c.src.At
	if c.seen != nil && c.seen.CLI == tend.ProviderCodex && c.seen.Provider != "" {
		here, evidence, at = c.seen.Provider, EvidenceSession, c.seen.At
	}
	if there := codexProvider(d.Codex); here != there {
		c.add(Item{Dim: "provider", Level: LevelUnequal, Name: "codex", Here: here, There: there, Evidence: evidence, At: at,
			What: i18n.F("envcheck.codex_provider", here, there), Fix: i18n.T("envcheck.provider_fix")})
	}
	if s.CodexHost != d.CodexHost {
		c.add(Item{Dim: "provider", Level: LevelUnequal, Name: "codex_host", Here: s.CodexHost, There: d.CodexHost,
			What: i18n.F("envcheck.codex_host", orNone(s.CodexHost), orNone(d.CodexHost)), Fix: i18n.T("envcheck.provider_fix")})
	}
}

// sources are how an Unknown entry is shown.
var sources = map[string]string{"git": "git", "claude_json": ".claude.json", "claude_settings": "settings.json",
	"plugins": "installed_plugins.json", "mcp_json": ".mcp.json", "codex_config": "config.toml"}

func source(u string) string {
	if s, ok := sources[u]; ok {
		return s
	}
	if rest, ok := strings.CutPrefix(u, "file:"); ok {
		kind, name, _ := strings.Cut(rest, ":")
		return label(File{Kind: kind, Name: name})
	}
	return u
}

func (c *comparison) unknowns() {
	for _, side := range []struct {
		unknown []string
		key     string
		at      time.Time
	}{{c.src.Unknown, "envcheck.unknown_here", c.src.At}, {c.dst.Unknown, "envcheck.unknown_there", c.dst.At}} {
		for _, u := range side.unknown {
			if strings.HasPrefix(u, "cli:") { // the CLI's own item says it
				continue
			}
			c.add(Item{Dim: "unknown", Level: LevelHint, Name: u, Evidence: EvidenceUnknown, At: side.at, What: i18n.F(side.key, source(u))})
		}
	}
}
