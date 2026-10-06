package coord

import (
	"cmp"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/wire"
)

// PeopleParams is people.names: the users whose names the caller wants.
type PeopleParams struct {
	IDs []string `json:"ids"`
}

// People answers people.names: the name of each user the caller has reason to know (knows); the others are left out,
// and the caller shows their ids.
type People struct {
	Names    map[string]string `json:"names"`
	Disabled []string          `json:"disabled,omitempty"` // of those, the ones offboarded
}

const maxNames = 500

func (c *Coord) peopleNames(p Principal, r *wire.Request) (any, error) {
	var pp PeopleParams
	if err := r.Decode(&pp); err != nil {
		return nil, err
	}
	if len(pp.IDs) > maxNames {
		return nil, bad("ids")
	}
	out := People{Names: map[string]string{}}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range pp.IDs {
		if _, done := out.Names[id]; done || id == "" {
			continue
		}
		u, ok := c.user(id)
		if !ok || !c.knows(p, id) {
			continue
		}
		local, _, _ := strings.Cut(u.Email, "@")
		out.Names[id] = cmp.Or(u.Name, local, id)
		if u.Disabled {
			out.Disabled = append(out.Disabled, id)
		}
	}
	slices.Sort(out.Disabled)
	return out, nil
}

// knows: p has reason to know user id: themselves, the owner of a machine p sees, the owner or a member of a project p
// sees, the owner or approver of a task p reads. The caller holds mu.
func (c *Coord) knows(p Principal, id string) bool {
	if id == p.User {
		return true
	}
	for name := range c.ms {
		if c.ownerOf(name) == id && c.canSee(p, name) {
			return true
		}
	}
	for _, pr := range c.st.Projects {
		if (pr.Owner == id || pr.Members[id] != "") && c.seesProject(p, pr) {
			return true
		}
	}
	for _, t := range c.st.Tasks {
		if (t.Owner == id || t.Approver == id) && c.canRead(c.st, p, t) {
			return true
		}
	}
	return false
}
