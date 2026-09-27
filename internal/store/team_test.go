package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTeam(t *testing.T) *Team {
	t.Helper()
	tm, err := OpenTeam(filepath.Join(t.TempDir(), File))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tm.Close() })
	return tm
}

func gitea(subject, email string, verified bool) Identity {
	return Identity{Provider: "gitea", Issuer: "https://git.example", Subject: subject, Username: "u" + subject, Email: email, EmailVerified: verified}
}

func TestOnlyAdmittedPeopleGetIn(t *testing.T) {
	tm := openTeam(t)
	if _, err := tm.Admit(gitea("1", "ann@corp.example", true), ""); !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("no rule, no invite: %v", err)
	}
	must(t, tm.AddAdmit(Admit{Kind: AdmitEmail, Value: "ann@corp.example", Role: RoleAdmin}))
	if _, err := tm.Admit(gitea("1", "ann@corp.example", false), ""); !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("an unverified email counts for nothing: %v", err)
	}
	ann, err := tm.Admit(gitea("1", "ann@corp.example", true), "")
	if err != nil || ann.Role != RoleAdmin || ann.Email != "ann@corp.example" {
		t.Fatalf("%+v %v", ann, err)
	}
	again, err := tm.Admit(gitea("1", "changed@corp.example", false), "")
	if err != nil || again.ID != ann.ID {
		t.Fatalf("a known identity is its user whatever its email says now: %+v %v", again, err)
	}
	if _, err := tm.Admit(Identity{Provider: "github", Issuer: "https://github.com", Subject: "9", Email: "ann@corp.example", EmailVerified: true}, ""); !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("another account with ann's email is not merged into hers: %v", err)
	}

	must(t, tm.AddAdmit(Admit{Kind: AdmitDomain, Value: "corp.example", Role: RoleMember}))
	bob, err := tm.Admit(gitea("2", "bob@corp.example", true), "")
	if err != nil || bob.Role != RoleMember {
		t.Fatalf("the domain lets bob in as a member: %+v %v", bob, err)
	}
	must(t, tm.AddAdmit(Admit{Kind: AdmitLogin, Value: "gitea:u3", Role: RoleMember}))
	if cy, err := tm.Admit(gitea("3", "", false), ""); err != nil || cy.Username != "u3" {
		t.Fatalf("a provider's username: %+v %v", cy, err)
	}

	disabled := true
	must(t, tm.SetUser(bob.ID, nil, &disabled))
	if _, err := tm.Admit(gitea("2", "bob@corp.example", true), ""); !errors.Is(err, ErrDisabled) {
		t.Fatalf("a disabled user: %v", err)
	}
}

func TestAnInviteLetsOnePersonInOnce(t *testing.T) {
	tm := openTeam(t)
	secret, err := tm.NewInvite(RoleMember, LocalUser, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	dee, err := tm.Admit(gitea("4", "dee@else.example", false), secret)
	if err != nil || dee.Role != RoleMember {
		t.Fatalf("%+v %v", dee, err)
	}
	if _, err := tm.Admit(gitea("5", "", false), secret); !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("a used invite: %v", err)
	}
	old, _ := tm.NewInvite(RoleMember, LocalUser, -time.Minute)
	if _, err := tm.Admit(gitea("6", "", false), old); !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("an expired invite: %v", err)
	}
}

func TestACredentialIsItsSecretsHashAndGoesWhenRevoked(t *testing.T) {
	tm := openTeam(t)
	secret, c, err := tm.NewCredential(KindNode, "mba", LocalUser, 0)
	if err != nil || c.Kind != KindNode || c.Owner != LocalUser {
		t.Fatalf("%+v %v", c, err)
	}
	if _, _, err := tm.NewCredential(KindNode, "mba", LocalUser, 0); !errors.Is(err, ErrExists) {
		t.Fatalf("two live node tokens for one machine: %v", err)
	}
	found := func() (Credential, bool) {
		cs, err := tm.ActiveCredentials()
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range cs {
			if x.Sum == Sum(secret) {
				return x, true
			}
		}
		return Credential{}, false
	}
	if got, ok := found(); !ok || got.ID != c.ID || got.Sum == secret {
		t.Fatalf("stored as its hash only: %+v %v", got, ok)
	}
	must(t, tm.Bind(c.ID, "n_1", "host-a"))
	if err := tm.Bind(c.ID, "n_2", "host-b"); !errors.Is(err, ErrOtherMachine) {
		t.Fatalf("a bound node token on another machine: %v", err)
	}
	must(t, tm.Revoke(c.ID))
	if _, ok := found(); ok {
		t.Fatal("a revoked credential still counts")
	}
	if _, _, err := tm.NewCredential(KindNode, "mba", LocalUser, 0); err != nil {
		t.Fatalf("the machine gets a new token once the old one is revoked: %v", err)
	}
	web, cw, _ := tm.NewCredential(KindWeb, "", LocalUser, -time.Second)
	cs, _ := tm.ActiveCredentials()
	for _, x := range cs {
		if x.ID == cw.ID || x.Sum == Sum(web) {
			t.Fatal("an expired session still counts")
		}
	}
}

func TestTheServerHostIsTheFirstAdmin(t *testing.T) {
	tm := openTeam(t)
	u, ok, err := tm.User(LocalUser)
	if err != nil || !ok || u.Role != RoleAdmin {
		t.Fatalf("%+v %v %v", u, ok, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestANoticeIsDeliveredOncePerUser(t *testing.T) {
	team := openTeam(t)
	if err := team.SetWebhook(LocalUser, "https://hooks.example/x"); err != nil {
		t.Fatal(err)
	}
	if url, _ := team.Webhook(LocalUser); url != "https://hooks.example/x" {
		t.Fatal(url)
	}
	first, err := team.Claim(7, LocalUser, "task.needs_you")
	again, _ := team.Claim(7, LocalUser, "task.needs_you")
	if err != nil || !first || again {
		t.Fatalf("%v %v %v", first, again, err)
	}
	if err := team.Delivered(7, LocalUser, "task.needs_you", "200"); err != nil {
		t.Fatal(err)
	}
}
