package coord

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

type ProjectCreate struct {
	ID    string `json:"id,omitempty"` // default: a new one
	Name  string `json:"name"`
	Owner string `json:"owner,omitempty"` // default: the caller
}

// RunMessages is run.messages: a page of a run's conversation, as remote.MessagesParams pages a session's.
type RunMessages struct {
	Run    string `json:"run"`
	Before int64  `json:"before"`
	N      int    `json:"n"`
	File   string `json:"file,omitempty"`
}

var projectID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

const maxName = 128

func projectView(st *task.State, id string) any {
	if p := st.Projects[id]; p != nil {
		cp := *p
		cp.Members = maps.Clone(p.Members)
		return &cp
	}
	return nil
}

func shareView(st *task.State, machine string) any {
	if s := st.Shares[machine]; s != nil {
		cp := *s
		return &cp
	}
	return &task.Share{Machine: machine}
}

func (c *Coord) checkUser(id string) error {
	if u, ok := c.user(id); !ok || u.Disabled {
		return notFound("user " + id)
	}
	return nil
}

func (c *Coord) projectCreate(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p ProjectCreate
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > maxName {
		return "", nil, bad("name")
	}
	if p.ID == "" {
		p.ID = newID("p_")
	}
	if !projectID.MatchString(p.ID) {
		return "", nil, bad("id")
	}
	if c.st.Projects[p.ID] != nil {
		return "", nil, conflict("project " + p.ID)
	}
	p.Owner = firstOf(p.Owner, who.User)
	if err := c.checkUser(p.Owner); err != nil {
		return "", nil, err
	}
	pr := task.Project{ID: p.ID, Name: p.Name, Owner: p.Owner}
	return pr.ID, []journal.Event{journal.NewEvent(task.EProjectCreated, pr)}, nil
}

// managedProject is project id when who manages it (its owner, or an admin): not found when they cannot see it.
func (c *Coord) managedProject(who Principal, id string) (*task.Project, error) {
	pr := c.st.Projects[id]
	switch {
	case pr == nil || !who.Admin && pr.Role(who.User) == "":
		return nil, notFound("project " + id)
	case !who.Admin && pr.Owner != who.User:
		return nil, forbidden("project " + id)
	}
	return pr, nil
}

