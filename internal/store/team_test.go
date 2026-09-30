package store

import (
	"errors"
	"path/filepath"
	"strings"
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
	waiting, _ := tm.NewInvite(RoleAdmin, LocalUser, time.Hour)
	list, err := tm.Invites()
	if err != nil || len(list) != 1 || list[0].Role != RoleAdmin || strings.Contains(waiting, list[0].ID) || list[0].ID != Sum(waiting)[:inviteIDLen] {
		t.Fatalf("only the waiting one is listed, by its hash: %+v %v", list, err)
	}
	must(t, tm.RevokeInvite(list[0].ID))
	if _, err := tm.Admit(gitea("7", "", false), waiting); !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("a revoked invite: %v", err)
	}
	if err := tm.RevokeInvite(list[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked twice: %v", err)
	}
}

func TestAProjectInviteSaysWhichProjectItOpens(t *testing.T) {
	tm := openTeam(t)
	if _, err := tm.NewProjectInvite(RoleMember, LocalUser, "p1", "owner", time.Hour); err == nil {
		t.Fatal("a project invite grants participant or reader")
	}
	secret, err := tm.NewProjectInvite(RoleMember, LocalUser, "p1", "reader", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := tm.Invites(); len(list) != 1 || list[0].Project != "p1" || list[0].Access != "reader" {
		t.Fatalf("%+v", list)
	}
	eve, err := tm.Admit(gitea("8", "", false), secret)
	if err != nil {
		t.Fatal(err)
	}
	info, err := tm.Invite(secret)
	if err != nil || info.Project != "p1" || info.Access != "reader" || info.UsedBy != eve.ID {
		t.Fatalf("%+v %v", info, err)
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

// A secret the server keeps is written once: whoever comes second gets the first one back, now and after reopening.
func TestASecretIsKeptOnceAndTheFirstStays(t *testing.T) {
	path := filepath.Join(t.TempDir(), File)
	tm, err := OpenTeam(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := tm.KeepSecret("push", []byte("first")); err != nil || string(got) != "first" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := tm.KeepSecret("push", []byte("second")); err != nil || string(got) != "first" {
		t.Fatalf("a second secret replaced the first: %q %v", got, err)
	}
	if got, _ := tm.KeepSecret("other", []byte("x")); string(got) != "x" {
		t.Fatalf("another name: %q", got)
	}
	tm.Close()
	if tm, err = OpenTeam(path); err != nil {
		t.Fatal(err)
	}
	defer tm.Close()
	if got, err := tm.KeepSecret("push", []byte("third")); err != nil || string(got) != "first" {
		t.Fatalf("after reopening: %q %v", got, err)
	}
}

// A database from before the secrets table gains it, keeps its people, and is copied aside first.
func TestUpgradingToTheSecretsTableKeepsTheTeam(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, File)
	tm, err := OpenTeam(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Admit(gitea("1", "ann@corp.example", true), ""); !errors.Is(err, ErrNotAdmitted) {
		t.Fatal(err)
	}
	tm.AddAdmit(Admit{Kind: AdmitEmail, Value: "ann@corp.example", Role: RoleMember})
	ann, err := tm.Admit(gitea("1", "ann@corp.example", true), "")
	if err != nil {
		t.Fatal(err)
	}
	tm.Close()
	before0009(t, path)
	exec(t, path, "DROP TABLE secrets")
	setVersion(t, path, 7)
	if tm, err = OpenTeam(path); err != nil {
		t.Fatal(err)
	}
	defer tm.Close()
	if u, ok, err := tm.User(ann.ID); err != nil || !ok || u.Email != "ann@corp.example" {
		t.Fatalf("ann after the upgrade: %+v %v %v", u, ok, err)
	}
	if got, err := tm.KeepSecret("push", []byte("k")); err != nil || string(got) != "k" {
		t.Fatalf("%q %v", got, err)
	}
	if baks, _ := filepath.Glob(filepath.Join(dir, File+".v7-*.bak")); len(baks) != 1 {
		t.Fatalf("no copy of the v7 database: %v", baks)
	}
}
