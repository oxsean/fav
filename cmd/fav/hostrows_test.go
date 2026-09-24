package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
)

type fakeHost struct {
	sessions []remote.Session
	live     map[string]capture.Live
}

func (f fakeHost) Handle(_ context.Context, method string, _ json.RawMessage) (any, error) {
	switch method {
	case remote.MHello:
		return remote.Hello{Proto: remote.Proto}, nil
	case remote.MList:
		return remote.List{Sessions: f.sessions}, nil
	case remote.MLive:
		return remote.Live{Live: f.live}, nil
	}
	return nil, &remote.Error{Code: remote.CodeUnknownMethod}
}

const (
	farFav   = "aaaa1111-0000-4000-8000-000000000001"
	farOther = "aaaa2222-0000-4000-8000-000000000002"
)

// fakeHosts: mba answers with two sessions (one favorited, one running), down never answers; dials counts reaching either.
func fakeHosts(t *testing.T, mbaUp bool) *atomic.Int32 {
	t.Helper()
	now := time.Now()
	mba := fakeHost{
		sessions: []remote.Session{
			{ID: "f00dfeed", Provider: fav.ProviderClaude, SessionID: farFav, Title: "far favorite", Status: fav.StatusDoing,
				FavoritedAt: &now, UpdatedAt: now, LastAt: now, Turns: 12, Cwd: "/home/me/far"},
			{Provider: fav.ProviderCodex, SessionID: farOther, Title: "far session", UpdatedAt: now, LastAt: now, Turns: 9},
		},
		live: map[string]capture.Live{farOther: {Status: "working"}},
	}
	dials := &atomic.Int32{}
	h := remote.NewHostsDial([]fav.Host{{Name: "mba", SSH: "mba"}, {Name: "down", SSH: "down"}}, i18n.EN,
		func(host fav.Host) (*remote.Client, error) {
			dials.Add(1)
			if host.Name == "mba" && mbaUp {
				return remote.Pipe(mba), nil
			}
			return nil, &remote.Error{Code: remote.CodeOffline}
		})
	old := remoteHosts
	remoteHosts = func() *remote.Hosts { return h }
	t.Cleanup(func() { remoteHosts = old; h.Close() })
	return dials
}

type listed struct {
	SessionID string `json:"session_id"`
	Host      string `json:"host"`
}

func listedBy(t *testing.T, args ...string) (rows []listed, stderr string) {
	t.Helper()
	var out string
	var err error
	stderr = stderrOf(t, func() { out = stdoutOf(t, func() { err = run(args) }) })
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return rows, stderr
}

func hostsIn(rows []listed) map[string]int {
	n := map[string]int{}
	for _, r := range rows {
		n[r.Host]++
	}
	return n
}

func TestListsMergeHostRows(t *testing.T) {
	machine(t)
	dials := fakeHosts(t, true)

	local, _ := listedBy(t, "sessions", "--json")
	if dials.Load() != 0 || hostsIn(local)[""] != len(local) {
		t.Fatalf("without host: no host is reached and every row is local: %d dials, %v", dials.Load(), hostsIn(local))
	}

	rows, _ := listedBy(t, "sessions", "host:mba", "--json")
	if len(rows) != 2 || hostsIn(rows)["mba"] != 2 {
		t.Errorf("host:mba lists mba's sessions only: %+v", rows)
	}
	if rows, _ := listedBy(t, "sessions", "host:MBA", "far", "favorite", "--json"); len(rows) != 1 || rows[0].SessionID != farFav {
		t.Errorf("the query filters host rows too: %+v", rows)
	}
	if rows, _ := listedBy(t, "list", "host:mba", "--json"); len(rows) != 1 || rows[0].SessionID != farFav {
		t.Errorf("fav list shows the host's favorites only: %+v", rows)
	}
	if rows, _ := listedBy(t, "sessions", "host:mba", "status:live", "--json"); len(rows) != 1 || rows[0].SessionID != farOther {
		t.Errorf("status:live asks the host who runs: %+v", rows)
	}

	if out := stdoutOf(t, func() { run([]string{"fzf-list", "host:mba"}) }); !strings.HasPrefix(out, "mba:"+farFav+"\t") || strings.Count(out, "\n") != 1 {
		t.Errorf("the fzf favorites tab keys a host row host:sid:\n%s", out)
	}
	t.Setenv("FZF_PROMPT", "Agents > ")
	if out := stdoutOf(t, func() { run([]string{"fzf-list", "host:mba"}) }); !strings.HasPrefix(out, "mba:"+farOther+"\t") || strings.Count(out, "\n") != 1 {
		t.Errorf("the fzf Agents tab lists what runs on the host:\n%s", out)
	}
	t.Setenv("FZF_PROMPT", "")

	all, stderr := listedBy(t, "sessions", "host:all", "--json")
	if got := hostsIn(all); got["mba"] != 2 || got[""] != len(local) || got["down"] != 0 {
		t.Errorf("host:all is this machine plus every host that answers: %v", got)
	}
	if lines := strings.Split(strings.TrimSpace(stderr), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "down") {
		t.Errorf("one stderr line names the host that did not answer: %q", stderr)
	}
}

