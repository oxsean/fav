package envcheck

import (
	"errors"
	"slices"

	"github.com/oxsean/fav/internal/index"
)

// SeenOf is what a session of provider's CLI saw, from its index summary (nil when it recorded nothing), its files
// named as Collect names them for dir, the directory it ran in.
func SeenOf(provider string, e *index.Env, dir string) *Seen {
	s := &Seen{CLI: provider}
	if e == nil {
		return s
	}
	s.At, s.Version, s.Model, s.Provider = e.At, e.Version, e.Model, e.Provider
	s.Approval, s.Sandbox, s.Platform, s.Shell, s.Worktree = e.Approval, e.Sandbox, e.Platform, e.Shell, e.Worktree
	s.Skills, s.Tools, s.MCP, s.Agents = slices.Clone(e.Skills), slices.Clone(e.Tools), slices.Clone(e.MCP), slices.Clone(e.Agents)
	s.Used, s.UsedMCP, s.Known = slices.Clone(e.Used), slices.Clone(e.UsedMCP), slices.Clone(e.Known)
	for _, f := range e.Files {
		kind, name := nameOf(f.Path, dir)
		s.Files = append(s.Files, File{Kind: kind, Name: name, Norm: f.Norm})
	}
	return s
}

func (s *Seen) knows(set string) bool { return s != nil && slices.Contains(s.Known, set) }

// ErrNotListed: env.file names a file that is not one of the directory's instruction files.
var ErrNotListed = errors.New("not an instruction file here")

// FileText is the text of one of dir's instruction files as Collect lists it by kind and name; any other file is
// ErrNotListed, whatever its path.
func FileText(kind, name, dir string) (string, error) {
	for _, f := range instructionFiles(dir, func(string) {}) {
		if f.Kind == kind && f.Name == name {
			b, err := readCapped(f.path)
			return string(b), err
		}
	}
	return "", ErrNotListed
}
