package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/dial"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/testkit"
	"github.com/oxsean/fav/internal/wire"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestListenNeedsLoopbackTailnetOrTLS(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:7777": true, "localhost:7777": true, "[::1]:7777": true, "100.101.8.10:7777": true,
		"[fd7a:115c:a1e0::1]:7777": true, "0.0.0.0:7777": false, "192.168.1.5:7777": false, ":7777": false,
	} {
		if err := CheckListen(addr, false, false); (err == nil) != ok {
			t.Errorf("%s: %v", addr, err)
		}
	}
	if CheckListen("0.0.0.0:7777", true, false) != nil || CheckListen("0.0.0.0:7777", false, true) != nil {
		t.Error("TLS or an explicit plain listen allows any address")
	}
}

func TestTokensJSONMovesIntoTheDatabase(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "server"), 0o700)
	legacy := `{"tokens":[{"name":"mba","role":"node","sum":"` + store.Sum("tend_old-node") + `","bound":"n_1","bound_host":"mba.local"},` +
		`{"name":"laptop","role":"client","sum":"` + store.Sum("tend_old-client") + `"}]}`
	os.WriteFile(filepath.Join(home, "server", "tokens.json"), []byte(legacy), 0o600)
	team, err := store.OpenTeam(filepath.Join(home, "coord", store.File))
	if err != nil {
		t.Fatal(err)
	}
	defer team.Close()
	if n, err := ImportTokens(home, team); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(home, "server", "tokens.json")); !os.IsNotExist(err) {
		t.Fatal("tokens.json is kept aside once moved")
	}
	d, _ := NewDirectory(team)
	if c, u, ok := d.find("tend_old-node", store.KindNode); !ok || c.Name != "mba" || c.NodeID != "n_1" || u.ID != store.LocalUser {
		t.Fatalf("the node token still works and stays bound: %+v %+v", c, u)
	}
	if _, u, ok := d.find("tend_old-client", store.KindToken); !ok || principal(u) != coord.Owner {
		t.Fatalf("a client token of the host is the coordinator's owner: %+v", u)
	}
	if n, err := ImportTokens(home, team); err != nil || n != 0 {
		t.Fatalf("nothing left to move: %d %v", n, err)
	}
}

type rig struct {
	t      *testing.T
	home   string
	url    string
	c      *coord.Coord
	srv    *Server
	team   *store.Team
	nodeT  string
	nodeID string // the node token's credential id
	client string
	nodeAt string // the node's home: one machine unless a test names another
}