func (c *Coord) projectEdit(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.ProjectEdit
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	pr, err := c.managedProject(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	changed := false
	if p.Name != nil {
		if *p.Name = strings.TrimSpace(*p.Name); *p.Name == "" || len(*p.Name) > maxName {
			return "", nil, bad("name")
		}
		changed = changed || *p.Name != pr.Name
	}
	if p.Owner != nil {
		if err := c.checkUser(*p.Owner); err != nil {
			return "", nil, err
		}
		changed = changed || *p.Owner != pr.Owner
	}
	if err := c.checkSettings(p); err != nil {
		return "", nil, err
	}
	changed = changed || p.Repos != nil && !reflect.DeepEqual(*p.Repos, pr.Repos) || p.Links != nil && !reflect.DeepEqual(*p.Links, pr.Links) ||
		p.Context != nil && *p.Context != pr.Context || p.Defaults != nil && !reflect.DeepEqual(*p.Defaults, pr.Defaults) ||
		p.Hooks != nil && !reflect.DeepEqual(*p.Hooks, pr.Hooks) || p.Fetch != nil && !slices.Equal(*p.Fetch, pr.Fetch)
	if !changed {
		return pr.ID, nil, nil
	}
	return pr.ID, []journal.Event{journal.NewEvent(task.EProjectEdited, p)}, nil
}

func (c *Coord) projectMember(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.MemberSet
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	pr, err := c.managedProject(who, p.Project)
	if err != nil {
		return "", nil, err
	}
	if !slices.Contains([]string{"", task.RoleParticipant, task.RoleReader}, p.Role) {
		return "", nil, bad("role " + p.Role)
	}
	if p.Role != "" {
		if err := c.checkUser(p.User); err != nil {
			return "", nil, err
		}
	}
	if pr.Members[p.User] == p.Role {
		return pr.ID, nil, nil
	}
	return pr.ID, []journal.Event{journal.NewEvent(task.EMemberSet, p)}, nil
}

// machineShare replaces who else may dispatch to a machine: its owner or an admin decides.
func (c *Coord) machineShare(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.Share
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	if c.ms[p.Machine] == nil || !c.canSee(who, p.Machine) {
		return "", nil, notFound("machine " + p.Machine)
	}
	if !who.Admin && c.ownerOf(p.Machine) != who.User {
		return "", nil, forbidden("machine " + p.Machine)
	}
	slices.Sort(p.Users)
	p.Users = slices.Compact(p.Users)
	slices.Sort(p.Projects)
	p.Projects = slices.Compact(p.Projects)
	for _, u := range p.Users {
		if err := c.checkUser(u); err != nil {
			return "", nil, err
		}
	}
	for _, id := range p.Projects {
		if c.st.Projects[id] == nil {
			return "", nil, notFound("project " + id)
		}
	}
	now := c.st.Shares[p.Machine]
	if now == nil {
		now = &task.Share{Machine: p.Machine}
	}
	if slices.Equal(now.Users, p.Users) && slices.Equal(now.Projects, p.Projects) && now.Approve == p.Approve {
		return p.Machine, nil, nil
	}
	return p.Machine, []journal.Event{journal.NewEvent(task.EMachineShared, p)}, nil
}

// runMessages is a page of run's conversation, for whoever may read the run: its session, not any of the machine's.
func (c *Coord) runMessages(ctx context.Context, who Principal, r *wire.Request) (any, error) {
	var p RunMessages
	if err := r.Decode(&p); err != nil {
		return nil, err
	}
	run, err := c.readableRun(who, p.Run)
	if err != nil {
		return nil, err
	}
	if run.Session == "" {
		return nil, notFound("session of " + run.ID)
	}
	var out json.RawMessage
	err = c.call(ctx, run.Machine, remote.MMessages, remote.MessagesParams{Ref: remote.Ref{Provider: run.Provider, SessionID: run.Session},
		Before: p.Before, N: p.N, File: p.File}, &out)
	return out, err
}

// Hooks a project may set.
var hookNames = []string{"setup", "before_run", "check", "cleanup"}

// checkSettings checks what a project edit sets besides its name and owner. The caller holds mu.
func (c *Coord) checkSettings(p task.ProjectEdit) error {
	argv := func(what string, a []string) error {
		if len(a) > 64 || slices.ContainsFunc(a, func(s string) bool { return len(s) > maxLine }) {
			return bad(what)
		}
		return nil
	}
	if p.Repos != nil {
		if len(*p.Repos) > 20 {
			return bad("repos")
		}
		for i, r := range *p.Repos {
			switch {
			case r.Name == "" || len(r.Name) > 64 || slices.ContainsFunc((*p.Repos)[:i], func(x task.Repo) bool { return x.Name == r.Name }):
				return bad("repo name " + r.Name)
			case len(r.Remote) > maxLine || len(r.Base) > 255:
				return bad("repo " + r.Name)
			}
			for m, d := range r.Dirs {
				if m == "" || len(m) > 64 || d == "" || len(d) > 4096 {
					return bad("repo " + r.Name + " dir on " + m)
				}
			}
		}
	}
	if p.Links != nil && (len(*p.Links) > 50 || slices.ContainsFunc(*p.Links, func(l task.Link) bool {
		return l.Kind == "" || len(l.Kind) > 32 || l.URL == "" || len(l.URL) > maxLine
	})) {
		return bad("links")
	}
	if p.Context != nil && len(*p.Context) > maxBrief {
		return bad("context")
	}
	if d := p.Defaults; d != nil {
		if err := c.checkMachine(d.Machine); err != nil {
			return err
		}
		for _, a := range append([]string{d.Agent}, slices.Collect(maps.Values(d.Roles))...) {
			if err := c.checkAgent(a); err != nil {
				return err
			}
		}
	}
	if p.Hooks != nil {
		for k, a := range *p.Hooks {
			if !slices.Contains(hookNames, k) || len(a) == 0 {
				return bad("hook " + k)
			}
			if err := argv("hook "+k, a); err != nil {
				return err
			}
		}
	}
	if p.Fetch != nil {
		return argv("fetch", *p.Fetch)
	}
	return nil
}

// Offboard is user.offboard: hand what user holds to others before they leave. To takes what no project owner
// does; "" is the caller.
type Offboard struct {
	User string `json:"user"`
	To   string `json:"to,omitempty"`
}

// userOffboard hands user's projects, tasks and definitions on, takes them out of every project and closes their
// machines to everyone else; runs queued there by others are then canceled before they start. The caller holds mu.
func (c *Coord) userOffboard(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p Offboard
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	if _, ok := c.user(p.User); !ok || p.User == Owner.User {
		return "", nil, notFound("user " + p.User)
	}
	to := cmp.Or(p.To, who.User)
	if to == p.User {
		return "", nil, bad("to")
	}
	if err := c.checkUser(to); err != nil {
		return "", nil, err
	}
	var events []journal.Event
	owners := map[string]string{} // project → its owner once this is done
	for _, id := range slices.Sorted(maps.Keys(c.st.Projects)) {
		pr := c.st.Projects[id]
		owners[id] = pr.Owner
		if pr.Owner == p.User {
			owners[id] = to
			events = append(events, journal.NewEvent(task.EProjectEdited, task.ProjectEdit{ID: id, Owner: &to}))
		}
		if _, member := pr.Members[p.User]; member {
			events = append(events, journal.NewEvent(task.EMemberSet, task.MemberSet{Project: id, User: p.User}))
		}
	}
	for _, id := range slices.Sorted(maps.Keys(c.st.Tasks)) {
		t := c.st.Tasks[id]
		if task.Finished(t.Status) || t.Owner != p.User && t.Approver != p.User {
			continue
		}
		heir := cmp.Or(owners[t.Project], to)
		e := task.TaskEdit{ID: id}
		if t.Owner == p.User {
			e.Owner = &heir
		}
		if t.Approver == p.User {
			e.Approver = &heir
		}
		events = append(events, journal.NewEvent(task.ETaskEdited, e))
	}
	for _, name := range slices.Sorted(maps.Keys(c.st.AgentDefs)) {
		if d := c.st.AgentDefs[name]; d.Owner == p.User {
			cp := *d
			cp.Owner = to
			events = append(events, journal.NewEvent(task.EAgentDefSaved, cp))
		}
	}
	for _, m := range slices.Sorted(maps.Keys(c.st.Shares)) {
		if c.ownerOf(m) == p.User {
			events = append(events, journal.NewEvent(task.EMachineShared, task.Share{Machine: m}))
		}
	}
	return p.User, events, nil
}
