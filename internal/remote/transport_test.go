package remote

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// machineRig is one machine "m" answering with NewLocal, reached through one transport.
type machineRig struct {
	open func(target string, mine bool) *Hosts // target names where the machine is reached from (an ssh alias, a server)
	down func()                                // the machine stops answering
}

type transportKind struct {
	name   string
	shares bool // the transport knows machines that are not the viewer's own
	rig    func(t *testing.T) machineRig
}

var transports = []transportKind{
	{"ssh", false, sshRig},
	{"node.call", true, nodeCallRig},
}

func sshRig(t *testing.T) machineRig {
	var mu sync.Mutex
	var offline bool
	var clients []*Client
	dial := func(tend.Host) (*Client, error) {
		mu.Lock()
		defer mu.Unlock()
		if offline {
			return nil, &wire.Error{Code: wire.CodeOffline}
		}
		c := Pipe(NewLocal("t"))
		clients = append(clients, c)
		return c, nil
	}
	return machineRig{
		open: func(target string, _ bool) *Hosts {
			h := NewHostsDial([]tend.Host{{Name: "m", SSH: target}}, "", dial)
			t.Cleanup(h.Close)
			return h
		},
		down: func() {
			mu.Lock()
			defer mu.Unlock()
			offline = true
			for _, c := range clients {
				c.Close()
			}
		},
	}
}

// fakeCoordinator answers node.call as the coordinator does: the read goes on to the machine's node unchanged, an
// unknown machine is not_found, one whose node is not connected is offline.
type fakeCoordinator struct {
	mu    sync.Mutex
	nodes map[string]*Client // nil: known, not connected
}

