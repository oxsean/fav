package node

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// MRepos finds a repository's checkouts on this machine (remote.ReposParams).
const MRepos = remote.MRepos

// What one node.repos looks at.
var (
	reposBudget  = 3 * time.Second
	reposIndexed = 100 // session directories
	reposWalk    = 200 // directories a walk of the roots reads
)

const reposDepth = 3

// Repos are the checkouts of p.Remote under the roots (node.allow_dirs, else the home), in the order they were found:
// ~/.claude.json's githubRepoPaths, the directories of the indexed Codex sessions that recorded the remote, those of the
// other indexed sessions (the latest first), then a walk of the roots three levels down. Each one is a checkout whose
// origin has the remote's task.RemoteKey, listed once by its top level.
func (n *Node) Repos(ctx context.Context, p remote.ReposParams) (remote.Repos, error) {
	key := task.RemoteKey(p.Remote)
	if key == "" {
		return remote.Repos{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "remote"}
	}
	ctx, cancel := context.WithTimeout(ctx, reposBudget)
	defer cancel()
	roots := n.dirRoots()
	out := remote.Repos{Dirs: []remote.RepoDir{}}
	seen := map[string]bool{}
	try := func(dir, from string) {
		if ctx.Err() != nil || dir == "" || !filepath.IsAbs(dir) {
			return
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			return
		}
		seen[dir] = true
		if !paths.IsDir(dir) || !underAny(dir, roots) {
			return
		}
		if r := task.RemoteKey(repoGit(ctx, dir, "remote", "get-url", "origin")); r != key {
			return
		}
		top := filepath.Clean(filepath.FromSlash(repoGit(ctx, dir, "rev-parse", "--show-toplevel")))
		if top == "." || slices.ContainsFunc(out.Dirs, func(d remote.RepoDir) bool { return paths.Same(d.Path, top) }) || !underAny(top, roots) {
			return
		}
		branch := repoGit(ctx, top, "symbolic-ref", "--short", "-q", "HEAD")
		out.Dirs = append(out.Dirs, remote.RepoDir{Path: top, Branch: branch, From: from})
	}
	for _, d := range githubRepoPaths(key) {
		try(d, "claude")
	}
	same, other := indexedDirs(key, reposIndexed)
	for _, d := range same {
		try(d, "index-remote")
	}
	for _, d := range other {
		try(d, "index")
	}
	walkCheckouts(ctx, roots, func(d string) { try(d, "scan") })
	return out, nil
}

func repoGit(ctx context.Context, dir string, args ...string) string {
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	b, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// githubRepoPaths are the checkouts Claude remembers of a GitHub repository (key: task.RemoteKey). ⚠️ The file also
// holds the account: only that one key is decoded, every other value skipped token by token.
func githubRepoPaths(key string) []string {
	repo, ok := strings.CutPrefix(key, "github.com/")
	if !ok {
		return nil
	}
	f, err := os.Open(capture.ClaudeJSON())
	if err != nil {
		return nil
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil
		}
		if k != "githubRepoPaths" {
			if skipValue(dec) != nil {
				return nil
			}
			continue
		}
		var m map[string][]string
		if dec.Decode(&m) != nil {
			return nil
		}
		for name, dirs := range m {
			if strings.EqualFold(name, repo) {
				return dirs
			}
		}
		return nil
	}
	return nil
}

func skipValue(dec *json.Decoder) error {
	depth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		switch t {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

// indexedDirs are the main checkouts, else the directories, of the sessions the index has, the latest first: same holds
// every one a Codex session recorded key's origin in, other at most n of the rest.
func indexedDirs(key string, n int) (same, other []string) {
	idx, err := index.Open()
	if err != nil {
		return nil, nil
	}
	ss := idx.Sessions()
	slices.SortFunc(ss, func(a, b *index.Session) int { return b.LastAt.Compare(a.LastAt) })
	for _, s := range ss {
		d := cmp.Or(s.Repo, s.Cwd)
		switch {
		case d == "" || slices.Contains(same, d):
		case task.RemoteKey(s.GitRemote()) == key:
			same = append(same, d)
			other = slices.DeleteFunc(other, func(o string) bool { return o == d })
		case !slices.Contains(other, d) && len(same)+len(other) < n:
			other = append(other, d)
		}
	}
	return same, other
}

// walkCheckouts calls found for each checkout within reposDepth levels under roots, hidden directories left out, until
// it has read reposWalk directories or ctx ends; it does not look inside a checkout.
func walkCheckouts(ctx context.Context, roots []string, found func(string)) {
	queue, read := slices.Clone(roots), 0
	depth := map[string]int{}
	for len(queue) > 0 && read < reposWalk && ctx.Err() == nil {
		dir := queue[0]
		queue = queue[1:]
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			found(dir)
			continue
		}
		if depth[dir] == reposDepth {
			continue
		}
		entries, err := os.ReadDir(dir)
		read++
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				sub := filepath.Join(dir, e.Name())
				depth[sub] = depth[dir] + 1
				queue = append(queue, sub)
			}
		}
	}
}
