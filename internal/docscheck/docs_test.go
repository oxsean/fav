// Package docscheck fails when the agent instructions, the READMEs or the /tend skill name a file, symbol, environment
// variable, test or subcommand the code no longer has.
package docscheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var docs = []string{"AGENTS.md", "README.md", "README.zh.md", "skills/tend/SKILL.md"}

// notCode: capitalised words and tokens in backticks that are not Go names.
var notCode = []string{"Width", "Height", "TestName", "RESULT", "tend.sh", "tend.cmd", "Enter", "Esc", "Tab", "Space", "Backspace", "Shift", "Ctrl", "Alt", "PATH", "HOME", "LANG",
	"LC_ALL", "LC_MESSAGES", "NO_COLOR", "HERDR_ENV", "FZF_PROMPT", "FZF_PREVIEW_COLUMNS", "CLAUDE_CODE_SESSION_ID",
	"WSL_DISTRO_NAME", "LOCALAPPDATA", "USERPROFILE", "TMPDIR", "GOOS", "GOARCH", "EDITOR", "Nerd", "Rebased", "Herdr",
	"Claude", "Codex", "ChatGPT", "BM25", "JSON", "JSONL", "UUID", "TODO", "CJK", "IME", "ASCII", "OSC", "ControlMaster",
	"LockFileEx", "OpenSSH", "PowerShell", "WSL", "Debian", "Ubuntu", "OrbStack", "README", "SKILL", "GitHub", "bypassPermissions", "acceptEdits", "dontAsk"}

var (
	backtick = regexp.MustCompile("`([^`\n]+)`")
	pathLike = regexp.MustCompile(`^(internal|cmd|tools|scripts|skills|docs)/[\w./-]*$|^[\w.-]+\.(go|sh|py|toml|yml|yaml|md|js|mjs|css)$`)
	// external: package qualifiers and builtins that are not this module's
	external = regexp.MustCompile(`^(t|tea|syscall|lipgloss|os|filepath|strings|exec|time|json|testing)\.|^len\(`)
	goFile   = regexp.MustCompile(`[\w-]+\.go\b`)
	goRun    = regexp.MustCompile(`go run \./([\w./-]+)`)
	keyName  = regexp.MustCompile(`^([A-Z]|F\d+)$`)
	ident    = regexp.MustCompile(`^(?:[a-z][a-z0-9]*\.)?([A-Za-z_][A-Za-z0-9_]*)(?:\.([A-Za-z_][A-Za-z0-9_]*))?(?:\(\))?$`)
	envVar   = regexp.MustCompile(`^[A-Z][A-Z0-9]*_[A-Z0-9_]+$`)
	testRun  = regexp.MustCompile(`-run '?\^?(Test\w+)`)
	tendCmd  = regexp.MustCompile(`^tend ([a-z][a-z-]+)(?: \[?([a-z][a-z-]+))?`)
	caseLine = regexp.MustCompile(`case ((?:"[^"]*"(?:, )?)+):`)
	quoted   = regexp.MustCompile(`"([^"]*)"`)
	mdLink   = regexp.MustCompile(`\]\(([^)#\s]+\.md)(?:#[^)]*)?\)`)
)

func root(t *testing.T) string {
	r, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// names are every declared Go name: functions, methods, types, fields, constants, variables; and all Go source text.
func names(t *testing.T, root string) (map[string]bool, string) {
	out := map[string]bool{}
	var src strings.Builder
	fset := token.NewFileSet()
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		src.Write(b)
		f, err := parser.ParseFile(fset, p, b, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl:
				out[x.Name.Name] = true
			case *ast.TypeSpec:
				out[x.Name.Name] = true
			case *ast.ValueSpec:
				for _, n := range x.Names {
					out[n.Name] = true
				}
			case *ast.Field:
				for _, n := range x.Names {
					out[n.Name] = true
				}
			}
			return true
		})
		return nil
	})
	return out, src.String()
}

// files are the repository's paths and their base names.
func files(t *testing.T, root string) map[string]bool {
	out := map[string]bool{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		out[rel], out[d.Name()] = true, true
		return nil
	})
	return out
}

