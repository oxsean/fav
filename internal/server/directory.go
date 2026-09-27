package server

import (
	"crypto/subtle"
	"slices"
	"strings"
	"sync"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
)

// Directory is the team as the server and the coordinator see it: the users and the live credentials, read from the
// database and kept in memory; Reload reads them again (the server does every few seconds, and after it changes
// them, so tend-server commands run beside it take effect).
type Directory struct {
	team  *store.Team
	mu    sync.RWMutex
	creds map[string]store.Credential // live ones, by id
	users map[string]store.User
}

func NewDirectory(team *store.Team) (*Directory, error) {
	d := &Directory{team: team}
	return d, d.Reload()
}

func (d *Directory) Team() *store.Team { return d.team }

func (d *Directory) Reload() error {
	cs, err := d.team.ActiveCredentials()
	if err != nil {
		return err
	}
	us, err := d.team.Users()
	if err != nil {
		return err
	}
	creds := map[string]store.Credential{}
	for _, c := range cs {
		creds[c.ID] = c
	}
	for id, c := range creds { // a browser session started with a token lasts only as long as the token
		if parent, ok := strings.CutPrefix(c.Name, viaToken); ok && c.Kind == store.KindWeb {
			if _, live := creds[parent]; !live {
				delete(creds, id)
			}
		}
	}
	users := map[string]store.User{}
	for _, u := range us {
		users[u.ID] = u
	}
	d.mu.Lock()
	d.creds, d.users = creds, users
	d.mu.Unlock()
	return nil
}

// viaToken prefixes the name of a browser session that signed in with a personal token: the token's id follows.
const viaToken = "token:"

// find is the live credential whose secret is secret, of one of kinds, with its owner.
func (d *Directory) find(secret string, kinds ...string) (store.Credential, store.User, bool) {
	if secret == "" {
		return store.Credential{}, store.User{}, false
	}
	want := store.Sum(secret)
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, c := range d.creds {
		if subtle.ConstantTimeCompare([]byte(c.Sum), []byte(want)) == 1 && slices.Contains(kinds, c.Kind) {
			u, ok := d.users[c.Owner]
			return c, u, ok && !u.Disabled
		}
	}
	return store.Credential{}, store.User{}, false
}

// live: credential id still lets its holder in.
func (d *Directory) live(id string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.creds[id]
	return ok
}

// MachineOwner is the user who owns machine: the owner of its node token.
func (d *Directory) MachineOwner(machine string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, c := range d.creds {
		if c.Kind == store.KindNode && c.Name == machine {
			return c.Owner
		}
	}
	return ""
}

// NodeNames are the machines that have a node token.
func (d *Directory) NodeNames() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var out []string
	for _, c := range d.creds {
		if c.Kind == store.KindNode {
			out = append(out, c.Name)
		}
	}
	slices.Sort(out)
	return out
}

// User is id as the coordinator needs them.
func (d *Directory) User(id string) (coord.User, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	u, ok := d.users[id]
	if !ok {
		return coord.User{}, false
	}
	return coord.User{ID: u.ID, Name: u.Name, Email: u.Email, Admin: u.Role == store.RoleAdmin, Disabled: u.Disabled}, true
}

// principal is who a client credential's holder is to the coordinator: the server host is the coordinator's owner.
func principal(u store.User) coord.Principal {
	if u.ID == store.LocalUser {
		return coord.Owner
	}
	return coord.Principal{User: u.ID, Admin: u.Role == store.RoleAdmin}
}
