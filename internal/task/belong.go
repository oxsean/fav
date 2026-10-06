package task

import (
	"maps"
	"slices"

	"github.com/oxsean/fav/internal/pathmap"
)

// ProjectOf is the project a session on machine, whose OS is goos, belongs to: the one with a repository whose
// directory on machine holds dir. The deepest such directory wins, the same directory in two projects goes to the
// first project id, nil when none holds it. dir is the session's main checkout when it ran in a linked worktree
// (tend.Rec.Repo, remote.Session.Repo), else its cwd. Every project given counts: the caller passes those its viewer
// sees.
func ProjectOf(projects map[string]*Project, machine, goos, dir string) *Project {
	var best *Project
	var bestDir string
	for _, id := range slices.Sorted(maps.Keys(projects)) {
		p := projects[id]
		for _, r := range p.Repos {
			d := r.Dirs[machine]
			if d == "" || !pathmap.Under(dir, d, goos) {
				continue
			}
			if best == nil || pathmap.Under(d, bestDir, goos) && !pathmap.Under(bestDir, d, goos) {
				best, bestDir = p, d
			}
		}
	}
	return best
}

// MayAttach: user may change p's directories on a machine machineOwner owns: p's owner and admins on any machine, a
// participant on their own. Whether user sees p at all is the caller's question.
func MayAttach(p *Project, user string, admin bool, machineOwner string) bool {
	switch {
	case p == nil || user == "" && !admin:
		return false
	case admin || p.Owner == user:
		return true
	}
	return p.Role(user) == RoleParticipant && machineOwner == user
}