func TestUnreachableHostShowsItsCache(t *testing.T) {
	machine(t)
	fakeHosts(t, true)
	listedBy(t, "sessions", "host:mba", "--json")

	fakeHosts(t, false)
	rows, stderr := listedBy(t, "sessions", "host:mba", "--json")
	if len(rows) != 2 || hostsIn(rows)["mba"] != 2 {
		t.Errorf("an unreachable host contributes its cached rows: %+v", rows)
	}
	if !strings.Contains(stderr, "mba") || strings.Count(stderr, "\n") != 1 {
		t.Errorf("and one line saying so: %q", stderr)
	}
}

func TestPickHostRef(t *testing.T) {
	machine(t)
	fakeHosts(t, true)
	s, err := fav.Open()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ref, want string
	}{
		{"mba:" + farFav, farFav},
		{"mba:aaaa2", farOther},
		{"MBA:f00dfeed", farFav},
	} {
		r, err := pick(s, c.ref)
		if err != nil || r.SessionID != c.want || r.Host != "mba" {
			t.Errorf("%s: %+v %v", c.ref, r, err)
		}
	}
	for _, ref := range []string{"mba:aaaa", "mba:ffff", "mba:", "nohost:" + farFav, "down:" + farFav} {
		if r, err := pick(s, ref); err == nil {
			t.Errorf("%s must fail (ambiguous, unknown, empty, not a host, unreachable): %+v", ref, r)
		}
	}
}

func TestHostRecordsAreReadOnly(t *testing.T) {
	machine(t)
	fakeHosts(t, true)
	want := i18n.F("cli.remote.read_only", "mba")
	for _, args := range [][]string{
		{"archive", "mba:" + farFav},
		{"status", "mba:" + farFav, "done"},
		{"fav", "mba:" + farOther},
		{"pin", "mba:" + farFav},
		{"rm", "-y", "mba:" + farFav},
		{"edit", "mba:" + farFav},
		{"handoff", "mba:" + farFav},
		{"resume", "--fork", "mba:" + farFav},
		{"fzf-pick", "togglefav", "mba:" + farFav},
	} {
		if err := run(args); err == nil || err.Error() != want {
			t.Errorf("%v: %v, want %q", args, err, want)
		}
	}
}

func TestResumeHostDryRun(t *testing.T) {
	machine(t)
	fakeHosts(t, true)
	var err error
	out := stdoutOf(t, func() { err = run([]string{"resume", "--dry-run", "mba:" + farFav}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"far favorite", "ssh", "mba", "resume --terminal --no-herdr " + farFav} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run misses %q:\n%s", want, out)
		}
	}
}

func TestFzfReusesARecentHostList(t *testing.T) {
	machine(t)
	fakeHosts(t, true)
	t.Setenv("FZF_PROMPT", "Agents > ")
	stdoutOf(t, func() { run([]string{"fzf-list", "host:mba"}) })
	dials := fakeHosts(t, false)
	out := stdoutOf(t, func() { run([]string{"fzf-list", "host:mba"}) })
	if dials.Load() != 0 || !strings.HasPrefix(out, "mba:"+farOther+"\t") {
		t.Fatalf("a keystroke in fzf reads the list and who runs from the cache: %d dials\n%s", dials.Load(), out)
	}
	if _, stderr := listedBy(t, "sessions", "host:mbb", "--json"); !strings.Contains(stderr, "mbb") || !strings.Contains(stderr, "mba") {
		t.Errorf("an unknown host names the configured ones: %q", stderr)
	}
}

func TestFzfDoesNotRedialADownHostPerKeystroke(t *testing.T) {
	machine(t)
	fakeHosts(t, true)
	stdoutOf(t, func() { run([]string{"fzf-list", "host:mba"}) })
	ageCache(t, "mba")
	dials := fakeHosts(t, false)
	stderrOf(t, func() { stdoutOf(t, func() { run([]string{"fzf-list", "host:mba"}) }) }) // an old list: reach it, and fail
	n := dials.Load()
	if n == 0 {
		t.Fatal("an old list is fetched again")
	}
	var out string
	stderrOf(t, func() { out = stdoutOf(t, func() { run([]string{"fzf-list", "host:mba"}) }) })
	if dials.Load() != n || !strings.HasPrefix(out, "mba:") {
		t.Fatalf("a host that just failed shows its cache without a new dial: %d → %d\n%s", n, dials.Load(), out)
	}
}

// ageCache makes host's cached list a minute old.
func ageCache(t *testing.T, host string) {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(fav.Home(), "hosts", host+"-*", "sessions.json"))
	if len(paths) != 1 {
		t.Fatalf("cache of %s: %v", host, paths)
	}
	var c map[string]any
	b, _ := os.ReadFile(paths[0])
	json.Unmarshal(b, &c)
	c["at"] = time.Now().Add(-time.Minute)
	b, _ = json.Marshal(c)
	os.WriteFile(paths[0], b, 0o600)
}