// subcommands are the case labels of the tend dispatchers.
func subcommands(t *testing.T, root string, file string) map[string]bool {
	b, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, m := range caseLine.FindAllStringSubmatch(string(b), -1) {
		for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
			out[q[1]] = true
		}
	}
	return out
}

func TestDocsNameWhatExists(t *testing.T) {
	root := root(t)
	decl, src := names(t, root)
	paths := files(t, root)
	top := subcommands(t, root, "cmd/tend/main.go")
	hosts := subcommands(t, root, "cmd/tend/hosts.go")
	for _, doc := range docs {
		b, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		for _, m := range backtick.FindAllStringSubmatch(text, -1) {
			tok := strings.TrimRight(strings.TrimSpace(m[1]), "/")
			if slices.Contains(notCode, tok) || external.MatchString(tok) || keyName.MatchString(tok) {
				continue
			}
			checkTend(t, doc, tok, top, hosts)
			switch {
			case pathLike.MatchString(tok):
				if !paths[tok] {
					t.Errorf("%s names %q: no such file", doc, tok)
				}
			case envVar.MatchString(tok):
				if !strings.Contains(src, `"`+tok) {
					t.Errorf("%s names %q: no code reads it", doc, tok)
				}
			case ident.MatchString(tok) && (strings.ContainsAny(tok, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") || strings.HasSuffix(tok, "()")):
				g := ident.FindStringSubmatch(tok)
				last := g[1]
				if g[2] != "" {
					last = g[2]
				}
				if !decl[last] {
					t.Errorf("%s names %q: no Go declaration %s", doc, tok, last)
				}
			}
		}
		for _, m := range testRun.FindAllStringSubmatch(text, -1) {
			if !decl[m[1]] && !slices.Contains(notCode, m[1]) {
				t.Errorf("%s runs %s: no such test", doc, m[1])
			}
		}
		fenced := false
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "```") {
				fenced = !fenced
				continue
			}
			if fenced {
				checkTend(t, doc, strings.TrimSpace(line), top, hosts)
				for _, f := range goFile.FindAllString(line, -1) {
					if !paths[f] {
						t.Errorf("%s names %q: no such file", doc, f)
					}
				}
				for _, m := range goRun.FindAllStringSubmatch(line, -1) {
					if !paths[m[1]] {
						t.Errorf("%s runs %q: no such package", doc, m[1])
					}
				}
			}
		}
	}
}

func checkTend(t *testing.T, doc, snippet string, top, hosts map[string]bool) {
	m := tendCmd.FindStringSubmatch(snippet)
	if m == nil {
		return
	}
	if !top[m[1]] {
		t.Errorf("%s: tend %s is not a subcommand", doc, m[1])
	} else if m[1] == "hosts" && m[2] != "" && !hosts[m[2]] {
		t.Errorf("%s: tend hosts %s is not a subcommand", doc, m[2])
	}
}

// TestDesignDocsAreIndexed fails when a design document is missing from docs/design/README.md or a Markdown link in
// the design documents or AGENTS.md points at no file.
func TestDesignDocsAreIndexed(t *testing.T) {
	root := root(t)
	design := filepath.Join(root, "docs", "design")
	index, err := os.ReadFile(filepath.Join(design, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	linked := map[string]bool{}
	for _, m := range mdLink.FindAllStringSubmatch(string(index), -1) {
		linked[filepath.ToSlash(filepath.Clean(m[1]))] = true
	}
	pages := []string{filepath.Join(root, "AGENTS.md")}
	filepath.WalkDir(design, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		pages = append(pages, p)
		if rel, _ := filepath.Rel(design, p); rel != "README.md" && !linked[filepath.ToSlash(rel)] {
			t.Errorf("docs/design/README.md does not list %s", filepath.ToSlash(rel))
		}
		return nil
	})
	for _, page := range pages {
		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, page)
		for _, m := range mdLink.FindAllStringSubmatch(string(b), -1) {
			if strings.Contains(m[1], "://") {
				continue
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(page), filepath.FromSlash(m[1]))); err != nil {
				t.Errorf("%s links %s: no such file", filepath.ToSlash(rel), m[1])
			}
		}
	}
}
