package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
)

// shown is tend show --json ref: the session and its host.
func shown(t *testing.T, ref string) (r tend.Rec, err error) {
	t.Helper()
	out := stdoutOf(t, func() { err = run([]string{"show", "--json", ref}) })
	if err == nil {
		err = json.Unmarshal([]byte(out), &r)
	}
	return r, err
}

// TestHostRefFindsWhatItsListsHide: a session the lists hide (an sdk-cli run) is found by its full id, and by a prefix
// as this machine finds its own, on another machine over ssh: tend show and resume --dry-run.
func TestHostRefFindsWhatItsListsHide(t *testing.T) {
	d := machine(t)
	sdk := d.Get("sdk")
	listedBy(t, "sessions", "--json") // show reads the index as it was last refreshed
	for _, ref := range []string{sdk.ID, sdk.ID[:8]} {
		if r, err := shown(t, ref); err != nil || r.SessionID != sdk.ID || r.Host != "" {
			t.Fatalf("this machine's rule, %s: %+v %v", ref, r, err)
		}
	}
	fixtureMBA(t)
	if rows, _ := listedBy(t, "sessions", "host:mba", "status:all", "--json"); len(rows) == 0 || strings.Contains(strings.Join(sessionsOf(rows), " "), sdk.ID) {
		t.Fatalf("mba's lists hide it: %v", rows)
	}
	for _, ref := range []string{"mba:" + sdk.ID, "mba:" + sdk.ID[:8]} {
		if r, err := shown(t, ref); err != nil || r.SessionID != sdk.ID || r.Host != "mba" {
			t.Errorf("%s: %+v %v", ref, r, err)
		}
	}
	if _, err := shown(t, "mba:fa"); err == nil || err.Error() != i18n.F("cli.session_ambiguous", "mba:fa") {
		t.Errorf("an ambiguous prefix: %v", err)
	}
	var err error
	out := stdoutOf(t, func() { err = run([]string{"resume", "--dry-run", "mba:" + sdk.ID}) })
	if err != nil || !strings.Contains(out, "resume --terminal --no-herdr "+sdk.ID) {
		t.Fatalf("resume --dry-run: %v\n%s", err, out)
	}
}

func sessionsOf(rows []listed) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.SessionID
	}
	return out
}

// TestServedHostRefFindsWhatItsListsHide: the same through the server's node.call, where mba has no ssh host here.
func TestServedHostRefFindsWhatItsListsHide(t *testing.T) {
	servedCLI(t)
	sdk := fixtureOf(t).Get("sdk").ID
	for _, ref := range []string{"mba:" + sdk, "mba:" + sdk[:8]} {
		if r, err := shown(t, ref); err != nil || r.SessionID != sdk || r.Host != "mba" {
			t.Errorf("%s: %+v %v", ref, r, err)
		}
	}
	var err error
	stdoutOf(t, func() { err = run([]string{"resume", "--dry-run", "mba:" + sdk}) })
	if err == nil || !strings.Contains(err.Error(), "--resume "+sdk) {
		t.Fatalf("resume --dry-run says what to run there: %v", err)
	}
}

// fixtureOf is a dataset like the one machine(t) built: the same session ids.
func fixtureOf(t *testing.T) *fixture.Dataset {
	t.Helper()
	d, err := fixture.Build(filepath.Join(t.TempDir(), "again"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestHostRefOnAnOlderNode: a tend that answers the query without its id is looked up in its list as before, and a
// session that list hides says to update it.
func TestHostRefOnAnOlderNode(t *testing.T) {
	d := machine(t)
	h := remote.NewHostsDial([]tend.Host{{Name: "mba", SSH: "mba"}}, i18n.EN, func(tend.Host) (*remote.Client, error) {
		return remote.Pipe(ignoresID{remote.NewLocal("test")}), nil
	})
	was := remoteHosts
	remoteHosts = func() *remote.Hosts { return h }
	t.Cleanup(func() { remoteHosts = was; h.Close() })
	oauth := d.Get("oauth")
	if r, err := shown(t, "mba:"+oauth.ID[:8]); err != nil || r.SessionID != oauth.ID || r.Host != "mba" {
		t.Fatalf("a listed session: %+v %v", r, err)
	}
	want := remote.TooOld("mba", remote.MQuery)
	if _, err := shown(t, "mba:"+d.Get("sdk").ID); err == nil || err.Error() != want {
		t.Fatalf("a hidden one: %v, want %q", err, want)
	}
}

// ignoresID answers as a tend from before QueryParams.ID: it reads the query without it.
type ignoresID struct{ remote.Handler }

func (o ignoresID) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == remote.MQuery {
		var p remote.QueryParams
		json.Unmarshal(params, &p)
		p.ID = ""
		params, _ = json.Marshal(p)
	}
	return o.Handler.Handle(ctx, method, params)
}

// TestMigrateDryRunFindsWhatItsListsHide: tend migrate host:sid --dry-run takes a session that machine's lists hide.
func TestMigrateDryRunFindsWhatItsListsHide(t *testing.T) {
	self, peer, _ := twoHomes(t)
	defer i18n.Set(i18n.ZH)
	const sid = "5e551011-0c1a-4de0-8000-0000000000c2"
	l := fixture.NewLiveClaude(peer.Claude, sid, filepath.Join(peer.Work, "webapp"), "sdk-cli")
	for _, s := range []string{"one", "two", "three"} {
		if l.User("ask "+s) != nil || l.Reply("answer "+s) != nil {
			t.Fatal("transcript")
		}
	}
	there := filepath.Join(self.Work, "webapp")
	if _, errText, err := migrateOut(t, "peer:"+sid, "--to", "self", "--dir", there, "--dry-run"); err != nil {
		t.Fatalf("dry run: %v\n%s", err, errText)
	}
}
