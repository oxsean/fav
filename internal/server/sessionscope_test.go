package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/dial"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// clientOf is a client connection signed in with a new personal token of user's.
func (r *rig) clientOf(user string) *wire.Conn {
	r.t.Helper()
	tok, _, err := r.team.NewCredential(store.KindToken, "c-"+user, user, 0)
	must(r.t, err)
	r.srv.sweep()
	c, err := dial.Dial(context.Background(), r.url, RoleClient, tok, wire.Options{})
	must(r.t, err)
	r.t.Cleanup(func() { c.Close() })
	return c
}

// A machine registered under local is read by no admin; token owner hands it to its owner, who reads it and says who
// else may, in the security log; local then reads it no more.
func TestAMachineHandedToItsOwnerIsReadByWhomTheySay(t *testing.T) {
	r := newRig(t)
	r.node(r.nodeT)
	r.waitMachine("n1", coord.MachineConnected)
	ann, _ := r.member("Ann")
	must(t, r.team.AddAdmit(store.Admit{Kind: store.AdmitEmail, Value: "root@corp.example", Role: store.RoleAdmin}))
	root, err := r.team.Admit(store.Identity{Provider: "gitea", Issuer: "https://git.example", Subject: "root", Email: "root@corp.example", EmailVerified: true}, "")
	must(t, err)
	local, err := dial.Dial(context.Background(), r.url, RoleClient, r.client, wire.Options{})
	must(t, err)
	t.Cleanup(func() { local.Close() })
	annC, rootC := r.clientOf(ann.ID), r.clientOf(root.ID)
	reads := func(c *wire.Conn) string {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return wire.Code(c.Call(ctx, coord.MNodeCall, coord.NodeCall{Machine: "n1", Method: remote.MList}, nil))
	}
	if got := reads(rootC); got != wire.CodeUnauthorized {
		t.Fatalf("an admin reads a machine under local: %q", got)
	}
	if got := reads(local); got != "" {
		t.Fatalf("local reads its machine: %q", got)
	}

	must(t, r.team.SetOwner(r.nodeID, ann.ID)) // tend-server token owner n1 <ann>
	r.srv.sweep()
	if got := r.srv.opt.Dir.MachineOwner("n1"); got != ann.ID {
		t.Fatalf("the server reads the new owner: %q", got)
	}
	for c, want := range map[*wire.Conn]string{annC: "", local: wire.CodeUnauthorized, rootC: wire.CodeUnauthorized} {
		if got := reads(c); got != want {
			t.Errorf("handed to ann: %q, want %q", got, want)
		}
	}
	if m := r.machine("n1"); m.Owner != ann.ID {
		t.Errorf("the machine list has its new owner: %+v", m)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	must(t, annC.CallCommand(ctx, coord.MMachineSessions, "scope", task.SessionsSet{Machine: "n1", Users: []string{root.ID}}, nil))
	if got := reads(rootC); got != "" {
		t.Errorf("the admin ann names reads it: %q", got)
	}
	log, _ := r.team.AuditLog(20)
	found := false
	for _, e := range log {
		found = found || e.Kind == coord.MMachineSessions && e.Actor == ann.ID && strings.Contains(e.Detail, "machine=n1") && strings.Contains(e.Detail, root.ID)
	}
	if !found {
		t.Errorf("the security log keeps the scope: %+v", log)
	}
}
