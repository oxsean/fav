package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

type fakeHost struct {
	sessions []remote.Session // put rewrites them in place
	live     map[string]capture.Live
	old      bool // a tend from before put
}

func (f fakeHost) Handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case remote.MHello:
		h := remote.Hello{Proto: wire.Proto, Version: "v0.0.9"}
		if !f.old {
			h.Methods = []string{remote.MHello, remote.MList, remote.MLive, remote.MPut}
		}
		return h, nil
	case remote.MPut:
		if f.old {
			break
		}
		var p remote.PutParams
		json.Unmarshal(params, &p)
		i := slices.IndexFunc(f.sessions, func(s remote.Session) bool { return s.Provider == p.Provider && s.SessionID == p.SessionID })
		if i < 0 {
			return nil, &wire.Error{Code: wire.CodeNotFound}
		}
		if p.Expect != nil && !f.sessions[i].UpdatedAt.Equal(*p.Expect) {
			return nil, &wire.Error{Code: wire.CodeStale}
		}
		r := f.sessions[i].Rec("")
		p.Patch.Apply(r, time.Now())
		r.UpdatedAt = time.Now()
		f.sessions[i] = remote.SessionOf(r)
		return remote.Row{Session: f.sessions[i]}, nil
	case remote.MList:
		return remote.List{Sessions: f.sessions}, nil
	case remote.MLive:
		return remote.Live{Live: f.live}, nil
	}
	return nil, &wire.Error{Code: wire.CodeUnknownMethod}
}

const (
	farFavorite = "aaaa1111-0000-4000-8000-000000000001"
	farOther    = "aaaa2222-0000-4000-8000-000000000002"
)

// fakeHosts: mba answers with two sessions (one favorited, one running), down never answers; dials counts reaching either.
func fakeHosts(t *testing.T, mbaUp bool) *atomic.Int32 {
	t.Helper()
	dials, _ := fakeMBA(t, mbaUp, false)
	return dials
}

// fakeMBA is fakeHosts with mba's sessions, which put rewrites; old: its tend has no put.
func fakeMBA(t *testing.T, mbaUp, old bool) (*atomic.Int32, []remote.Session) {
	t.Helper()
	now := time.Now()
	mba := fakeHost{
		sessions: []remote.Session{
			{ID: "f00dfeed", Provider: tend.ProviderClaude, SessionID: farFavorite, Title: "far favorite", Status: tend.StatusDoing,
				FavoritedAt: &now, UpdatedAt: now, LastAt: now, Turns: 12, Cwd: "/home/me/far"},
			{Provider: tend.ProviderCodex, SessionID: farOther, Title: "far session", UpdatedAt: now, LastAt: now, Turns: 9},
		},
		live: map[string]capture.Live{farOther: {Status: "working"}},
		old:  old,
	}
	dials := &atomic.Int32{}
	h := remote.NewHostsDial([]tend.Host{{Name: "mba", SSH: "mba"}, {Name: "down", SSH: "down"}}, i18n.EN,
		func(host tend.Host) (*remote.Client, error) {
			dials.Add(1)
			if host.Name == "mba" && mbaUp {
				return remote.Pipe(mba), nil
			}
			return nil, &wire.Error{Code: wire.CodeOffline}
		})
	was := remoteHosts
	remoteHosts = func() *remote.Hosts { return h }
	t.Cleanup(func() { remoteHosts = was; h.Close() })
	return dials, mba.sessions
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
	if rows, _ := listedBy(t, "sessions", "host:MBA", "far", "favorite", "--json"); len(rows) != 1 || rows[0].SessionID != farFavorite {
		t.Errorf("the query filters host rows too: %+v", rows)
	}
	if rows, _ := listedBy(t, "list", "host:mba", "--json"); len(rows) != 1 || rows[0].SessionID != farFavorite {
		t.Errorf("tend list shows the host's favorites only: %+v", rows)
	}
	if rows, _ := listedBy(t, "sessions", "host:mba", "status:live", "--json"); len(rows) != 1 || rows[0].SessionID != farOther {
		t.Errorf("status:live asks the host who runs: %+v", rows)
	}

	if out := stdoutOf(t, func() { run([]string{"fzf-list", "host:mba"}) }); !strings.HasPrefix(out, "mba:"+farFavorite+"\t") || strings.Count(out, "\n") != 1 {
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
	s, err := tend.Open()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ref, want string
	}{
		{"mba:" + farFavorite, farFavorite},
		{"mba:aaaa2", farOther},
		{"MBA:f00dfeed", farFavorite},
	} {
		r, err := pick(s, c.ref)
		if err != nil || r.SessionID != c.want || r.Host != "mba" {
			t.Errorf("%s: %+v %v", c.ref, r, err)
		}
	}
	for _, ref := range []string{"mba:aaaa", "mba:ffff", "mba:", "nohost:" + farFavorite, "down:" + farFavorite} {
		if r, err := pick(s, ref); err == nil {
			t.Errorf("%s must fail (ambiguous, unknown, empty, not a host, unreachable): %+v", ref, r)
		}
	}
}

