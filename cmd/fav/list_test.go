package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/index"
)

func machine(t *testing.T) *fixture.Dataset {
	t.Helper()
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAV_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	t.Setenv("FZF_PROMPT", "")
	return d
}

func sessionIDs(t *testing.T, args ...string) map[string]bool {
	t.Helper()
	var err error
	out := stdoutOf(t, func() { err = run(args) })
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	var rows []struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.SessionID] = true
	}
	return ids
}

func TestListsAnswerEveryStatus(t *testing.T) {
	d := machine(t)
	s, err := fav.Open()
	if err != nil {
		t.Fatal(err)
	}
	fave := d.Get("oauth")
	r := s.BySession(fave.Provider, fave.ID)
	if _, err := index.Trash(s, r, index.SessionFilesOf(nil, r)); err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(t, "sessions", "status:trash", "--json"); len(got) != 1 || !got[fave.ID] {
		t.Errorf("fav sessions status:trash lists the trash: %v", got)
	}

	agents := sessionIDs(t, "list", "status:agent", "--json")
	for _, x := range d.Sessions {
		if agents[x.ID] != x.Agent {
			t.Errorf("fav list status:agent: %s listed %v, want %v", x.Name, agents[x.ID], x.Agent)
		}
	}

	var live fixture.Session
	for _, x := range d.Sessions {
		if x.Favorite && x.Provider == fav.ProviderClaude && x.ID != fave.ID {
			live = x
			break
		}
	}
	state := filepath.Join(d.Claude, "sessions", strconv.Itoa(os.Getpid())+".json")
	os.MkdirAll(filepath.Dir(state), 0o755)
	os.WriteFile(state, []byte(`{"pid":`+strconv.Itoa(os.Getpid())+`,"sessionId":"`+live.ID+`"}`), 0o644)
	rec := s.BySession(live.Provider, live.ID)
	if out := stdoutOf(t, func() { run([]string{"fzf-list", "status:live"}) }); !strings.HasPrefix(out, rec.ID+"\t") || strings.Count(out, "\n") != 1 {
		t.Errorf("the fzf favorites tab answers status:live with the running favorite %s:\n%s", rec.ID, out)
	}
}

func TestFzfKeptRowStaysInPlace(t *testing.T) {
	machine(t)
	keys := func(args ...string) []string {
		t.Helper()
		var err error
		out := stdoutOf(t, func() { err = run(append([]string{"fzf-list"}, args...)) })
		if err != nil {
			t.Fatal(err)
		}
		var ks []string
		for l := range strings.Lines(out) {
			ks = append(ks, strings.Fields(l)[0])
		}
		return ks
	}
	full := keys("status:all")
	if len(full) < 3 {
		t.Fatalf("fixture has too few favorites: %v", full)
	}
	var filter, kept string
	shown := map[string]bool{}
	for _, f := range []string{"status:todo", "status:doing", "status:done"} {
		clear(shown)
		for _, k := range keys(f) {
			shown[k] = true
		}
		for i, k := range full {
			if !shown[k] && kept == "" && slices.ContainsFunc(full[i+1:], func(x string) bool { return shown[x] }) {
				filter, kept = f, k
			}
		}
		if kept != "" {
			break
		}
	}
	if kept == "" {
		t.Fatalf("fixture needs a favorite listed before one of another status: %v", full)
	}
	var want []string
	for _, k := range full {
		if shown[k] || k == kept {
			want = append(want, k)
		}
	}
	if got := keys("--keep", kept, filter); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPickFindsEverySessionTheIndexHas(t *testing.T) {
	d := machine(t)
	s, err := fav.Open()
	if err != nil {
		t.Fatal(err)
	}
	idx, _ := index.Open()
	if idx, _ = idx.Refresh(); idx.Save() != nil {
		t.Fatal("index not written")
	}
	for _, name := range []string{"chain-old", "short", "sdk"} {
		id := d.Get(name).ID
		if r, err := pick(s, id[:10]); err != nil || r.SessionID != id {
			t.Errorf("%s: %v %+v", name, err, r)
		}
	}
}