func (f *fakeCoordinator) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method != "node.call" {
		return nil, &wire.Error{Code: wire.CodeUnknownMethod}
	}
	var p struct {
		Machine string          `json:"machine"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &wire.Error{Code: wire.CodeBadRequest}
	}
	f.mu.Lock()
	n, known := f.nodes[p.Machine]
	f.mu.Unlock()
	switch {
	case !known:
		return nil, &wire.Error{Code: wire.CodeNotFound, Detail: "machine " + p.Machine}
	case n == nil:
		return nil, &wire.Error{Code: wire.CodeOffline}
	}
	var out json.RawMessage
	err := n.Call(ctx, p.Method, p.Params, &out)
	return out, err
}

func nodeCallRig(t *testing.T) machineRig {
	f := &fakeCoordinator{nodes: map[string]*Client{"m": Pipe(NewLocal("t"))}}
	cli := pipeClient(t, f)
	call := func(ctx context.Context, machine, method string, params json.RawMessage, out any) error {
		return cli.Call(ctx, "node.call", map[string]any{"machine": machine, "method": method, "params": params}, out)
	}
	return machineRig{
		open: func(server string, mine bool) *Hosts {
			n := NewNodeCall(server, call)
			n.SetMachines([]Machine{{Name: "m", Mine: mine}})
			h := NewHostsOver(n)
			t.Cleanup(h.Close)
			return h
		},
		down: func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.nodes["m"].Close()
			f.nodes["m"] = nil
		},
	}
}

// fixtureMachine builds the machine's data and points this process's homes into it; the cache lands in its home too.
func fixtureMachine(t *testing.T) *fixture.Dataset {
	t.Helper()
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	return d
}

func cacheFiles(t *testing.T, name string) []string {
	t.Helper()
	var out []string
	for _, f := range []string{"sessions.json", "live.json"} {
		if _, err := os.Stat(filepath.Join(CacheDir(name), f)); err == nil {
			out = append(out, f)
		}
	}
	return out
}

func rewrite(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := fileio.WriteAtomic(path, 0o644, func(w io.Writer) error { _, err := w.Write(b); return err }); err != nil {
		t.Fatal(err)
	}
}

func TestSessionReadsOverEachTransport(t *testing.T) {
	ctx := context.Background()
	rows := []struct {
		name   string
		shared bool // run only where the transport knows shared machines
		run    func(t *testing.T, d *fixture.Dataset, r machineRig)
	}{
		{"list", false, func(t *testing.T, d *fixture.Dataset, r machineRig) {
			h := r.open("a", true)
			recs, st := h.Sessions(ctx, "m")
			if st.Err != nil || st.Version != "t" || st.At.IsZero() {
				t.Fatalf("%+v", st)
			}
			oauth := d.Get("oauth")
			found := false
			for _, rec := range recs {
				found = found || rec.SessionID == oauth.ID && rec.Host == "m"
			}
			if !found {
				t.Fatalf("the list carries the machine's sessions as its rows: %d rows", len(recs))
			}
			if got := cacheFiles(t, "m"); len(got) != 1 || got[0] != "sessions.json" {
				t.Fatalf("the viewer's own machine keeps its list on disk: %v", got)
			}
			if cached, _ := h.Cached("m"); len(cached) != len(recs) {
				t.Fatalf("cached %d of %d", len(cached), len(recs))
			}
		}},
		{"live", false, func(t *testing.T, d *fixture.Dataset, r machineRig) {
			oauth := d.Get("oauth")
			pid := os.Getpid()
			b, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": oauth.ID, "cwd": oauth.Cwd, "kind": "interactive", "status": "busy",
				"statusUpdatedAt": time.Now().UnixMilli()})
			if err := os.WriteFile(filepath.Join(d.Claude, "sessions", strconv.Itoa(pid)+".json"), b, 0o644); err != nil {
				t.Fatal(err)
			}
			h := r.open("a", true)
			live, err := h.Live(ctx, "m")
			if err != nil || live[oauth.ID].Status != "working" {
				t.Fatalf("%v %v", live, err)
			}
			if cached, at := h.CachedLive("m"); cached[oauth.ID].Status != "working" || at.IsZero() {
				t.Fatalf("cached live: %v %v", cached, at)
			}
			if got := cacheFiles(t, "m"); len(got) != 1 || got[0] != "live.json" {
				t.Fatalf("who runs is kept beside the list: %v", got)
			}
		}},
		{"paging", false, func(t *testing.T, d *fixture.Dataset, r machineRig) {
			oauth := d.Get("oauth")
			src := r.open("a", true).Source(&tend.Rec{Host: "m", Provider: oauth.Provider, SessionID: oauth.ID})
			last := src.Messages(-1, 2)
			if last.Err != nil || len(last.Msgs) != 2 || last.From <= 0 {
				t.Fatalf("the last page: %+v", last)
			}
			older := src.Messages(last.From, 2)
			if older.Err != nil || len(older.Msgs) == 0 || older.Msgs[len(older.Msgs)-1].Off >= last.Msgs[0].Off {
				t.Fatalf("the page before it: %+v", older)
			}
			if full := src.TextFull(last.Msgs[0].Off, "fallback"); full == "" || full == "fallback" {
				t.Fatalf("a full text by offset: %q", full)
			}
		}},
		{"stale", false, func(t *testing.T, d *fixture.Dataset, r machineRig) {
			oauth := d.Get("oauth")
			src := r.open("a", true).Source(&tend.Rec{Host: "m", Provider: oauth.Provider, SessionID: oauth.ID})
			last := src.Messages(-1, 2)
			if last.Err != nil {
				t.Fatal(last.Err)
			}
			rewrite(t, oauth.Path)
			if p := src.Messages(last.From, 2); !Stale(p.Err) {
				t.Fatalf("an older page of a rewritten transcript: %v", p.Err)
			}
			if p := src.Messages(-1, 2); p.Err != nil || len(p.Msgs) != 2 {
				t.Fatalf("read again from the end: %v", p.Err)
			}
			rewrite(t, oauth.Path)
			if p := src.Messages(-1, 2); !Stale(p.Err) {
				t.Fatalf("the tail of a rewritten transcript says so once: %v", p.Err)
			}
		}},
		{"not_found", false, func(t *testing.T, d *fixture.Dataset, r machineRig) {
			src := r.open("a", true).Source(&tend.Rec{Host: "m", Provider: tend.ProviderClaude, SessionID: "0e0e0e0e-gone"})
			if p := src.Messages(-1, 2); code(p.Err) != wire.CodeNotFound {
				t.Fatalf("a transcript that cannot be read is an error, not an empty head: %v", p.Err)
			}
		}},
		{"offline from cache", false, func(t *testing.T, d *fixture.Dataset, r machineRig) {
			h := r.open("a", true)
			recs, _ := h.Sessions(ctx, "m")
			r.down()
			again, st := h.Sessions(ctx, "m")
			if code(st.Err) != wire.CodeOffline || len(again) != len(recs) || st.At.IsZero() || st.Version != "t" {
				t.Fatalf("an offline machine shows its cached list: %d of %d, %+v", len(again), len(recs), st)
			}
			if at, err := h.Failed("m"); code(err) != wire.CodeOffline || at.IsZero() {
				t.Fatalf("failed: %v %v", at, err)
			}
			if _, err := h.Live(ctx, "m"); code(err) != wire.CodeOffline {
				t.Fatalf("live: %v", err)
			}
		}},
		{"cache dropped on a changed target", false, func(t *testing.T, d *fixture.Dataset, r machineRig) {
			h := r.open("a", true)
			if _, st := h.Sessions(ctx, "m"); st.Err != nil {
				t.Fatal(st.Err)
			}
			if _, err := h.Live(ctx, "m"); err != nil {
				t.Fatal(err)
			}
			moved := r.open("b", true)
			if recs, st := moved.Cached("m"); len(recs) != 0 || !st.At.IsZero() {
				t.Fatal("a machine reached from somewhere else does not show the old list")
			}
			if _, at := moved.CachedLive("m"); !at.IsZero() {
				t.Fatal("nor who ran there")
			}
		}},
		{"a shared machine writes nothing", true, func(t *testing.T, d *fixture.Dataset, r machineRig) {
			h := r.open("a", false)
			recs, st := h.Sessions(ctx, "m")
			if st.Err != nil || len(recs) == 0 {
				t.Fatalf("%d %v", len(recs), st.Err)
			}
			if _, err := h.Live(ctx, "m"); err != nil {
				t.Fatal(err)
			}
			if cached, _ := h.Cached("m"); len(cached) != len(recs) {
				t.Fatalf("held in memory: %d of %d", len(cached), len(recs))
			}
			if _, at := h.CachedLive("m"); at.IsZero() {
				t.Fatal("who runs is held in memory")
			}
			r.down()
			if again, st := h.Sessions(ctx, "m"); code(st.Err) != wire.CodeOffline || len(again) != len(recs) {
				t.Fatalf("offline, the list held in memory: %d %v", len(again), st.Err)
			}
			if _, err := h.Failed("m"); code(err) != wire.CodeOffline {
				t.Fatalf("failed: %v", err)
			}
			if _, err := os.Stat(CacheDir("m")); !os.IsNotExist(err) {
				t.Fatalf("nothing of a shared machine on disk: %v", err)
			}
			if recs, st := r.open("a", false).Cached("m"); len(recs) != 0 || !st.At.IsZero() {
				t.Fatal("the next process starts without it")
			}
		}},
	}
	for _, tr := range transports {
		for _, row := range rows {
			if row.shared && !tr.shares {
				continue
			}
			t.Run(tr.name+"/"+row.name, func(t *testing.T) {
				d := fixtureMachine(t)
				row.run(t, d, tr.rig(t))
			})
		}
	}
}

func TestSwitchingToAServerDropsTheSSHCache(t *testing.T) {
	fixtureMachine(t)
	ctx := context.Background()
	if _, st := sshRig(t).open("mba", true).Sessions(ctx, "m"); st.Err != nil {
		t.Fatal(st.Err)
	}
	h := nodeCallRig(t).open("https://tend.example", true)
	if recs, _ := h.Cached("m"); len(recs) != 0 {
		t.Fatal("the list fetched over ssh is not shown as the server's")
	}
	if _, st := h.Sessions(ctx, "m"); st.Err != nil {
		t.Fatal(st.Err)
	}
	if recs, _ := sshRig(t).open("mba", true).Cached("m"); len(recs) != 0 {
		t.Fatal("nor the server's as the one over ssh")
	}
}

func TestNodeCallReachesOnlyItsMachines(t *testing.T) {
	fixtureMachine(t)
	ctx := context.Background()
	h := nodeCallRig(t).open("https://tend.example", true)
	if err := h.Call(ctx, "other", MEcho, Text{"x"}, nil); code(err) != wire.CodeNotFound {
		t.Fatalf("a machine not listed: %v", err)
	}
	if names := h.Names(); len(names) != 1 || names[0] != "m" {
		t.Fatalf("names: %v", names)
	}
	if _, ok := h.Host("m"); ok {
		t.Fatal("a machine read through a server has no ssh host")
	}
	if _, ok := h.ResumeCommand(&tend.Rec{Host: "m", SessionID: "s1"}); ok {
		t.Fatal("nor an ssh resume")
	}
	var got Text
	if err := h.Call(ctx, "m", MEcho, Text{"中文 ✓"}, &got); err != nil || got.Text != "中文 ✓" {
		t.Fatalf("echo: %q %v", got.Text, err)
	}
	if hello, err := h.Hello(ctx, "m"); err != nil || hello.Proto != wire.Proto || hello.Version != "t" {
		t.Fatalf("hello: %+v %v", hello, err)
	}
}

// TestKeptAreTheServersMachinesOnDisk: the machines kept on disk from one server, not another's nor those over ssh.
func TestKeptAreTheServersMachinesOnDisk(t *testing.T) {
	fixtureMachine(t)
	ctx := context.Background()
	if _, st := sshRig(t).open("mba", true).Sessions(ctx, "m"); st.Err != nil {
		t.Fatal(st.Err)
	}
	if kept := NewNodeCall("https://tend.example", nil).Kept(); len(kept) != 0 {
		t.Fatalf("a list fetched over ssh is not the server's: %v", kept)
	}
	if _, st := nodeCallRig(t).open("https://tend.example", true).Sessions(ctx, "m"); st.Err != nil {
		t.Fatal(st.Err)
	}
	if kept := NewNodeCall("https://tend.example", nil).Kept(); len(kept) != 1 || kept[0] != (Machine{Name: "m", Mine: true}) {
		t.Fatalf("kept: %v", kept)
	}
	if kept := NewNodeCall("https://other.example", nil).Kept(); len(kept) != 0 {
		t.Fatalf("another server's: %v", kept)
	}
}

// TestForgetDropsWhatIsNoLongerTheViewers: a machine the server's whole list no longer gives as the viewer's own (now
// shared by another person, or gone) loses what was kept of it from that server; another server's is left alone.
func TestForgetDropsWhatIsNoLongerTheViewers(t *testing.T) {
	fixtureMachine(t)
	const server = "https://tend.example"
	keep := func() {
		t.Helper()
		if _, st := nodeCallRig(t).open(server, true).Sessions(context.Background(), "m"); st.Err != nil {
			t.Fatal(st.Err)
		}
	}
	kept := func() []Machine { return NewNodeCall(server, nil).Kept() }
	keep()
	NewNodeCall(server, nil).Forget([]Machine{{Name: "m", Mine: true}})
	NewNodeCall("https://other.example", nil).Forget(nil)
	if len(kept()) != 1 {
		t.Fatalf("still the viewer's, or another server's list: kept %v", kept())
	}
	NewNodeCall(server, nil).Forget([]Machine{{Name: "m"}})
	if _, err := os.Stat(CacheDir("m")); !os.IsNotExist(err) || len(kept()) != 0 {
		t.Fatalf("shared now: %v, kept %v", err, kept())
	}
	keep()
	NewNodeCall(server, nil).Forget(nil)
	if len(kept()) != 0 {
		t.Fatalf("not listed any more: kept %v", kept())
	}
}