// TestHostRecordsWriteThroughPut: favorite, archive, status, done, edit and fzf's toggles take host:sid and write on
// that machine; an old tend there says how to update it.
func TestHostRecordsWriteThroughPut(t *testing.T) {
	d := machine(t)
	_, mba := fakeMBA(t, true, false)
	at := func(sid string) remote.Session {
		return mba[slices.IndexFunc(mba, func(s remote.Session) bool { return s.SessionID == sid })]
	}
	editor := filepath.Join(t.TempDir(), "editor.sh")
	os.WriteFile(editor, []byte("#!/bin/sh\nsed -i.bak -e 's/\"title\": \"far favorite\"/\"title\": \"edited far away\"/' -e 's/\"summary\": \"\"/\"summary\": \"s\"/' \"$1\"\n"), 0o755)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", editor)
	for _, step := range []struct {
		args []string
		ok   func() bool
	}{
		{[]string{"favorite", "mba:" + farOther}, func() bool { return at(farOther).FavoritedAt != nil }},
		{[]string{"archive", "mba:" + farOther}, func() bool { return at(farOther).ArchivedAt != nil }},
		{[]string{"unarchive", "mba:" + farOther}, func() bool { return at(farOther).ArchivedAt == nil }},
		{[]string{"status", "mba:" + farFavorite, "todo"}, func() bool { return at(farFavorite).Status == tend.StatusTodo }},
		{[]string{"done", "mba:" + farFavorite}, func() bool { return at(farFavorite).Status == tend.StatusDone }},
		{[]string{"fzf-pick", "togglefav", "mba:" + farFavorite}, func() bool { return at(farFavorite).FavoritedAt == nil }},
		{[]string{"fzf-pick", "toggledone", "mba:" + farFavorite}, func() bool { return at(farFavorite).Status == tend.StatusDoing }},
		{[]string{"edit", "mba:" + farFavorite}, func() bool { return at(farFavorite).Title == "edited far away" }},
	} {
		if step.args[0] == "edit" && runtime.GOOS == "windows" { // the editor is a shell script
			continue
		}
		var err error
		stdoutOf(t, func() { err = run(step.args) })
		if err != nil || !step.ok() {
			t.Fatalf("%v: %v", step.args, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(d.Home, "records.jsonl")); strings.Contains(string(b), farFavorite) || strings.Contains(string(b), farOther) {
		t.Fatal("nothing of mba's is written here")
	}
	cached, _ := remoteHosts().Cached("mba")
	i := slices.IndexFunc(cached, func(r *tend.Rec) bool { return r.SessionID == farFavorite })
	if i < 0 || cached[i].Status != tend.StatusDoing || cached[i].FavoritedAt != nil {
		t.Fatal("the kept list takes the answered rows")
	}
	if runtime.GOOS != "windows" && cached[i].Title != "edited far away" {
		t.Fatal("the kept list takes the edited title")
	}
}

func TestHostRecordsOnAnOldTendSayHowToUpdateIt(t *testing.T) {
	machine(t)
	fakeMBA(t, true, true)
	want := i18n.F("remote.put_old", "mba", "mba")
	for _, args := range [][]string{{"favorite", "mba:" + farOther}, {"fzf-pick", "togglefav", "mba:" + farFavorite}} {
		if err := run(args); err == nil || err.Error() != want {
			t.Errorf("%v: %v, want %q", args, err, want)
		}
	}
}

func TestHostRecordsAreReadOnly(t *testing.T) {
	machine(t)
	fakeHosts(t, true)
	want := i18n.F("cli.remote.read_only", "mba")
	for _, args := range [][]string{
		{"pin", "mba:" + farFavorite},
		{"rm", "-y", "mba:" + farFavorite},
		{"handoff", "mba:" + farFavorite},
		{"resume", "--fork", "mba:" + farFavorite},
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
	out := stdoutOf(t, func() { err = run([]string{"resume", "--dry-run", "mba:" + farFavorite}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"far favorite", "ssh", "mba", "resume --terminal --no-herdr " + farFavorite} {
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
	paths, _ := filepath.Glob(filepath.Join(tend.Home(), "hosts", host+"-*", "sessions.json"))
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
