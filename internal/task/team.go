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
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Owner     string            `json:"owner,omitempty"`   // manages the members
	Members   map[string]string `json:"members,omitempty"` // user → role
	Rev       int               `json:"rev,omitzero"`
	CreatedAt time.Time         `json:"created_at,omitzero"`
	UpdatedAt time.Time         `json:"updated_at,omitzero"`
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
	ID    string  `json:"id"`
	Name  *string `json:"name,omitempty"`
	Owner *string `json:"owner,omitempty"`
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
