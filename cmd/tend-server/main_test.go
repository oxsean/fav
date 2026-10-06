package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// token owner hands a machine registered under local to a real person: the token and the node it is bound to stay, the
// security log keeps it, and a missing machine or user, or a disabled one, is refused.
func TestTokenOwnerHandsAMachineToItsOwner(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(dbPath(os.Getenv("TEND_HOME"))), 0o700); err != nil {
		t.Fatal(err)
	}
	team, err := openTeam()
	if err != nil {
		t.Fatal(err)
	}
	_, node, err := team.NewCredential(store.KindNode, "mba", store.LocalUser, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := team.Bind(node.ID, "n_1", "mba.local"); err != nil {
		t.Fatal(err)
	}
	person := func(subject, name string) store.User {
		invite, err := team.NewInvite(store.RoleMember, store.LocalUser, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		u, err := team.Admit(store.Identity{Provider: "github", Subject: subject, Username: name}, invite)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	ann, bob := person("1", "ann"), person("2", "bob")
	off := true
	if err := team.SetUser(bob.ID, nil, &off); err != nil {
		t.Fatal(err)
	}
	team.Close()

	for _, c := range []struct {
		args []string
		want func() string // after run: it sets the language
	}{
		{[]string{"token", "owner", "mba"}, func() string { return i18n.T("cli.server.token_owner_usage") }},
		{[]string{"token", "owner", "nowhere", ann.ID}, func() string { return i18n.F("cli.server.token_missing", "nowhere") }},
		{[]string{"token", "owner", "mba", "u_nobody"}, func() string { return i18n.F("cli.server.no_user", "u_nobody") }},
		{[]string{"token", "owner", "mba", bob.ID}, func() string { return i18n.F("cli.server.user_disabled", bob.ID) }},
	} {
		if err := run(c.args); err == nil || err.Error() != c.want() {
			t.Errorf("%v: %v, want %q", c.args, err, c.want())
		}
	}
	if err := run([]string{"token", "owner", "mba", ann.ID}); err != nil {
		t.Fatal(err)
	}

	team, err = openTeam()
	if err != nil {
		t.Fatal(err)
	}
	defer team.Close()
	c, err := team.NodeCredential("mba")
	if err != nil || c.ID != node.ID || c.Owner != ann.ID || c.NodeID != "n_1" || c.Sum != node.Sum {
		t.Fatalf("mba is ann's, with the same token and node: %+v %v", c, err)
	}
	log, err := team.AuditLog(10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range log {
		found = found || e.Kind == "machine" && e.Actor == store.LocalUser && strings.Contains(e.Detail, "mba") && strings.Contains(e.Detail, ann.ID)
	}
	if !found {
		t.Fatalf("the security log has the change: %+v", log)
	}
}
