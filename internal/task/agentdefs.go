package task

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/defs"
	"github.com/oxsean/fav/internal/journal"
)

// Agent definition events.
const (
	EAgentDefSaved   = "agentdef_saved"   // AgentDef
	EAgentDefRemoved = "agentdef_removed" // AgentDefRef
	EAgentDefShared  = "agentdef_shared"  // AgentDefShare
	// EAgentDefTransferred gives a definition to another owner, its sharing kept.
	EAgentDefTransferred = "agentdef_transferred" // AgentDefTransfer
)

// ProjectOwner prefixes an owner that is a project: its owner manages the definition, its participants use it.
const ProjectOwner = "project:"

// AgentDef is a definition as the coordinator keeps it: whose it is and whom it is shared with.
type AgentDef struct {
	defs.AgentDef
	Owner     string    `json:"owner"`
	Share     DefShare  `json:"share,omitzero"`
	SavedBy   string    `json:"saved_by,omitempty"` // who saved this text last
	SavedAt   time.Time `json:"saved_at,omitzero"`
	Rev       int       `json:"rev,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// DefShare is who else may use a definition, and whether they may read it too.
type DefShare struct {
	Users    []string `json:"users,omitempty"`
	Projects []string `json:"projects,omitempty"`
	All      bool     `json:"all,omitempty"`
	View     bool     `json:"view,omitempty"` // those it is shared with may read its instructions
	Left     []string `json:"left,omitempty"` // who stopped using it: a project or everyone no longer gives it to them
}

type AgentDefRef struct {
	Name string `json:"name"`
}

type AgentDefTransfer struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

type AgentDefShare struct {
	Name  string   `json:"name"`
	Share DefShare `json:"share"`
}

// Project is the project that owns d, "" when a user does.
func (d *AgentDef) Project() string {
	p, _ := strings.CutPrefix(d.Owner, ProjectOwner)
	if p == d.Owner {
		return ""
	}
	return p
}

// Usable: user may run d for a task of project (role is their role in it).
func (d *AgentDef) Usable(user, project, role string) bool {
	switch {
	case d == nil:
		return false
	case d.Owner == user, slices.Contains(d.Share.Users, user):
		return true
	case slices.Contains(d.Share.Left, user):
		return false
	case d.Share.All:
		return true
	case project == "" || role != RoleParticipant:
		return false
	}
	return d.Project() == project || slices.Contains(d.Share.Projects, project)
}

func (s *State) applyDefs(e journal.Event, at time.Time) (bool, error) {
	switch e.Type {
	case EAgentDefSaved:
		var d AgentDef
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		if old := s.AgentDefs[d.Name]; old != nil {
			d.Rev = old.Rev
		}
		d.Rev++
		d.UpdatedAt, d.SavedAt = at, at
		s.AgentDefs[d.Name] = &d
	case EAgentDefRemoved:
		var d AgentDefRef
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		if s.AgentDefs[d.Name] == nil {
			return true, fmt.Errorf("no agent %s", d.Name)
		}
		delete(s.AgentDefs, d.Name)
	case EAgentDefShared:
		var d AgentDefShare
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		x := s.AgentDefs[d.Name]
		if x == nil {
			return true, fmt.Errorf("no agent %s", d.Name)
		}
		x.Share, x.UpdatedAt = d.Share, at
		x.Rev++
	case EAgentDefTransferred:
		var d AgentDefTransfer
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		x := s.AgentDefs[d.Name]
		if x == nil {
			return true, fmt.Errorf("no agent %s", d.Name)
		}
		x.Owner, x.UpdatedAt = d.Owner, at
		x.Rev++
	default:
		return false, nil
	}
	return true, nil
}
