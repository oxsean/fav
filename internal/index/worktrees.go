package index

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/paths"
)

// wtEntry is what git said about a session directory while it existed; kept after it is removed.
type wtEntry struct {
	Repo   string `json:"repo,omitempty"`   // the main checkout when the directory is inside a linked worktree
	Remote string `json:"remote,omitempty"` // origin of a main checkout, to match removed Codex desktop worktrees
}

// worktrees maps a session directory to wtEntry; shared read-only between index snapshots, learn copies on change.
type worktrees map[string]wtEntry

func worktreesPath(indexPath string) string {
	return filepath.Join(filepath.Dir(indexPath), "worktrees.json")
}

func loadWorktrees(path string) worktrees {
	w := worktrees{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &w)
	}
	return w
}

// learn asks git about every existing session directory not seen before (two execs each, once per directory) and saves
// the map when it grew.
func (w worktrees) learn(files map[string]*File, path string) worktrees {
	var next worktrees
	for _, f := range files {
		if f.Cwd == "" || AgentScratch(f.Cwd) {
			continue
		}
		if _, ok := w[f.Cwd]; ok {
			continue
		}
		if next != nil {
			if _, ok := next[f.Cwd]; ok {
				continue
			}
		}
		if !paths.IsDir(f.Cwd) {
			continue
		}
		if next == nil {
			next = make(worktrees, len(w)+8)
			maps.Copy(next, w)
		}
		next[f.Cwd] = gitWorktree(f.Cwd)
	}
	if next == nil {
		return w
	}
	if b, err := json.Marshal(next); err == nil {
		fileio.WriteFile(path, b, 0o644)
	}
	return next
}

// gitWorktree: a linked worktree has a common git dir outside its own top level.
func gitWorktree(dir string) wtEntry {
	out := capture.GitOut(dir, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel")
	common, top, ok := strings.Cut(out, "\n")
	if !ok || filepath.Base(common) != ".git" {
		return wtEntry{}
	}
	if main := filepath.Dir(paths.Clean(common)); !paths.Same(main, top) {
		return wtEntry{Repo: main}
	}
	return wtEntry{Remote: capture.GitOut(dir, "remote", "get-url", "origin")}
}

var claudeWorktrees = string(filepath.Separator) + filepath.Join(".claude", "worktrees") + string(filepath.Separator)

// repoOf: the main checkout of the worktree a session ran in, "" when it did not. Claude's worktree-state names it; Claude
// Code's own worktrees sit under <repo>/.claude/worktrees; otherwise what git said while the directory existed; a removed
// Codex desktop worktree (<codex home>/worktrees/<id>/<repo name>) is matched by origin and repository name.
func (w worktrees) repoOf(cwd, wtRepo, remote string) string {
	switch {
	case wtRepo != "":
		return wtRepo
	case cwd == "":
		return ""
	}
	if i := strings.Index(cwd, claudeWorktrees); i > 0 {
		return cwd[:i]
	}
	if e, ok := w[cwd]; ok {
		return e.Repo
	}
	rest, ok := paths.Inside(filepath.Join(capture.CodexHome(), "worktrees"), cwd)
	if !ok || rest == "." || remote == "" {
		return ""
	}
	parts := strings.Split(rest, string(filepath.Separator))
	if len(parts) < 2 {
		return ""
	}
	var hits []string
	for dir, e := range w {
		if e.Repo == "" && sameRemote(e.Remote, remote) && filepath.Base(dir) == parts[1] {
			hits = append(hits, dir)
		}
	}
	sort.Strings(hits)
	for _, h := range hits {
		if paths.IsDir(h) {
			return h
		}
	}
	return ""
}

func sameRemote(a, b string) bool {
	norm := func(s string) string {
		return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), "/"), ".git")
	}
	return a != "" && norm(a) == norm(b)
}
