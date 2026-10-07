package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
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

// TestTrashListsOtherMachines: tend trash host:<name> lists that machine's trash as its query answers, host:all this
// machine's and every other's, and tend sessions host:<name> status:trash lists the same rows.
func TestTrashListsOtherMachines(t *testing.T) {
	d := machine(t)
	fixtureMBA(t)
	s := d.Get("oauth")
	if err := run([]string{"rm", "-y", "mba:" + s.ID}); err != nil {
		t.Fatal(err)
	}
	listing := func(args ...string) []trashRow {
		t.Helper()
		var err error
		out := stdoutOf(t, func() { err = run(append([]string{"trash"}, args...)) })
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var rows []trashRow
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return rows
	}
	hosts := func(rows []trashRow) []string {
		var out []string
		for _, r := range rows {
			if r.SessionID == s.ID {
				out = append(out, r.Host)
			}
		}
		return out
	}
	// ⚠️ mba answers from this machine's own data: the session is in both trashes
	if got := hosts(listing("--json")); !slices.Equal(got, []string{""}) {
		t.Errorf("this machine's only: %q", got)
	}
	rows := listing("host:mba", "--json")
	if got := hosts(rows); !slices.Equal(got, []string{"mba"}) || time.Since(rows[0].DeletedAt) > time.Minute || rows[0].Files == nil {
		t.Errorf("mba's only: %+v", rows)
	}
	if got := hosts(listing("host:all", "--json")); !slices.Equal(got, []string{"", "mba"}) {
		t.Errorf("both: %q", got)
	}
	var out string
	var err error
	out = stdoutOf(t, func() { err = run([]string{"trash", "host:mba"}) })
	if err != nil || !strings.HasPrefix(out, "mba:"+s.ID+"  ") {
		t.Errorf("the line names it as --restore takes it: %q %v", out, err)
	}
	if got, _ := listedBy(t, "sessions", "host:mba status:trash", "--json"); len(got) != 1 || got[0].SessionID != s.ID || got[0].Host != "mba" {
		t.Errorf("tend sessions lists mba's trash: %+v", got)
	}
	for _, args := range [][]string{{"trash", "oauth"}, {"trash", "host:mba", "--purge"}, {"trash", "host:mba", "--restore", s.ID}} {
		if err := run(args); err == nil || err.Error() != i18n.T("cli.trash.usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestTrashOfAnOldMachineSaysHowToUpdateIt(t *testing.T) {
	machine(t)
	fakeMBA(t, true, true)
	var out string
	errOut := stderrOf(t, func() { out = stdoutOf(t, func() { run([]string{"trash", "host:all"}) }) })
	if !strings.Contains(errOut, i18n.F("remote.trash_old", "mba", "mba")) || !strings.Contains(errOut, i18n.F("remote.trash_unread", "down", remote.Reason(&wire.Error{Code: wire.CodeOffline}))) {
		t.Errorf("stderr: %q", errOut)
	}
	if strings.TrimSpace(out) != i18n.T("cli.trash.empty") {
		t.Errorf("stdout: %q", out)
	}
}
