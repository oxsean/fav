package main

import (
	"os"
	"testing"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
)

// fixtureMBA is host mba answered by this machine's own tend handler: the fixture's sessions as another machine's.
func fixtureMBA(t *testing.T) {
	t.Helper()
	h := remote.NewHostsDial([]tend.Host{{Name: "mba", SSH: "mba"}}, i18n.EN, func(tend.Host) (*remote.Client, error) {
		return remote.Pipe(remote.NewLocal("test")), nil
	})
	was := remoteHosts
	remoteHosts = func() *remote.Hosts { return h }
	t.Cleanup(func() { remoteHosts = was; h.Close() })
}

// TestRmAndRestoreAnotherMachinesSession: tend rm host:id moves the session's files on that machine into its trash,
// tend trash --restore host:id puts them back, both through its node methods.
func TestRmAndRestoreAnotherMachinesSession(t *testing.T) {
	d := machine(t)
	fixtureMBA(t)
	s := d.Get("oauth")
	ref := "mba:" + s.ID[:8]
	var err error
	out := stdoutOf(t, func() { err = run([]string{"rm", "-y", ref}) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Fatalf("the transcript moved on mba: %v", err)
	}
	if out == "" {
		t.Fatal("says what it did")
	}
	if err := run([]string{"rm", "-y", ref}); err == nil {
		t.Fatal("gone from mba's list")
	}

	out = stdoutOf(t, func() { err = run([]string{"trash", "--restore", ref}) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path); err != nil {
		t.Fatalf("the transcript is back: %v", err)
	}
	if err := run([]string{"trash", "--restore", ref}); err == nil || err.Error() != i18n.F("cli.trash.not_found", ref) {
		t.Fatalf("no longer in mba's trash: %v", err)
	}
}

// TestRmOnAnotherMachineThatCannotSaysWhy: a running session, an old tend, a machine out of reach.
func TestRmOnAnotherMachineThatCannotSaysWhy(t *testing.T) {
	machine(t)
	fakeHosts(t, true)
	if err := run([]string{"rm", "-y", "mba:" + farOther}); err == nil || err.Error() != i18n.F("remote.trash_busy", "mba") {
		t.Errorf("running: %v", err)
	}
	if err := run([]string{"rm", "-y", "down:" + farOther}); err == nil {
		t.Error("an unreachable machine's session is not found")
	}
	fakeMBA(t, true, true)
	want := i18n.F("remote.trash_old", "mba", "mba")
	if err := run([]string{"rm", "-y", "mba:" + farFavorite}); err == nil || err.Error() != want {
		t.Errorf("old tend: %v, want %q", err, want)
	}
	if err := run([]string{"trash", "--restore", "mba:" + farFavorite}); err == nil || err.Error() != i18n.F("remote.trash_old", "mba", "mba") {
		t.Errorf("old tend: %v", err)
	}
}
