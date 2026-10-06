// Package coordtest gives tests a mode 2 coordinator without HTTP: people reach it as clients over pipes, the way
// dial.Connect hands a client to the TUI and the CLI (a coord.Client whose Coord is nil), and nodes attach over pipes.
package coordtest

import (
	"context"
	"sync"
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/wire"
)

// Team is a running team-mode coordinator.
type Team struct {
	C      *coord.Coord
	t      testing.TB
	mu     sync.Mutex
	owners map[string]string // machine → its owner
}

func (tm *Team) owner(m string) string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.owners[m]
}

// NewTeam runs a team-mode coordinator in a temporary home for users; a machine attached later belongs to the user
// Attach names.
func NewTeam(t testing.TB, users ...coord.User) *Team {
	t.Helper()
	tm := &Team{t: t, owners: map[string]string{}}
	byID := map[string]coord.User{}
	for _, u := range users {
		byID[u.ID] = u
	}
	c, err := coord.Open(coord.Options{Home: t.TempDir(), Version: "test", Remote: true,
		MachineOwner: tm.owner,
		Users:        func(id string) (coord.User, bool) { u, ok := byID[id]; return u, ok }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	t.Cleanup(func() { cancel(); c.Close() })
	tm.C = c
	return tm
}

// Client is p's connection, as dial.Connect would give it; opt is the client end's options (its push handler).
func (tm *Team) Client(p coord.Principal, opt wire.Options) *coord.Client {
	a, b := wire.Pipe(opt, wire.Options{Handler: tm.C.HandlerFor(p)})
	tm.t.Cleanup(func() { a.Close(); b.Close() })
	return &coord.Client{Conn: a}
}

// Attach connects n as machine name, owned by owner; its agent CLIs count as installed without being run.
func (tm *Team) Attach(name, owner string, n *node.Node) {
	tm.t.Helper()
	tm.mu.Lock()
	tm.owners[name] = owner
	tm.mu.Unlock()
	n.Probe = func(string) agent.Check { return agent.Check{Installed: true, Auth: agent.AuthUnknown} }
	toNode, nodeEnd := wire.Pipe(tm.C.NodeOptions(), wire.Options{Handler: n.Handler(remote.NewLocal("test"))})
	tm.t.Cleanup(func() { nodeEnd.Close() })
	if err := tm.C.Attach(name, toNode, nil); err != nil {
		tm.t.Fatal(err)
	}
}
