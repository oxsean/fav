package envcheck

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/tend"
)

// The roots a File's Name is under.
const (
	KindClaude = "claude" // Claude's config directory
	KindCodex  = "codex"  // Codex's home
	KindHome   = "home"
	KindDir    = "dir" // the directory asked about
	KindPath   = "path"
)

const (
	cliTimeout = 5 * time.Second
	cliFresh   = time.Minute
	fileCap    = 1 << 20 // bytes read of an instruction file
	dirtyCap   = 30
)

// Collect is this machine's Print for dir ("" for the machine alone): names, versions and hashes, read from a fixed
// list of files and keys. Credential files are never opened; a source that exists but cannot be read is Unknown.
func Collect(ctx context.Context, dir string) Print {
	p := Print{At: time.Now(), Dir: dir}
	if dir != "" && !paths.IsDir(dir) {
		p.NoDir, dir = true, ""
	}
	p.CLIs = clis(ctx, &p)
	if dir != "" {
		g, err := GitOf(ctx, dir)
		if err != nil {
			p.unknown("git")
		}
		p.Git = g
	}
	for _, f := range instructionFiles(dir, p.unknown) {
		p.Files = append(p.Files, f.File)
	}
	p.Skills = skills(dir, &p)
	claudeJSON(dir, &p)
	mcpJSON(dir, &p)
	claudeSettings(dir, &p)
	codexConfig(dir, &p)
	for _, s := range []*[]string{&p.MCP, &p.CodexMCP, &p.Project.AllowedTools, &p.Project.MCPJSON, &p.Provider.ClaudeEnv, &p.Unknown} {
		*s = sorted(*s)
	}
	return p
}

func (p *Print) unknown(what string) { p.Unknown = append(p.Unknown, what) }

func sorted(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}

func clis(ctx context.Context, p *Print) []CLI {
	var out []CLI
	for _, name := range []string{tend.ProviderClaude, tend.ProviderCodex} {
		c := CLI{Name: name}
		if exe, err := exec.LookPath(name); err == nil {
			c.Found = true
			if c.Version = cliVersion(ctx, exe); c.Version == "" {
				p.unknown("cli:" + name)
			}
		}
		out = append(out, c)
	}
	return out
}

var versions = struct {
	sync.Mutex
	m map[string]version
}{m: map[string]version{}}

type version struct {
	v  string
	at time.Time
}

var versionWord = regexp.MustCompile(`\d+(\.\d+)+`)

// cliVersion is what `exe --version` prints first that looks like a version, asked at most once a minute.
func cliVersion(ctx context.Context, exe string) string {
	versions.Lock()
	c, ok := versions.m[exe]
	versions.Unlock()
	if ok && time.Since(c.at) < cliFresh {
		return c.v
	}
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "--version").Output()
	v := ""
	if err == nil {
		v = versionWord.FindString(string(out))
	}
	versions.Lock()
	versions.m[exe] = version{v, time.Now()}
	versions.Unlock()
	return v
}

type located struct {
	File
	path string
}

// instructionFiles are the files the CLIs load as instructions for dir, and the files Claude's ones @-include (one
// level, .md and .txt only), sorted by kind and name; what exists but cannot be read is passed to unknown.
func instructionFiles(dir string, unknown func(string)) []located {
	claude, codex := capture.ClaudeHome(), capture.CodexHome()
	type root struct {
		path    string
		imports bool
	}
	roots := []root{{filepath.Join(claude, "CLAUDE.md"), true}}
	if dir != "" {
		roots = append(roots, root{filepath.Join(dir, "CLAUDE.md"), true}, root{filepath.Join(dir, ".claude", "CLAUDE.md"), true},
			root{filepath.Join(dir, "CLAUDE.local.md"), true})
	}
	roots = append(roots, root{filepath.Join(codex, "AGENTS.md"), false})
	if dir != "" {
		roots = append(roots, root{filepath.Join(dir, "AGENTS.md"), false})
	}
	var out []located
	seen := map[string]bool{}
	add := func(p string) []byte {
		p = paths.Clean(p)
		if seen[p] {
			return nil
		}
		seen[p] = true
		kind, name := nameOf(p, dir)
		b, err := readCapped(p)
		if err != nil {
			if !os.IsNotExist(err) {
				unknown("file:" + kind + ":" + name)
			}
			return nil
		}
		out = append(out, located{File{Kind: kind, Name: name, SHA: sha(b), Norm: index.NormSHA(b)}, p})
		return b
	}
	for _, r := range roots {
		b := add(r.path)
		if b == nil || !r.imports {
			continue
		}
		for _, imp := range imports(b, filepath.Dir(r.path)) {
			add(imp)
		}
	}
	slices.SortFunc(out, func(a, b located) int { return strings.Compare(a.Kind+"\x00"+a.Name, b.Kind+"\x00"+b.Name) })
	return out
}

// readCapped is a regular file's bytes, refused past fileCap.
func readCapped(p string) ([]byte, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > fileCap {
		return nil, os.ErrInvalid
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, fileCap))
}

var importRef = regexp.MustCompile(`(?:^|\s)@(?:"([^"]+)"|(\S+))`)

// imports are the files a CLAUDE.md names with @path outside code, resolved from base (its directory) and the home.
func imports(text []byte, base string) []string {
	var out []string
	fenced := false
	sc := bufio.NewScanner(bytes.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		parts := strings.Split(line, "`")
		for i := 0; i < len(parts); i += 2 { // odd parts are inline code
			for _, m := range importRef.FindAllStringSubmatch(parts[i], -1) {
				ref := m[1] + m[2]
				switch strings.ToLower(filepath.Ext(ref)) {
				case ".md", ".txt":
				default:
					continue
				}
				ref = paths.Expand(ref)
				if !filepath.IsAbs(ref) {
					ref = filepath.Join(base, filepath.FromSlash(ref))
				}
				out = append(out, ref)
			}
		}
	}
	return out
}

// nameOf is p's kind and slash-separated name under it: Claude's or Codex's directory, dir, then the home.
func nameOf(p, dir string) (kind, name string) {
	home, _ := os.UserHomeDir()
	for _, r := range [][2]string{{KindClaude, capture.ClaudeHome()}, {KindCodex, capture.CodexHome()}, {KindDir, dir}, {KindHome, home}} {
		if r[1] == "" {
			continue
		}
		if rel, ok := paths.Inside(r[1], p); ok && rel != "." {
			return r[0], filepath.ToSlash(rel)
		}
	}
	return KindPath, paths.Clean(p)
}

// skills are Claude's skill directories: its own, dir's, and each installed plugin's under the plugin's name.
func skills(dir string, p *Print) []string {
	claude := capture.ClaudeHome()
	out := skillDirs(filepath.Join(claude, "skills"), "")
	if dir != "" {
		out = append(out, skillDirs(filepath.Join(dir, ".claude", "skills"), "")...)
	}
	plugins, err := installedPlugins(filepath.Join(claude, "plugins", "installed_plugins.json"), dir)
	if err != nil {
		p.unknown("plugins")
	}
	for name, dirs := range plugins {
		for _, d := range dirs {
			out = append(out, skillDirs(filepath.Join(d, "skills"), name+":")...)
		}
	}
	return sorted(out)
}

func skillDirs(root, prefix string) []string {
	entries, _ := os.ReadDir(root)
	var out []string
	for _, e := range entries {
		if paths.Exists(filepath.Join(root, e.Name(), "SKILL.md")) {
			out = append(out, prefix+e.Name())
		}
	}
	return out
}
