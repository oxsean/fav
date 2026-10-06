package coord_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// A team-mode coordinator with one node dialed in: its owner's client reads the machine's sessions through node.call,
// with the same Hosts, cache and Source as over ssh.
func TestHostsReadAMachineThroughATeamCoordinator(t *testing.T) {
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	const owner, other = "u_ann", "u_bob"
	users := map[string]coord.User{owner: {ID: owner, Name: "ann"}, other: {ID: other, Name: "bob"}}
	c, err := coord.Open(coord.Options{Home: t.TempDir(), Version: "test", Remote: true,
		MachineOwner: func(string) string { return owner },
		Users:        func(id string) (coord.User, bool) { u, ok := users[id]; return u, ok }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	t.Cleanup(func() { cancel(); c.Close() })

	n := node.New(t.TempDir())
	n.Probe = func(string) agent.Check { return agent.Check{Installed: true, Auth: agent.AuthUnknown} }
	toNode, nodeEnd := wire.Pipe(c.NodeOptionsFor("mba"), wire.Options{Handler: n.Handler(remote.NewLocal("t"))})
	t.Cleanup(func() { nodeEnd.Close() })
	if err := c.Attach("mba", toNode, nil); err != nil {
		t.Fatal(err)
	}

	clientOf := func(user string) remote.NodeCaller {
		cli, _ := wire.Pipe(wire.Options{}, wire.Options{Handler: c.HandlerFor(coord.Principal{User: user})})
		t.Cleanup(func() { cli.Close() })
		return func(ctx context.Context, machine, method string, params json.RawMessage, out any) error {
			return cli.Call(ctx, coord.MNodeCall, coord.NodeCall{Machine: machine, Method: method, Params: params}, out)
		}
	}
	open := func(user string) *remote.Hosts {
		nc := remote.NewNodeCall("https://tend.example", clientOf(user))
		nc.SetMachines([]remote.Machine{{Name: "mba", Mine: user == owner}})
		h := remote.NewHostsOver(nc)
		t.Cleanup(h.Close)
		return h
	}

	call := context.Background()
	h := open(owner)
	recs, st := h.Sessions(call, "mba")
	if st.Err != nil || st.Version != "t" || len(recs) == 0 {
		t.Fatalf("the owner lists the machine: %d rows, %+v", len(recs), st)
	}
	if _, err := os.Stat(filepath.Join(remote.CacheDir("mba"), "sessions.json")); err != nil {
		t.Fatalf("the owner's machine is cached on disk: %v", err)
	}
	oauth := d.Get("oauth")
	src := h.Source(&tend.Rec{Host: "mba", Provider: oauth.Provider, SessionID: oauth.ID})
	last := src.Messages(-1, 2)
	if last.Err != nil || len(last.Msgs) != 2 {
		t.Fatalf("the last page: %v", last.Err)
	}
	if older := src.Messages(last.From, 2); older.Err != nil || len(older.Msgs) == 0 {
		t.Fatalf("the page before it: %v", older.Err)
	}
	gone := h.Source(&tend.Rec{Host: "mba", Provider: tend.ProviderClaude, SessionID: "0e0e0e0e-gone"})
	if p := gone.Messages(-1, 2); wire.Code(p.Err) != wire.CodeNotFound {
		t.Fatalf("a missing transcript comes back not_found through the coordinator: %v", p.Err)
	}
	if _, st := open(other).Sessions(call, "mba"); wire.Code(st.Err) != wire.CodeUnauthorized {
		t.Fatalf("someone else's machine is refused: %v", st.Err)
	}

	nodeEnd.Close()
	again, st := h.Sessions(call, "mba")
	if code := wire.Code(st.Err); code != wire.CodeClosed && code != wire.CodeOffline || len(again) != len(recs) {
		t.Fatalf("a machine whose node is gone shows the cached list: %d of %d, %v", len(again), len(recs), st.Err)
	}
	if _, err := h.Failed("mba"); err == nil {
		t.Fatal("and says the fetch failed")
	}
}
