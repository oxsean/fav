package coord

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// ProjectAttach is project.attach: Dir on Machine joins the project's repositories.
type ProjectAttach struct {
	Project string `json:"project"`
	Machine string `json:"machine"`
	Dir     string `json:"dir"`              // absolute, in the machine's form
	Remote  string `json:"remote,omitempty"` // its git remote: the repository with the same one takes it
}

// ProjectDetach is project.detach: Dir on Machine leaves the project's repositories.
type ProjectDetach struct {
	Project string `json:"project"`
	Machine string `json:"machine"`
	Dir     string `json:"dir"`
}

type SessionsParams struct {
	Machine string `json:"machine"`
}

// SessionsList answers sessions.list: the machine's sessions and who of them runs as its node answers list and live,
// and the project each belongs to among those the caller sees ("provider:session_id" → project id; none: in no project).
type SessionsList struct {
	Sessions []json.RawMessage          `json:"sessions"`
	Live     map[string]json.RawMessage `json:"live"`
	Projects map[string]string          `json:"projects"`
}

// attachable is project id when who may change its directories on machine: its owner and admins anywhere, a
// participant on a machine they own; not found when they cannot see it. The caller holds mu.
func (c *Coord) attachable(who Principal, id, machine string) (*task.Project, error) {
	pr := c.st.Projects[id]
	switch {
	case pr == nil || !c.seesProject(who, pr):
		return nil, notFound("project " + id)
	case task.MayAttach(pr, who.User, who.Admin, c.ownerOf(machine)):
		return pr, nil
	}
	return nil, forbidden("project " + id + " on " + machine)
}

// osOf is machine's GOOS as its hello said; "" not known. The caller holds mu.
func (c *Coord) osOf(machine string) string {
	if m := c.ms[machine]; m != nil {
		return m.hello.OS
	}
	return ""
}

// absoluteOn: dir is absolute in the form of a machine whose OS is goos, or of some machine when goos is not known.
// A path under ~ is not: it names no session's directory.
func absoluteOn(dir, goos string) bool {
	if goos == "" {
		goos = "linux"
		if pathmap.Drive(dir) || strings.HasPrefix(dir, `\\`) {
			goos = "windows"
		}
	}
	return pathmap.Under(dir, dir, goos)
}

// sameDir: a and b are one directory on a machine whose OS is goos; written alike when it is not known.
func sameDir(a, b, goos string) bool {
	return a == b || goos != "" && pathmap.Under(a, b, goos) && pathmap.Under(b, a, goos)
}

func cloneRepos(rs []task.Repo) []task.Repo {
	out := slices.Clone(rs)
	for i := range out {
		out[i].Dirs = maps.Clone(out[i].Dirs)
	}
	return out
}

