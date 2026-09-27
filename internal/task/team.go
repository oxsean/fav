package task

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/oxsean/fav/internal/journal"
)

// Member roles in a project.
const (
	RoleParticipant = "participant" // creates, starts, answers, stops
	RoleReader      = "reader"      // sees, changes nothing
)

// Project is a team's unit of access: whoever is not a member sees none of its tasks and runs.
type Project struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Owner     string              `json:"owner,omitempty"`   // manages the members
	Members   map[string]string   `json:"members,omitempty"` // user → role
	Repos     []Repo              `json:"repos,omitempty"`
	Links     []Link              `json:"links,omitempty"`
	Context   string              `json:"context,omitempty"` // what every run of the project is told about it
	Defaults  Defaults            `json:"defaults,omitzero"`
	Hooks     map[string][]string `json:"hooks,omitempty"`     // setup | before_run | check | cleanup → argv
	Fetch     []string            `json:"fetch,omitempty"`     // argv that prints a requirement; {ref} is its link
	Workflows map[string]string   `json:"workflows,omitempty"` // its own workflows: name → Markdown definition
	Rev       int                 `json:"rev,omitzero"`
	CreatedAt time.Time           `json:"created_at,omitzero"`
	UpdatedAt time.Time           `json:"updated_at,omitzero"`
}

// Repo is a repository of a project and where it is on each machine.
type Repo struct {
	Name   string            `json:"name"`
	Remote string            `json:"remote,omitempty"` // shared by the machines that hand work on
	Base   string            `json:"base,omitempty"`   // the branch new work starts from
	Dirs   map[string]string `json:"dirs,omitempty"`   // machine → its checkout there
}

type Link struct {
	Kind string `json:"kind"` // tracker | doc | …
	URL  string `json:"url"`
}

// Defaults are what a task of the project runs with when it says nothing itself.
type Defaults struct {
	Workflow string            `json:"workflow,omitempty"`
	Roles    map[string]string `json:"roles,omitempty"` // role → agent
	Machine  string            `json:"machine,omitempty"`
	Agent    string            `json:"agent,omitempty"`
}

// Agent is the agent a task of p runs with by default: Defaults.Agent, else the one for implementing.
func (p *Project) Agent() string {
	if p == nil {
		return ""
	}
	if p.Defaults.Agent != "" {
		return p.Defaults.Agent
	}
	return p.Defaults.Roles["implement"]
}

// DirOn is where p's first repository with a checkout on machine is there.
func (p *Project) DirOn(machine string) (string, bool) {
	if p == nil {
		return "", false
	}
	for _, r := range p.Repos {
		if d := r.Dirs[machine]; d != "" {
			return d, true
		}
	}
	return "", false
}

// Role is user's role in p: the owner participates; "" is none.
func (p *Project) Role(user string) string {
	if p == nil || user == "" {
		return ""
	}
	if p.Owner == user {
		return RoleParticipant
	}
	return p.Members[user]
}

// Share is who else may dispatch to a machine besides its owner.
type Share struct {
	Machine  string   `json:"machine"`
	Users    []string `json:"users,omitempty"`
	Projects []string `json:"projects,omitempty"`
	Approve  bool     `json:"approve,omitempty"` // they may also grant its runs' permission requests
}

// Opens: s lets user, or a run of project, use its machine.
func (s *Share) Opens(user, project string) bool {
	return s != nil && (slices.Contains(s.Users, user) || project != "" && slices.Contains(s.Projects, project))
}

// Team events.
const (
	EProjectCreated = "project_created"
	EProjectEdited  = "project_edited"
	EMemberSet      = "member_set"
	EMachineShared  = "machine_shared"
)

type ProjectEdit struct {
	ID        string               `json:"id"`
	Name      *string              `json:"name,omitempty"`
	Owner     *string              `json:"owner,omitempty"`
	Repos     *[]Repo              `json:"repos,omitempty"`
	Links     *[]Link              `json:"links,omitempty"`
	Context   *string              `json:"context,omitempty"`
	Defaults  *Defaults            `json:"defaults,omitempty"`
	Hooks     *map[string][]string `json:"hooks,omitempty"`
	Fetch     *[]string            `json:"fetch,omitempty"`
	Workflows *map[string]string   `json:"workflows,omitempty"`
}

// MemberSet gives user a role in project; "" takes it away.
type MemberSet struct {
	Project string `json:"project"`
	User    string `json:"user"`
	Role    string `json:"role,omitempty"`
}

// applyTeam folds a team event in; false when e is not one.
func (s *State) applyTeam(e journal.Event, at time.Time) (bool, error) {
	switch e.Type {
	case EProjectCreated:
		var p Project
		if err := json.Unmarshal(e.Data, &p); err != nil {
			return true, err
		}
		if s.Projects[p.ID] != nil {
			return true, fmt.Errorf("project %s exists", p.ID)
		}
		p.Rev, p.CreatedAt, p.UpdatedAt = 1, at, at
		s.Projects[p.ID] = &p
	case EProjectEdited:
		var d ProjectEdit
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		p := s.Projects[d.ID]
		if p == nil {
			return true, fmt.Errorf("no project %s", d.ID)
		}
		if d.Name != nil {
			p.Name = *d.Name
		}
		if d.Owner != nil {
			p.Owner = *d.Owner
		}
		if d.Repos != nil {
			p.Repos = *d.Repos
		}
		if d.Links != nil {
			p.Links = *d.Links
		}
		if d.Context != nil {
			p.Context = *d.Context
		}
		if d.Defaults != nil {
			p.Defaults = *d.Defaults
		}
		if d.Hooks != nil {
			p.Hooks = *d.Hooks
		}
		if d.Fetch != nil {
			p.Fetch = *d.Fetch
		}
		if d.Workflows != nil {
			p.Workflows = *d.Workflows
		}
		p.Rev++
		p.UpdatedAt = at
	case EMemberSet:
		var d MemberSet
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		p := s.Projects[d.Project]
		if p == nil {
			return true, fmt.Errorf("no project %s", d.Project)
		}
		if d.Role == "" {
			delete(p.Members, d.User)
		} else {
			if p.Members == nil {
				p.Members = map[string]string{}
			}
			p.Members[d.User] = d.Role
		}
		p.Rev++
		p.UpdatedAt = at
	case EMachineShared:
		var d Share
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		if len(d.Users) == 0 && len(d.Projects) == 0 {
			delete(s.Shares, d.Machine)
		} else {
			s.Shares[d.Machine] = &d
		}
	default:
		return false, nil
	}
	return true, nil
}
