package fixture

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/index"
)

func load(t *testing.T) (*Dataset, *index.Index, *fav.Store) {
	t.Helper()
	d, err := Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAV_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	idx, err := index.OpenAt(filepath.Join(d.Home, "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Refresh()
	store, err := fav.OpenAt(filepath.Join(d.Home, "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return d, idx, store
}

func byKey(ss []*index.Session) map[string]*index.Session {
	m := map[string]*index.Session{}
	for _, s := range ss {
		m[s.Provider+":"+s.SessionID] = s
	}
	return m
}

func key(s Session) string { return s.Provider + ":" + s.ID }

func TestIndexSeesEveryScenario(t *testing.T) {
	d, idx, _ := load(t)
	listed, agents := byKey(idx.Sessions()), byKey(idx.AgentSessions())
	for _, s := range d.Sessions {
		_, inList := listed[key(s)]
		_, inAgents := agents[key(s)]
		switch {
		case s.Name == "chain-old":
			if inList {
				t.Errorf("%s: an older link of a continuation chain is listed on its own", s.Name)
			}
		case s.Agent:
			if !inAgents {
				t.Errorf("%s: missing from the agent runs", s.Name)
			}
		case !inList:
			t.Errorf("%s: missing from the sessions", s.Name)
		}
	}

	get := func(name string) *index.Session {
		s := listed[key(d.Get(name))]
		if s == nil {
			t.Fatalf("%s not indexed", name)
		}
		return s
	}
	oauth := get("oauth")
	if oauth.Title != "登录页 OAuth 回调排障" || oauth.Turns != 4 || oauth.Branch != "feat/oauth-callback" || !strings.Contains(oauth.Recap, "OAuth") {
		t.Errorf("oauth: %+v", oauth)
	}
	for _, f := range []string{filepath.Join(d.Work, "webapp", "src", "auth", "callback.ts"), filepath.Join(d.Work, "webapp", "docs", "oauth.md")} {
		if oauth.Files[f] == 0 {
			t.Errorf("oauth: %s not among the files written: %v", f, oauth.Files)
		}
	}
	if s := get("worktree"); s.Repo != filepath.Join(d.Work, "webapp") {
		t.Errorf("worktree: repo %q", s.Repo)
	}
	if s := get("chain-new"); !slices.Contains(s.Aliases, d.Get("chain-old").ID) || s.Turns != 3 {
		t.Errorf("chain: %+v", s)
	}
	cli := get("codex-cli")
	if cli.Title != "修 webapp CI" || !strings.Contains(cli.Recap, "lint") || cli.Files[filepath.Join(d.Work, "webapp", "src", "app.ts")] == 0 {
		t.Errorf("codex-cli: %+v", cli)
	}
	if !get("codex-desktop").App {
		t.Error("codex-desktop: not marked as started in the app")
	}
	if s := get("codex-resumed"); s.Turns != 3 {
		t.Errorf("codex-resumed: the two rollouts should add up to 3 turns, got %d", s.Turns)
	}
	if !get("codex-archived").CodexArchived() {
		t.Error("codex-archived: not recognised as archived")
	}
}

func TestFavoritesAttach(t *testing.T) {
	d, idx, store := load(t)
	recs := idx.Attach(store, nil)
	favs := 0
	for _, r := range store.All() {
		if r.Favorite() {
			favs++
		}
		if r.Turns == 0 {
			t.Errorf("%s: favorite got no turns attached", r.Title)
		}
	}
	want := 0
	for _, s := range d.Sessions {
		if s.Favorite {
			want++
		}
	}
	if favs != want {
		t.Errorf("favorites: %d, want %d", favs, want)
	}
	for _, r := range recs {
		if r.SessionID == d.Get("oauth").ID {
			t.Errorf("a favorited session also came back as an unfavorited row")
		}
	}
}

func TestTranscriptPaging(t *testing.T) {
	d, _, _ := load(t)
	path := d.Get("pagination").Path
	if st, err := os.Stat(path); err != nil || st.Size() < 2<<20 {
		t.Fatalf("pagination transcript should exceed 2 MB: %v", err)
	}
	var msgs, pages int
	for before := int64(-1); ; pages++ {
		p := capture.Messages(path, before, 20)
		msgs += len(p.Msgs)
		if p.Done {
			break
		}
		if pages > 50 || p.From <= 0 || (before >= 0 && p.From >= before) {
			t.Fatalf("paging does not move backwards: page %d from %d", pages, p.From)
		}
		before = p.From
	}
	if pages < 2 || msgs < 60 {
		t.Errorf("read %d messages in %d pages", msgs, pages)
	}
}

func TestMessageSearch(t *testing.T) {
	d, idx, store := load(t)
	recs := append(idx.Attach(store, nil), store.All()...)
	dir := filepath.Join(d.Home, "text")
	if _, err := fulltext.Update(context.Background(), dir, idx.Paths(), nil); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]string{"state 解码": "oauth", "cursor drift": "pagination", "trace id": "codex-desktop"} {
		res := fulltext.Search(context.Background(), dir, fulltext.Cands(recs, idx.PathsBySession()), q)
		if len(res) == 0 || recs[res[0].Cand].SessionID != d.Get(want).ID {
			t.Errorf("%q: want %s first, got %d results", q, want, len(res))
		}
	}
}

func TestMovedProjectIsFound(t *testing.T) {
	d, idx, store := load(t)
	var hit *index.Missing
	for _, m := range idx.FindMissing(store, "") {
		if m.Dir == filepath.Join(d.Work, "legacy-app") {
			hit = &m
		}
	}
	if hit == nil || hit.Sessions != 3 {
		t.Fatalf("legacy-app not reported missing with its 3 sessions: %+v", hit)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed: the remote check cannot confirm the new location")
	}
	if want := filepath.Join(d.Root, "dev", "legacy-app"); !slices.Equal(hit.Found, []string{want}) {
		t.Errorf("found %v, want %s", hit.Found, want)
	}
}

func TestStaleLiveFileIsIgnored(t *testing.T) {
	d, _, _ := load(t)
	if _, ok := capture.LiveSessions()[d.Get("oauth").ID]; ok {
		t.Error("a sessions/<pid>.json whose process is gone counts as running")
	}
}

func TestMoveProjectRewritesNativePaths(t *testing.T) {
	d, idx, store := load(t)
	old, moved := filepath.Join(d.Work, "notes-api"), filepath.Join(d.Root, "dev", "notes-api")
	if err := os.Rename(old, moved); err != nil {
		t.Fatal(err)
	}
	plan, err := idx.PlanMove(store, nil, old, moved)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(store); err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Refresh()
	listed := byKey(idx.Sessions())
	for _, name := range []string{"pagination", "chain-new", "codex-desktop", "codex-resumed"} {
		if s := listed[key(d.Get(name))]; s == nil || s.Cwd != moved {
			t.Errorf("%s: cwd not moved: %+v", name, s)
		}
	}
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	pg := d.Get("pagination")
	if r := store.BySession(pg.Provider, pg.ID); r == nil || r.Cwd != moved {
		t.Errorf("the favorite keeps the old cwd: %+v", r)
	}
	q, _ := json.Marshal(moved)
	b, err := os.ReadFile(filepath.Join(d.Claude, "projects", index.ClaudeProjectName(moved), pg.ID+".jsonl"))
	if err != nil || !strings.Contains(string(b), `"cwd":`+string(q)) {
		t.Errorf("the transcript under the new project dir carries the escaped new cwd: %v", err)
	}
}
