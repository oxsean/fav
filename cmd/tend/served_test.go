package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/coordtest"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// servedCLI: this machine (ann-mac) is Ann's in mode 2; she owns mba too, Bob owns bobs and lets her read it. Every
// node answers with this fixture machine's sessions. It counts the dials of the server; down makes them fail.
func servedCLI(t *testing.T) (dials *atomic.Int32, down *atomic.Bool) {
	t.Helper()
	d := machine(t)
	served(t, "tend.example")
	ann, bob := coord.User{ID: "u_ann", Name: "Ann"}, coord.User{ID: "u_bob", Name: "Bob"}
	tm := coordtest.NewTeam(t, ann, bob)
	nodes := map[string]*node.Node{}
	for name, owner := range map[string]string{"ann-mac": ann.ID, "mba": ann.ID, "bobs": bob.ID} {
		nodes[name] = node.New(t.TempDir())
		tm.Attach(name, owner, nodes[name])
	}
	os.MkdirAll(filepath.Join(d.Home, "node"), 0o700)
	os.WriteFile(filepath.Join(d.Home, "node", "id"), []byte(nodes["ann-mac"].ID()+"\n"), 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tm.Client(coord.Principal{User: bob.ID}, wire.Options{}).CallCommand(ctx, coord.MMachineSessions, "share-bobs",
		task.SessionsSet{Machine: "bobs", Users: []string{ann.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	dials, down = &atomic.Int32{}, &atomic.Bool{}
	old := dialServer
	dialServer = func(context.Context) (*coord.Client, error) {
		dials.Add(1)
		if down.Load() {
			return nil, &wire.Error{Code: wire.CodeOffline}
		}
		return tm.Client(coord.Principal{User: ann.ID}, wire.Options{}), nil
	}
	t.Cleanup(func() { dialServer = old })
	return dials, down
}

// TestServedSessionsReadThroughTheServer: host:all is this machine plus the machines the server lets the viewer read,
// never this one twice; the own machine's list is kept on disk, the shared one's is not.
func TestServedSessionsReadThroughTheServer(t *testing.T) {
	servedCLI(t)
	local, _ := listedBy(t, "sessions", "--json")
	all, stderr := listedBy(t, "sessions", "host:all", "--json")
	got := hostsIn(all)
	if got["mba"] == 0 || got["bobs"] == 0 || got["ann-mac"] != 0 || got[""] != len(local) || stderr != "" {
		t.Fatalf("rows by host %v, stderr %q", got, stderr)
	}
	if _, err := os.Stat(filepath.Join(remote.CacheDir("mba"), "sessions.json")); err != nil {
		t.Fatalf("the own machine's list is kept: %v", err)
	}
	if _, err := os.Stat(remote.CacheDir("bobs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing of the shared machine on disk: %v", err)
	}
	if rows, _ := listedBy(t, "sessions", "host:bobs", "--json"); hostsIn(rows)["bobs"] != len(rows) || len(rows) == 0 {
		t.Fatalf("host: takes the server's machine names: %v", hostsIn(rows))
	}
}

// TestServedSessionsWithTheServerDown: one line says the server is down; the own machine shows its cache, the shared
// one is gone.
func TestServedSessionsWithTheServerDown(t *testing.T) {
	_, down := servedCLI(t)
	listedBy(t, "sessions", "host:all", "--json")
	down.Store(true)
	all, stderr := listedBy(t, "sessions", "host:all", "--json")
	if got := hostsIn(all); got["mba"] == 0 || got["bobs"] != 0 {
		t.Fatalf("rows by host %v", got)
	}
	want := i18n.F("remote.server_down", remote.Reason(&wire.Error{Code: wire.CodeOffline}))
	if strings.TrimSpace(stderr) != want {
		t.Fatalf("one line for the server, none per machine: %q", stderr)
	}
}

// TestFzfListKeepsTheThirtySecondRule: fzf lists in a new process per key: an own machine's list fetched within 30
// seconds is used without dialing, a failed dial is not tried again within 30 seconds, and shared machines are left
// to tend tui.
func TestFzfListKeepsTheThirtySecondRule(t *testing.T) {
	dials, down := servedCLI(t)
	t.Setenv("FZF_PROMPT", i18n.T("view.sessions")+" > ")
	fzfList := func() string { return stdoutOf(t, func() { run([]string{"fzf-list", "host:all"}) }) }

	listedBy(t, "sessions", "host:all", "--json")
	before := dials.Load()
	out := fzfList()
	if dials.Load() != before || !strings.Contains(out, "mba:") || strings.Contains(out, "bobs:") {
		t.Fatalf("a fresh own list without a dial, no shared machine: %d dials\n%s", dials.Load()-before, out)
	}
	if !strings.Contains(out, i18n.T("cli.fzf.shared_in_tui")) {
		t.Fatalf("a row says where shared machines are:\n%s", out)
	}

	ageList(t, "mba", time.Hour)
	down.Store(true)
	before = dials.Load()
	fzfList()
	if dials.Load() != before+1 {
		t.Fatalf("a stale list dials once: %d", dials.Load()-before)
	}
	fzfList()
	if dials.Load() != before+1 {
		t.Fatalf("a failed dial is not tried again within 30 seconds: %d", dials.Load()-before)
	}
	b, err := os.ReadFile(serverDownPath())
	if err != nil || strings.Contains(string(b), "session") && !strings.Contains(string(b), "failed_at") {
		t.Fatalf("the failure is recorded, nothing else: %s %v", b, err)
	}
	if info, err := os.Stat(serverDownPath()); err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("mode: %v %v", info.Mode(), err)
	}
}

// ageList makes machine's kept list as old as age.
func ageList(t *testing.T, machine string, age time.Duration) {
	t.Helper()
	path := filepath.Join(remote.CacheDir(machine), "sessions.json")
	var c map[string]any
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if err != nil {
		t.Fatal(err)
	}
	c["at"] = time.Now().Add(-age)
	b, _ = json.Marshal(c)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestServedWritesGoToTheViewersOwnMachines: host:sid writes go through the server's node.call put on the viewer's
// own machine; a machine shared with the viewer is refused, and so is any write while the server is down.
func TestServedWritesGoToTheViewersOwnMachines(t *testing.T) {
	_, down := servedCLI(t)
	all, _ := listedBy(t, "sessions", "host:all", "--json")
	sidOn := func(host string) string {
		for _, r := range all {
			if r.Host == host {
				return r.SessionID
			}
		}
		t.Fatalf("no row on %s", host)
		return ""
	}
	mba, bobs := sidOn("mba"), sidOn("bobs")
	var err error
	stdoutOf(t, func() { err = run([]string{"done", "mba:" + mba}) })
	if err != nil {
		t.Fatal(err)
	}
	st, err := tend.Open()
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(st.All(), func(r *tend.Rec) bool { return r.SessionID == mba }); i < 0 || st.All()[i].Status != tend.StatusDone {
		t.Fatal("mba's node wrote it (every node here answers with this fixture machine)")
	}
	if err := run([]string{"favorite", "bobs:" + bobs}); err == nil || err.Error() != i18n.F("cli.remote.shared_write", "bobs") {
		t.Fatalf("a shared machine: %v", err)
	}
	down.Store(true)
	want := i18n.F("remote.put_server_down", remote.Reason(&wire.Error{Code: wire.CodeOffline}))
	if err := run([]string{"favorite", "mba:" + mba}); err == nil || err.Error() != want {
		t.Fatalf("the server down: %v, want %q", err, want)
	}
}
