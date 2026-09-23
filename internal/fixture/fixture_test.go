package fixture

import (
	"context"
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
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

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
		m[s.Key()] = s
	}
	return m
}

func key(s Session) string { return fav.SessionKey(s.Provider, s.ID) }

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
		case s.Agent != inAgents:
			t.Errorf("%s: agent run %v, want %v", s.Name, inAgents, s.Agent)
		case s.Agent:
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
	for _, f := range []string{filepath.Join(d.Work, "webapp", "src", "auth", "callback.ts"), filepath.Join(d.Work, "webapp", "docs", "oauth.md"),
		filepath.Join(d.Work, "webapp", "docs", "state.ipynb")} {
		if oauth.Files[f] != 1 {
			t.Errorf("oauth: %s not written once: %v", f, oauth.Files)
		}
	}
	if len(oauth.Files) != 3 {
		t.Errorf("oauth: a file in the agent scratch dir is not a project file: %v", oauth.Files)
	}
	if s := get("worktree"); s.Repo != filepath.Join(d.Work, "webapp") {
		t.Errorf("worktree: repo %q", s.Repo)
	}
	if s := get("chain-new"); !slices.Contains(s.Aliases, d.Get("chain-old").ID) || s.Turns != 3 {
		t.Errorf("chain: %+v", s)
	}
	cli := get("codex-cli")
	if cli.Title != "修 webapp CI" || !strings.Contains(cli.Recap, "lint") || len(cli.Files) != 2 ||
		cli.Files[filepath.Join(d.Work, "webapp", "src", "app.ts")] != 1 || cli.Files[filepath.Join(d.Work, "webapp", "scripts", "lint.sh")] != 1 {
		t.Errorf("codex-cli: apply_patch paths, relative ones under the cwd: %+v", cli)
	}
	if got, want := agents[key(d.Get("sdk"))].DisplayTitle(), "webapp: Summarize the diff in one line"; got != want {
		t.Errorf("sdk: an untitled agent run is named after its directory and first prompt: %q", got)
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

func TestRowsListEveryKind(t *testing.T) {
	d, idx, store := load(t)
	unfav := idx.Attach(store, nil)
	var rs index.Rows
	list := func(expr string, all bool, live map[string]capture.Live) map[string]*fav.Rec {
		t.Helper()
		q := fav.Parse(expr)
		q.All = all
		recs, err := rs.List(store, idx, unfav, live, q)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]*fav.Rec{}
		for _, r := range recs {
			out[r.Key()] = r
		}
		return out
	}
	shown, agents := list("status:all", true, nil), list("status:agent", false, nil)
	for _, s := range d.Sessions {
		if _, ok := shown[key(s)]; ok != s.Listed {
			t.Errorf("%s: listed %v, want %v", s.Name, ok, s.Listed)
		}
		if _, ok := agents[key(s)]; ok != s.Agent {
			t.Errorf("%s: among the agent runs %v, want %v", s.Name, ok, s.Agent)
		}
	}

	oauth := d.Get("oauth")
	fresh := fav.SessionKey(fav.ProviderClaude, "0d0d0d0d-new")
	live := map[string]capture.Live{"0d0d0d0d-new": {Agent: fav.ProviderClaude, Cwd: filepath.Join(d.Work, "webapp")}, oauth.ID: {Agent: fav.ProviderClaude}}
	running := list("status:live", true, live)
	if len(running) != 2 || running[key(oauth)] != store.BySession(oauth.Provider, oauth.ID) || running[fresh] == nil || running[fresh].Project != "webapp" {
		t.Fatalf("running: the favorite's own record plus a row for the session the index has not seen: %v", running)
	}
	if again := list("status:live", true, live); again[fresh] != running[fresh] {
		t.Error("a made-up row must be the same object on the next call")
	}
	if favs := list("status:live", false, live); len(favs) != 1 || favs[key(oauth)] == nil {
		t.Errorf("favorites only: %v", favs)
	}

	r := running[key(oauth)]
	if _, err := index.Trash(store, r, index.SessionFilesOf(idx, r)); err != nil {
		t.Fatal(err)
	}
	trashed := list("status:trash", false, nil)
	if trashed[key(oauth)] == nil || trashed[key(oauth)].Title != r.Title || list("status:trash", false, nil)[key(oauth)] != trashed[key(oauth)] {
		t.Fatalf("trash rows come from the manifest, the same object each call: %v", trashed)
	}
	if _, ok := list("status:all", true, nil)[key(oauth)]; ok {
		t.Error("a trashed session is still listed")
	}
	if _, _, err := index.Restore(store, oauth.Provider, oauth.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := list("status:all", true, nil)[key(oauth)]; !ok || len(list("status:trash", false, nil)) != 0 {
		t.Error("the restored session is listed again and gone from the trash")
	}
	if _, err := os.Stat(oauth.Path); err != nil {
		t.Errorf("the transcript is back: %v", err)
	}
}

func TestTrashingAChainTakesEveryFileOfIt(t *testing.T) {
	d, idx, store := load(t)
	chain := d.Get("chain-new")
	var r *fav.Rec
	for _, x := range idx.Attach(store, nil) {
		if x.SessionID == chain.ID {
			r = x
		}
	}
	if r == nil {
		t.Fatal("the chain is listed")
	}
	if _, err := index.Trash(store, r, index.SessionFilesOf(idx, r)); err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Rescan(nil)
	for _, s := range idx.Sessions() {
		if s.SessionID == chain.ID || s.SessionID == d.Get("chain-old").ID {
			t.Fatalf("part of the trashed chain is listed again: %+v", s)
		}
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
	if _, err := fulltext.Update(context.Background(), dir, idx.Paths(), fulltext.Options{}, nil); err != nil {
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

func TestTranscriptFoundUnderConfiguredHomes(t *testing.T) {
	d, _, _ := load(t)
	for _, name := range []string{"oauth", "codex-cli"} {
		s := d.Get(name)
		if got := capture.TranscriptPath(s.Provider, s.ID); !paths.Same(got, s.Path) {
			t.Errorf("%s: transcript %q, want %q", name, got, s.Path)
		}
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
	q := testkit.JSONString(moved)
	b, err := os.ReadFile(filepath.Join(d.Claude, "projects", index.ClaudeProjectName(moved), pg.ID+".jsonl"))
	if err != nil || !strings.Contains(string(b), `"cwd":`+q) {
		t.Errorf("the transcript under the new project dir carries the escaped new cwd: %v", err)
	}
}
