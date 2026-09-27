package coord

import (
	"context"
	"encoding/json"
	"maps"
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