// repoName is a name for a repository at dir that none of rs has: the directory's, then -2, -3, …
func repoName(rs []task.Repo, dir string) string {
	base := cmp.Or(pathmap.Base(strings.TrimRight(dir, `/\`)), "repo")
	for len(base) > 56 {
		_, n := utf8.DecodeLastRuneInString(base)
		base = base[:len(base)-n]
	}
	name := base
	for i := 2; slices.ContainsFunc(rs, func(r task.Repo) bool { return r.Name == name }); i++ {
		name = base + "-" + strconv.Itoa(i)
	}
	return name
}

// projectAttach adds a directory on a machine to a project: to the repository of the same remote that has none there
// yet, else as a repository of its own. A directory the project has there already changes nothing.
func (c *Coord) projectAttach(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p ProjectAttach
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	p.Dir, p.Remote = strings.TrimSpace(p.Dir), strings.TrimSpace(p.Remote)
	pr, err := c.attachable(who, p.Project, p.Machine)
	if err != nil {
		return "", nil, err
	}
	if c.ms[p.Machine] == nil || !c.canSee(who, p.Machine) {
		return "", nil, notFound("machine " + p.Machine)
	}
	goos := c.osOf(p.Machine)
	if !absoluteOn(p.Dir, goos) {
		return "", nil, bad("dir " + p.Dir)
	}
	for _, rp := range pr.Repos {
		if d := rp.Dirs[p.Machine]; d != "" && sameDir(d, p.Dir, goos) {
			return pr.ID, nil, nil
		}
	}
	repos := cloneRepos(pr.Repos)
	at := -1
	if p.Remote != "" {
		at = slices.IndexFunc(repos, func(rp task.Repo) bool { return rp.Remote == p.Remote && rp.Dirs[p.Machine] == "" })
	}
	if at < 0 {
		repos = append(repos, task.Repo{Name: repoName(repos, p.Dir), Remote: p.Remote})
		at = len(repos) - 1
	}
	if repos[at].Dirs == nil {
		repos[at].Dirs = map[string]string{}
	}
	repos[at].Dirs[p.Machine] = p.Dir
	edit := task.ProjectEdit{ID: pr.ID, Repos: &repos}
	if err := c.checkSettings(edit); err != nil {
		return "", nil, err
	}
	return pr.ID, []journal.Event{journal.NewEvent(task.EProjectEdited, edit)}, nil
}

// projectDetach takes a directory on a machine out of a project, and a repository it leaves without one.
func (c *Coord) projectDetach(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p ProjectDetach
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	p.Dir = strings.TrimSpace(p.Dir)
	pr, err := c.attachable(who, p.Project, p.Machine)
	if err != nil {
		return "", nil, err
	}
	goos := c.osOf(p.Machine)
	repos := cloneRepos(pr.Repos)
	emptied := map[int]bool{}
	for i := range repos {
		if d := repos[i].Dirs[p.Machine]; d != "" && sameDir(d, p.Dir, goos) {
			delete(repos[i].Dirs, p.Machine)
			emptied[i] = len(repos[i].Dirs) == 0
		}
	}
	if len(emptied) == 0 {
		return "", nil, notFound("dir " + p.Dir + " on " + p.Machine)
	}
	kept := []task.Repo{}
	for i, rp := range repos {
		if !emptied[i] {
			kept = append(kept, rp)
		}
	}
	return pr.ID, []journal.Event{journal.NewEvent(task.EProjectEdited, task.ProjectEdit{ID: pr.ID, Repos: &kept})}, nil
}

// sessionsList asks machine's node for its sessions and who of them runs, as node.call would for whoever may read
// them, and tells which project each belongs to (task.ProjectOf: its main checkout, else its cwd). A failed live
// counts as none running.
func (c *Coord) sessionsList(ctx context.Context, who Principal, r *wire.Request) (any, error) {
	var p SessionsParams
	if err := r.Decode(&p); err != nil {
		return nil, err
	}
	c.mu.Lock()
	reads := c.readsSessions(who, p.Machine)
	c.mu.Unlock()
	if !reads {
		return nil, forbidden(MSessionsList)
	}
	var list struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	var live struct {
		Live map[string]json.RawMessage `json:"live"`
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if c.call(ctx, p.Machine, remote.MLive, nil, &live) != nil {
			live.Live = nil
		}
	}()
	err := c.call(ctx, p.Machine, remote.MList, nil, &list)
	<-done
	if err != nil {
		return nil, err
	}
	out := SessionsList{Sessions: list.Sessions, Live: live.Live, Projects: map[string]string{}}
	if out.Sessions == nil {
		out.Sessions = []json.RawMessage{}
	}
	if out.Live == nil {
		out.Live = map[string]json.RawMessage{}
	}

	c.mu.Lock()
	projects := map[string]*task.Project{}
	for id, pr := range c.st.Projects {
		if c.seesProject(who, pr) {
			projects[id] = &task.Project{ID: pr.ID, Repos: pr.Repos} // a project edit replaces Repos, never changes it in place
		}
	}
	goos := c.osOf(p.Machine)
	c.mu.Unlock()
	belongs := func(key, dir string) {
		if pr := task.ProjectOf(projects, p.Machine, goos, dir); pr != nil {
			out.Projects[key] = pr.ID
		}
	}
	listed := map[string]bool{}
	for _, raw := range out.Sessions {
		var s remote.Session
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		key := s.Provider + ":" + s.SessionID
		listed[key] = true
		belongs(key, cmp.Or(s.Repo, s.Cwd))
	}
	for id, raw := range out.Live {
		var l capture.Live
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		if key := l.Agent + ":" + id; !listed[key] && l.Cwd != "" {
			belongs(key, l.Cwd)
		}
	}
	return out, nil
}