func newRig(t *testing.T, logins ...tend.Login) *rig {
	r := &rig{t: t, home: t.TempDir(), nodeAt: t.TempDir()}
	var err error
	if r.team, err = store.OpenTeam(filepath.Join(r.home, "coord", store.File)); err != nil {
		t.Fatal(err)
	}
	var c store.Credential
	if r.nodeT, c, err = r.team.NewCredential(store.KindNode, "n1", store.LocalUser, 0); err != nil {
		t.Fatal(err)
	}
	r.nodeID = c.ID
	if r.client, _, err = r.team.NewCredential(store.KindToken, "me", store.LocalUser, 0); err != nil {
		t.Fatal(err)
	}
	dir, err := NewDirectory(r.team)
	if err != nil {
		t.Fatal(err)
	}
	r.c, err = coord.Open(coord.Options{Home: r.home, Version: "test", Remote: true, MachineOwner: dir.MachineOwner, Users: dir.User,
		Config: tend.Config{Agents: []tend.AgentProfile{{Name: "fake", Provider: "fake"}}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go r.c.Run(ctx)
	srv := New(Options{Home: r.home, Coord: r.c, Dir: dir, Config: tend.ServerConfig{Logins: logins}})
	srv.sweep()
	r.srv = srv
	hs := httptest.NewServer(srv.Handler())
	go func() {
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				srv.sweep()
			}
		}
	}()
	r.url = hs.URL
	t.Cleanup(func() { cancel(); hs.CloseClientConnections(); hs.Close(); r.c.Close(); r.team.Close() })
	return r
}

// node connects a node whose runs end at once with some output.
func (r *rig) node(token string) *wire.Conn {
	r.t.Helper()
	c, err := r.dialNode(r.nodeAt, token)
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

func (r *rig) dialNode(home, token string) (*wire.Conn, error) {
	n := node.New(home)
	n.Limits = tend.NodeConfig{AllowDirs: []string{os.TempDir(), r.t.TempDir()}}
	n.Launch = func(dir string, spec node.Spec) (string, error) {
		os.WriteFile(filepath.Join(dir, "output.log"), []byte("done here\n"), 0o600)
		return "", os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"rev":1,"state":"exited","exit_code":0,"session":"`+spec.Session+`"}`), 0o600)
	}
	c, err := dial.Dial(context.Background(), r.url, RoleNode, token, wire.Options{Handler: n.Handler(remote.NewLocal("n1"))})
	if err == nil {
		r.t.Cleanup(func() { c.Close() })
	}
	return c, err
}

func (r *rig) machine(name string) coord.Machine {
	for _, m := range r.c.Machines(context.Background(), false).Machines {
		if m.Name == name {
			return m
		}
	}
	return coord.Machine{}
}

func (r *rig) waitMachine(name, state string) {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for r.machine(name).State != state {
		if time.Now().After(deadline) {
			r.t.Fatalf("%s is %+v, want %s", name, r.machine(name), state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestANodeAndAClientMeetAtTheServer(t *testing.T) {
	r := newRig(t)
	r.waitMachine("n1", coord.MachineOffline)
	r.node(r.nodeT)
	r.waitMachine("n1", coord.MachineConnected)

	cl, err := dial.Connect(r.url, writeToken(t, r.client), wire.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx := context.Background()
	dir := t.TempDir()
	var tk task.Task
	if err := cl.CallCommand(ctx, coord.MTaskCreate, "c1", coord.TaskCreate{Title: "x", Dir: dir, Machine: "n1", Agent: "fake"}, &tk); err != nil {
		t.Fatal(err)
	}
	var run task.Run
	if err := cl.CallCommand(ctx, coord.MRunDispatch, "c2", coord.Dispatch{Task: tk.ID, Runner: node.RunnerBackground}, &run); err != nil || run.Machine != "n1" {
		t.Fatalf("%+v %v", run, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var st task.State
		if err := cl.Call(ctx, coord.MStateGet, nil, &st); err != nil {
			t.Fatal(err)
		}
		if got := st.Runs[run.ID]; got != nil && got.State == task.Exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%+v", st.Runs[run.ID])
		}
		time.Sleep(50 * time.Millisecond)
	}
	var tail node.Tail
	if err := cl.Call(ctx, coord.MRunTail, coord.TailParams{Run: run.ID, Before: -1}, &tail); err != nil || tail.Text != "done here\n" {
		t.Fatalf("output through the server: %+v %v", tail, err)
	}
}

func writeToken(t *testing.T, tok string) string {
	p := filepath.Join(t.TempDir(), "token")
	os.WriteFile(p, []byte(tok+"\n"), 0o600)
	return p
}

func TestWrongTokensAreRefused(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := dial.Dial(ctx, r.url, RoleNode, r.client, wire.Options{}); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a client token cannot be a node: %v", err)
	}
	if _, err := dial.Dial(ctx, r.url, RoleClient, r.nodeT, wire.Options{}); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a node token cannot be a client: %v", err)
	}
	if _, err := dial.Dial(ctx, r.url, RoleClient, "tend_nope", wire.Options{}); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("an unknown token: %v", err)
	}
}

func TestTheLaterNodeConnectionWinsAndRevokingDropsIt(t *testing.T) {
	r := newRig(t)
	first := r.node(r.nodeT)
	r.waitMachine("n1", coord.MachineConnected)
	second := r.node(r.nodeT)
	select {
	case <-first.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the earlier connection of the same node stays open")
	}
	r.waitMachine("n1", coord.MachineConnected)
	if err := r.team.Revoke(r.nodeID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("a revoked token keeps its connection")
	}
}

func TestATokenReplacedUnderTheSameNameDropsTheOldConnection(t *testing.T) {
	r := newRig(t)
	old := r.node(r.nodeT)
	r.waitMachine("n1", coord.MachineConnected)
	if err := r.team.Revoke(r.nodeID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.team.NewCredential(store.KindNode, "n1", store.LocalUser, 0); err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the replaced token keeps its connection")
	}
}

func TestANodeTokenStaysWithTheMachineThatFirstUsedIt(t *testing.T) {
	r := newRig(t)
	first := r.node(r.nodeT)
	r.waitMachine("n1", coord.MachineConnected)
	if c, err := r.team.NodeCredential("n1"); err != nil || c.NodeID == "" {
		t.Fatalf("the first connection binds the token: %+v %v", c, err)
	}
	first.Close()
	r.waitMachine("n1", coord.MachineOffline)
	other, err := r.dialNode(t.TempDir(), r.nodeT)
	if err == nil {
		select {
		case <-other.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("another machine with the token is refused")
		}
	}
	if r.machine("n1").State == coord.MachineConnected {
		t.Fatal("the other machine never became n1")
	}
	if err := r.team.Rebind(r.nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.dialNode(t.TempDir(), r.nodeT); err != nil {
		t.Fatal(err)
	}
	r.waitMachine("n1", coord.MachineConnected)
}
