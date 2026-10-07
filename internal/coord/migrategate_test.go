package coord

import (
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// handOffAndMigrate are the methods a handoff or migration calls on a machine's node.
var handOffAndMigrate = []string{remote.MHandoffFacts, remote.MHandoffPut, remote.MMemoryList, remote.MMemoryRead,
	remote.MMemoryTrash, remote.MMemoryRestore, remote.MMemoryPut, remote.MEnv, remote.MEnvFile, remote.MExportPlan,
	remote.MExportRead, remote.MExportDone, remote.MCopies, remote.MImportBegin, remote.MImportChunk, remote.MImportCommit,
	remote.MImportAbort}

// The handoff, memory, environment and migration methods reach a machine's node for its owner alone: whom its sessions
// are shared with, admins and anyone a machine under local are refused; local itself and mode 1's one user are not. A
// call that passes reaches the node, which answers what it answers (here: no such method).
func TestOnlyAMachinesOwnerHandsOffOrMigrates(t *testing.T) {
	e := served(t, map[string]string{"under-local": Owner.User})
	e.attach("mine", &fakeNode{sessions: []remote.Session{sess("m", 1, "/w", 5)}})
	e.attach("under-local", &fakeNode{sessions: []remote.Session{sess("l", 1, "/w", 5)}})
	if err := callAs(e.as(ann), MMachineSessions, "scope", task.SessionsSet{Machine: "mine", Users: []string{bob.User}}, nil); err != nil {
		t.Fatal(err)
	}
	call := func(e *env, p Principal, machine, method string) error {
		return callAs(e.as(p), MNodeCall, "", NodeCall{Machine: machine, Method: method,
			Params: mustJSON(remote.Ref{Provider: tend.ProviderClaude, SessionID: "m"})}, nil)
	}
	for _, method := range handOffAndMigrate {
		for _, c := range []struct {
			p       Principal
			machine string
			want    string
		}{{ann, "mine", wire.CodeUnknownMethod}, {bob, "mine", wire.CodeUnauthorized}, {root, "mine", wire.CodeUnauthorized},
			{cy, "mine", wire.CodeUnauthorized}, {root, "under-local", wire.CodeUnauthorized}, {ann, "under-local", wire.CodeUnauthorized},
			{Owner, "under-local", wire.CodeUnknownMethod}} {
			if err := call(e, c.p, c.machine, method); wire.Code(err) != c.want {
				t.Errorf("%s calls %s on %s: %v, want %q", c.p.User, method, c.machine, err, c.want)
			}
		}
	}

	single := newEnv(t, tend.Config{})
	single.start()
	single.attach("far", &fakeNode{sessions: []remote.Session{sess("x", 1, "/w", 5)}})
	for _, method := range handOffAndMigrate {
		if err := call(single, Owner, "far", method); wire.Code(err) != wire.CodeUnknownMethod {
			t.Errorf("mode 1's user calls %s on every machine: %v", method, err)
		}
	}
}

// node.repos looks for a repository's checkouts on a machine: its owner or an admin, as node.dirs.
func TestNodeReposIsForAMachinesOwnerOrAnAdmin(t *testing.T) {
	e := served(t, nil)
	e.attach("mine", &fakeNode{sessions: []remote.Session{sess("m", 1, "/w", 5)}})
	if err := callAs(e.as(ann), MMachineSessions, "scope", task.SessionsSet{Machine: "mine", Users: []string{bob.User}}, nil); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[Principal]string{ann: wire.CodeUnknownMethod, root: wire.CodeUnknownMethod, bob: wire.CodeUnauthorized,
		cy: wire.CodeUnauthorized} {
		err := callAs(e.as(p), MNodeCall, "", NodeCall{Machine: "mine", Method: node.MRepos, Params: mustJSON(map[string]string{"remote": "host/a/b"})}, nil)
		if wire.Code(err) != want {
			t.Errorf("%s calls node.repos: %v, want %q", p.User, err, want)
		}
	}
}

// A coordinator's hello tells clients it forwards the migration methods, in both modes.
func TestACoordinatorsHelloOffersMigrate(t *testing.T) {
	single := newEnv(t, tend.Config{})
	single.start()
	for name, cli := range map[string]*wire.Conn{"mode 2": served(t, nil).as(ann), "mode 1": single.as(Owner)} {
		var h remote.Hello
		if err := callAs(cli, remote.MHello, "", remote.HelloParams{Proto: wire.Proto, Role: "client"}, &h); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(h.Features, remote.FeatureMigrate) {
			t.Errorf("%s: hello features %v", name, h.Features)
		}
	}
}
