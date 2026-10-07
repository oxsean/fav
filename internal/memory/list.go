package memory

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/oxsean/fav/internal/paths"
)

// List is the Claude memory of each of dirs, one set per memory directory; with global, also Codex's global blocks
// that apply to each of dirs and, apart, those naming no directory.
func List(dirs []string, global bool) []Set {
	var out []Set
	seen := map[string]bool{}
	for _, d := range dirs {
		mem := ClaudeDir(d)
		if seen[mem] {
			continue
		}
		seen[mem] = true
		if s, ok := Load(KindClaude, mem); ok {
			out = append(out, s)
		}
	}
	if global {
		out = append(out, codexGlobal(dirs)...)
	}
	return out
}

type block struct {
	item Item
	cwd  string // "" when applies_to names no absolute directory
}

// codexGlobal splits Codex's MEMORY.md into its "# Task Group:" blocks, each applying to the cwd= of its applies_to.
func codexGlobal(dirs []string) []Set {
	p := codexGlobalIndex()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil
	}
	var blocks []block
	for i, l := range strings.Split(string(normalize(b)), "\n") {
		switch {
		case strings.HasPrefix(l, "# Task Group:"):
			blocks = append(blocks, block{item: Item{File: p, Title: strings.TrimSpace(strings.TrimPrefix(l, "# Task Group:")), At: info.ModTime(), Line: i + 1}})
		case len(blocks) == 0:
		case strings.HasPrefix(l, "scope:") && blocks[len(blocks)-1].item.Description == "":
			blocks[len(blocks)-1].item.Description = strings.TrimSpace(strings.TrimPrefix(l, "scope:"))
		case strings.HasPrefix(l, "applies_to:") && blocks[len(blocks)-1].cwd == "":
			blocks[len(blocks)-1].cwd = appliesTo(l)
		}
	}
	base := Set{Kind: KindCodexGlobal, Index: p, Lines: countLines(b), Bytes: int64(len(b))}
	var out []Set
	for _, d := range dirs {
		s := base
		s.Dir, s.Items = d, []Item{}
		for _, bl := range blocks {
			if bl.cwd != "" && paths.Under(bl.cwd, d) {
				s.Items = append(s.Items, bl.item)
			}
		}
		out = append(out, s)
	}
	loose := base
	loose.Items = []Item{}
	for _, bl := range blocks {
		if bl.cwd == "" || len(dirs) == 0 {
			loose.Items = append(loose.Items, bl.item)
		}
	}
	return append(out, loose)
}

// appliesTo is the absolute directory of "applies_to: cwd=<dir>; reuse_rule=…", "" when it names none.
func appliesTo(l string) string {
	_, rest, ok := strings.Cut(l, "cwd=")
	if !ok {
		return ""
	}
	cwd, _, _ := strings.Cut(rest, ";")
	cwd = strings.Trim(strings.TrimSpace(cwd), "`")
	if !filepath.IsAbs(cwd) {
		return ""
	}
	return filepath.Clean(cwd)
}
