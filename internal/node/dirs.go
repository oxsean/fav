package node

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/wire"
)

// MDirs lists the directories under one this node may run in (DirsParams), for picking a project's checkout.
const MDirs = "node.dirs"

type DirsParams struct {
	Path string `json:"path,omitempty"` // "" lists the roots
}

type Dir struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Git  bool   `json:"git,omitempty"` // a repository's checkout
}

type Dirs struct {
	Path   string `json:"path,omitempty"`
	Parent string `json:"parent,omitempty"` // one level up, while still inside the roots
	Dirs   []Dir  `json:"dirs"`
}

// maxDirs is how many directories one listing holds.
const maxDirs = 500

// dirRoots are where runs may go here: node.allow_dirs, else the home directory.
func (n *Node) dirRoots() []string {
	var out []string
	for _, r := range n.Limits.AllowDirs {
		out = append(out, filepath.Clean(paths.Expand(r)))
	}
	if len(out) == 0 {
		if home, err := os.UserHomeDir(); err == nil {
			out = []string{home}
		}
	}
	return out
}

// Dirs lists p's subdirectories, hidden ones left out; p must be one of the roots or under one.
func (n *Node) Dirs(p DirsParams) (Dirs, error) {
	roots := n.dirRoots()
	if p.Path == "" {
		out := Dirs{Dirs: []Dir{}}
		for _, r := range roots {
			out.Dirs = append(out.Dirs, listed(r, r))
		}
		return out, nil
	}
	dir := filepath.Clean(paths.Expand(p.Path))
	if !filepath.IsAbs(dir) || !underAny(dir, roots) {
		return Dirs{}, &wire.Error{Code: wire.CodeUnauthorized, Detail: "dir " + p.Path}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Dirs{}, &wire.Error{Code: wire.CodeNotFound, Detail: err.Error()}
	}
	out := Dirs{Path: dir, Dirs: []Dir{}}
	if up := filepath.Dir(dir); up != dir && underAny(up, roots) {
		out.Parent = up
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || len(out.Dirs) == maxDirs {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
			continue
		}
		out.Dirs = append(out.Dirs, listed(e.Name(), full))
	}
	slices.SortFunc(out.Dirs, func(a, b Dir) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}

func listed(name, full string) Dir {
	_, err := os.Stat(filepath.Join(full, ".git"))
	return Dir{Name: name, Path: full, Git: err == nil}
}
