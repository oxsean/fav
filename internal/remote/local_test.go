package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func localMachine(t *testing.T) (*fixture.Dataset, *Client) {
	t.Helper()
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	return d, pipeClient(t, NewLocal("test"))
}

func code(err error) string {
	var e *wire.Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func TestLocalHello(t *testing.T) {
	_, c := localMachine(t)
	defer i18n.Set(i18n.ZH)
	var h Hello
	if err := c.Call(context.Background(), MHello, HelloParams{Lang: i18n.EN}, &h); err != nil {
		t.Fatal(err)
	}
	if h.Proto != wire.Proto || h.Version != "test" || h.OS == "" || h.Arch == "" || len(h.Endpoint) != 12 || h.Home == "" || h.Sep == "" {
		t.Fatalf("%+v", h)
	}
	if _, ok := h.CLIs[tend.ProviderCodex]; !ok || len(h.CLIs) != 2 {
		t.Errorf("clis: %v", h.CLIs)
	}
	for _, m := range []string{MHello, MList, MMessages, MText, MSteps, MPulse, MChecks, MLive, MEcho, MQuery, MPut, MGrep, MHits} {
		if !slices.Contains(h.Methods, m) {
			t.Errorf("methods lack %s: %v", m, h.Methods)
		}
	}
	if i18n.T("remote.err.offline") != "offline" {
		t.Error("hello's lang sets the language check texts come back in")
	}
	var again Hello
	c.Call(context.Background(), MHello, nil, &again)
	t.Setenv("CODEX_HOME", t.TempDir())
	var other Hello
	c.Call(context.Background(), MHello, nil, &other)
	if again.Endpoint != h.Endpoint || other.Endpoint == h.Endpoint {
		t.Errorf("the endpoint is stable and follows the config dirs: %s %s %s", h.Endpoint, again.Endpoint, other.Endpoint)
	}
}

func TestLocalListsEverySessionAndFollowsTheStore(t *testing.T) {
	d, c := localMachine(t)
	list := func() map[string]Session {
		t.Helper()
		var l List
		if err := c.Call(context.Background(), MList, nil, &l); err != nil {
			t.Fatal(err)
		}
		out := map[string]Session{}
		for _, s := range l.Sessions {
			out[tend.SessionKey(s.Provider, s.SessionID)] = s
		}
		return out
	}
	got := list()
	for _, s := range d.Sessions {
		_, ok := got[tend.SessionKey(s.Provider, s.ID)]
		if want := !s.Agent && s.Name != "chain-old"; ok != want {
			t.Errorf("%s: listed %v, want %v (short sessions included, agent runs not)", s.Name, ok, want)
		}
	}
	oauth := d.Get("oauth")
	if s := got[tend.SessionKey(oauth.Provider, oauth.ID)]; s.Turns != 4 || s.Transcript == "" || s.LastAt.IsZero() {
		t.Errorf("index fields travel with the list: %+v", s)
	}
	arch := d.Get("archived")
	if s := got[tend.SessionKey(arch.Provider, arch.ID)]; s.ID == "" || s.ArchivedAt == nil || s.FavoritedAt == nil {
		t.Errorf("archived favorites are listed with their record: %+v", s)
	}

	store, err := tend.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(store.BySession(arch.Provider, arch.ID), func(r *tend.Rec) { r.Title = "改过的标题" }); err != nil {
		t.Fatal(err)
	}
	if s := list()[tend.SessionKey(arch.Provider, arch.ID)]; s.Title != "改过的标题" {
		t.Errorf("the next list reloads the store: %q", s.Title)
	}
}

func TestLocalReadsByRef(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	ref := func(name string) Ref { s := d.Get(name); return Ref{s.Provider, s.ID} }

	var p capture.Page
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref("oauth"), Before: -1, N: 2}, &p); err != nil || len(p.Msgs) != 2 {
		t.Fatalf("messages: %v %+v", err, p)
	}
	var tx Text
	if err := c.Call(ctx, MText, TextParams{Ref: ref("oauth"), Off: p.Msgs[0].Off, Fallback: "x"}, &tx); err != nil || tx.Text == "" || tx.Text == "x" {
		t.Errorf("text: %v %q", err, tx.Text)
	}
	var pr PulseResult
	if err := c.Call(ctx, MPulse, ref("oauth"), &pr); err != nil || !pr.OK || pr.Pulse.Size == 0 {
		t.Errorf("pulse: %v %+v", err, pr)
	}
	var ch Checks
	if err := c.Call(ctx, MChecks, ref("missing-dir"), &ch); err != nil || len(ch.Checks) == 0 {
		t.Errorf("checks: %v %+v", err, ch)
	}
	var st Steps
	if err := c.Call(ctx, MSteps, StepsParams{Ref: ref("oauth"), Steps: []capture.Step{{Text: "kept"}}}, &st); err != nil || len(st.Texts) != 1 {
		t.Errorf("steps: %v %+v", err, st)
	}
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref("sdk"), Before: -1, N: 5}, &p); err != nil || len(p.Msgs) == 0 {
		t.Errorf("an agent run the list leaves out still reads: %v %+v", err, p)
	}
	var l Live
	if err := c.Call(ctx, MLive, nil, &l); err != nil {
		t.Errorf("live: %v", err)
	}

	for _, e := range []struct {
		name   string
		method string
		params any
		want   string
	}{
		{"unknown session", MMessages, MessagesParams{Ref: Ref{tend.ProviderClaude, "nope"}, Before: -1, N: 5}, wire.CodeNotFound},
		{"no ref", MPulse, Ref{}, wire.CodeBadRequest},
		{"no page size", MMessages, MessagesParams{Ref: ref("oauth"), Before: -1, N: 0}, wire.CodeBadRequest},
		{"params of the wrong shape", MChecks, json.RawMessage(`[1]`), wire.CodeBadRequest},
		{"unknown method", "grep.all", nil, wire.CodeUnknownMethod},
	} {
		if err := c.Call(ctx, e.method, e.params, nil); code(err) != e.want {
			t.Errorf("%s: %v, want %s", e.name, err, e.want)
		}
	}
	const odd = "中文 ✓ \"q\" 'x' %PATH% $HOME"
	if err := c.Call(ctx, MEcho, Text{odd}, &tx); err != nil || tx.Text != odd {
		t.Errorf("echo: %v %q", err, tx.Text)
	}
	if c.Err() != nil || strings.Contains(tx.Text, "\x00") {
		t.Error("errors from the handler leave the client working")
	}
}

func TestARewrittenTranscriptIsStale(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	s := d.Get("oauth")
	ref := Ref{s.Provider, s.ID}
	var p capture.Page
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref, Before: -1, N: 2}, &p); err != nil || p.File == "" {
		t.Fatalf("a page names its file: %v %q", err, p.File)
	}
	old := p.File
	b, _ := os.ReadFile(s.Path)
	if err := fileio.WriteAtomic(s.Path, 0o644, func(w io.Writer) error { _, err := w.Write(b); return err }); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref, Before: p.From, N: 2, File: old}, nil); code(err) != wire.CodeStale {
		t.Fatalf("an older page of the old file: %v", err)
	}
	if err := c.Call(ctx, MText, TextParams{Ref: ref, Off: p.Msgs[0].Off, File: old}, nil); code(err) != wire.CodeStale {
		t.Fatalf("a full text of the old file: %v", err)
	}

	h := NewHostsDial([]tend.Host{{Name: "m"}}, "", func(tend.Host) (*Client, error) { return Pipe(NewLocal("t")), nil })
	defer h.Close()
	src := h.Source(&tend.Rec{Host: "m", Provider: s.Provider, SessionID: s.ID})
	if pg := src.Messages(-1, 2); pg.Err != nil {
		t.Fatal(pg.Err)
	}
	fileio.WriteAtomic(s.Path, 0o644, func(w io.Writer) error { _, err := w.Write(b); return err })
	if pg := src.Messages(-1, 2); !Stale(pg.Err) {
		t.Fatalf("the tail of a rewritten file says so once: %v", pg.Err)
	}
	if pg := src.Messages(-1, 2); pg.Err != nil || len(pg.Msgs) != 2 {
		t.Fatalf("then reads on: %v", pg.Err)
	}
	os.Remove(s.Path)
	if pg := src.Messages(-1, 2); code(pg.Err) != wire.CodeNotFound {
		t.Fatalf("a transcript that cannot be read is an error, not an empty head: %v", pg.Err)
	}
}

func keysOf(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, tend.SessionKey(r.Provider, r.SessionID))
	}
	return out
}

// TestLocalQueryListsAsTheTUIDoes: the defaults are the TUI's (unarchived, three turns or a record), projects and
// task texts come with the query, pages follow one another, and status:agent is the TUI's only.
func TestLocalQueryListsAsTheTUIDoes(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	key := func(name string) string { s := d.Get(name); return tend.SessionKey(s.Provider, s.ID) }
	query := func(p QueryParams) QueryResult {
		t.Helper()
		if p.Limit == 0 {
			p.Limit = 500
		}
		var res QueryResult
		if err := c.Call(ctx, MQuery, p, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	all := query(QueryParams{All: true})
	got := keysOf(all.Rows)
	for name, want := range map[string]bool{"oauth": true, "codex-desktop": true, "archived": false, "short": false, "sdk": false} {
		if slices.Contains(got, key(name)) != want {
			t.Errorf("%s listed %v, want %v", name, !want, want)
		}
	}
	if all.Total != len(all.Rows) || all.Matched != len(all.Rows) || len(all.Tokens) != 0 || all.Next != nil {
		t.Errorf("total %d matched %d of %d, tokens %v", all.Total, all.Matched, len(all.Rows), all.Tokens)
	}
	if favs := query(QueryParams{}); len(favs.Rows) == 0 || len(favs.Rows) >= len(all.Rows) || favs.Rows[0].ID == "" {
		t.Errorf("without all: the favorites only, %d of %d", len(favs.Rows), len(all.Rows))
	}
	if r := query(QueryParams{Q: "status:all turns:1", All: true}); !slices.Contains(keysOf(r.Rows), key("archived")) || !slices.Contains(keysOf(r.Rows), key("short")) {
		t.Errorf("status:all turns:1 lists the archived and the short one")
	}

	web := QueryParams{Q: "project:p_web", All: true, Projects: []ProjectDirs{{ID: "p_web", Name: "Web", Dirs: []string{filepath.Join(d.Work, "webapp")}}}}
	inWeb := query(web)
	if !slices.Contains(keysOf(inWeb.Rows), key("oauth")) || slices.Contains(keysOf(inWeb.Rows), key("pagination")) {
		t.Errorf("project:p_web: %v", keysOf(inWeb.Rows))
	}
	for _, r := range inWeb.Rows {
		if r.Project != "p_web" {
			t.Errorf("%s: project %q", r.SessionID, r.Project)
		}
	}
	if inWeb.Total != all.Total || inWeb.Facets.Projects["p_web"] != len(inWeb.Rows) || inWeb.Facets.Projects[""] != all.Total-len(inWeb.Rows) {
		t.Errorf("facets count the scope: total %d, %+v", inWeb.Total, inWeb.Facets.Projects)
	}
	web.Q = "project:none"
	if none := query(web); slices.Contains(keysOf(none.Rows), key("oauth")) || len(none.Rows)+len(inWeb.Rows) != all.Total {
		t.Errorf("project:none: %v", keysOf(none.Rows))
	}
	if r := query(QueryParams{Q: "zebra-task", All: true, Also: map[string]string{d.Get("pagination").ID: "the zebra-task brief"}}); len(r.Rows) != 1 || r.Rows[0].SessionID != d.Get("pagination").ID {
		t.Errorf("a keyword matches the linked task's text: %v", keysOf(r.Rows))
	}
	if r := query(QueryParams{Q: "host:elsewhere #oauth", All: true}); len(r.Rows) != 1 || len(r.Tokens) != 2 || r.Tokens[0].Kind != tend.TokHost {
		t.Errorf("host: is the caller's, this machine lists its own: %v %+v", keysOf(r.Rows), r.Tokens)
	}

	var paged []string
	var after *Cursor
	for range 20 {
		r := query(QueryParams{All: true, Sort: "started", Limit: 3, After: after})
		paged = append(paged, keysOf(r.Rows)...)
		if after = r.Next; after == nil {
			break
		}
	}
	if whole := keysOf(query(QueryParams{All: true, Sort: "started"}).Rows); !slices.Equal(paged, whole) {
		t.Errorf("pages of 3 %v, one page %v", paged, whole)
	}

	for _, st := range []string{"agent"} {
		var raw json.RawMessage
		if err := c.Call(ctx, MQuery, QueryParams{Q: "status:" + st, All: true, Limit: 10}, &raw); err != nil {
			t.Fatal(err)
		}
		var r QueryResult
		json.Unmarshal(raw, &r)
		if r.Status != st || len(r.Rows) != 0 || !strings.Contains(string(raw), `"rows":[]`) {
			t.Errorf("status:%s: %s", st, raw)
		}
	}
	for _, p := range []QueryParams{{Limit: 0}, {Limit: 501}, {Limit: 5, Sort: "size"}} {
		if err := c.Call(ctx, MQuery, p, nil); code(err) != wire.CodeBadRequest {
			t.Errorf("%+v: %v", p, err)
		}
	}
}

func TestLocalPutWritesThroughTheStore(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	ref := func(name string) Ref { s := d.Get(name); return Ref{s.Provider, s.ID} }
	put := func(p PutParams) (Row, error) {
		var row Row
		err := c.Call(ctx, MPut, p, &row)
		return row, err
	}

	row, err := put(PutParams{Ref: ref("codex-desktop"), Patch: tend.Patch{Tags: new([]string{"#Trace", "logs"})}})
	if err != nil || row.ID == "" || row.FavoritedAt != nil || !slices.Equal(row.Tags, []string{"trace", "logs"}) || row.Turns == 0 {
		t.Fatalf("a session without a record gets one, not a favorite: %+v %v", row, err)
	}
	store, _ := tend.Open()
	if r := store.BySession(row.Provider, row.SessionID); r == nil || r.Favorite() {
		t.Fatalf("the record is in records.jsonl: %+v", r)
	}
	var res QueryResult
	c.Call(ctx, MQuery, QueryParams{Q: "#trace", All: true, Limit: 10}, &res)
	if len(res.Rows) != 1 || res.Rows[0].ID != row.ID {
		t.Errorf("listed once, with its record: %+v", keysOf(res.Rows))
	}

	row, err = put(PutParams{Ref: ref("cjk-dir"), Patch: tend.Patch{Favorite: new(true), Status: new(tend.StatusDone)}})
	if err != nil || row.FavoritedAt == nil || row.Status != tend.StatusDone {
		t.Fatalf("favorite: %+v %v", row, err)
	}
	again, err := put(PutParams{Ref: ref("cjk-dir"), Patch: tend.Patch{Favorite: new(true)}})
	if err != nil || !again.FavoritedAt.Equal(*row.FavoritedAt) {
		t.Errorf("a patch sets: sending it again keeps the time: %v %v", again.FavoritedAt, err)
	}

	oauth := ref("oauth")
	var before QueryResult
	c.Call(ctx, MQuery, QueryParams{Q: "#oauth", Limit: 5}, &before)
	seen := before.Rows[0].UpdatedAt
	if _, err := put(PutParams{Ref: oauth, Patch: tend.Patch{Title: new("new title")}, Expect: &seen}); err != nil {
		t.Fatalf("expect as seen: %v", err)
	}
	if _, err := put(PutParams{Ref: oauth, Patch: tend.Patch{Title: new("lost")}, Expect: &seen}); code(err) != wire.CodeStale {
		t.Fatalf("written since it was seen: %v", err)
	}
	if r := store.BySession(oauth.Provider, oauth.SessionID); store.Reload() != nil || store.BySession(oauth.Provider, oauth.SessionID).Title != "new title" || r == nil {
		t.Error("a stale put writes nothing")
	}

	for _, e := range []struct {
		name string
		p    PutParams
		want string
	}{
		{"no ref", PutParams{Patch: tend.Patch{Favorite: new(true)}}, wire.CodeBadRequest},
		{"empty patch", PutParams{Ref: oauth}, wire.CodeBadRequest},
		{"bad status", PutParams{Ref: oauth, Patch: tend.Patch{Status: new("later")}}, wire.CodeBadRequest},
		{"blank title", PutParams{Ref: oauth, Patch: tend.Patch{Title: new("  ")}}, wire.CodeBadRequest},
		{"unknown session", PutParams{Ref: Ref{tend.ProviderClaude, "nope"}, Patch: tend.Patch{Favorite: new(true)}}, wire.CodeNotFound},
	} {
		if _, err := put(e.p); code(err) != e.want {
			t.Errorf("%s: %v, want %s", e.name, err, e.want)
		}
	}
}

// TestLocalTrashAndRestoreAsTheTUIDoes: trash moves a session's files into the trash the way the TUI's delete does and
// refuses a running one; status:trash lists it with when it was deleted, its conversation still reads there, and
// restore brings it back into the lists.
func TestLocalTrashAndRestoreAsTheTUIDoes(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	s := d.Get("oauth")
	ref := Ref{s.Provider, s.ID}
	listed := func(q string) (QueryResult, []string) {
		t.Helper()
		var res QueryResult
		if err := c.Call(ctx, MQuery, QueryParams{Q: q, All: true, Limit: 500, Fresh: true}, &res); err != nil {
			t.Fatal(err)
		}
		return res, keysOf(res.Rows)
	}
	key := tend.SessionKey(s.Provider, s.ID)
	if _, keys := listed("status:all"); !slices.Contains(keys, key) {
		t.Fatalf("listed before: %v", keys)
	}
	cfg := tend.DefaultConfig()
	cfg.TrashDays = 9
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	var tr TrashResult
	if err := c.Call(ctx, MTrash, TrashParams{Ref: ref}, &tr); err != nil || tr.Files == 0 || tr.Title == "" {
		t.Fatalf("trash: %+v %v", tr, err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Fatalf("the transcript moved: %v", err)
	}
	if _, keys := listed("status:all"); slices.Contains(keys, key) {
		t.Fatalf("no longer listed: %v", keys)
	}
	res, keys := listed("status:trash")
	if !slices.Equal(keys, []string{key}) || res.Rows[0].DeletedAt == nil || time.Since(*res.Rows[0].DeletedAt) > time.Minute ||
		res.TrashDays != 9 || res.Status != "" || res.Matched != 1 {
		t.Fatalf("status:trash: %v %+v", keys, res)
	}
	if res, keys := listed("status:trash nothing-matches-this"); len(keys) != 0 || res.Total != 1 {
		t.Errorf("the trash filters as any list: %v %+v", keys, res)
	}
	if res, _ := listed("status:all"); res.TrashDays != 9 || res.Rows[0].DeletedAt != nil {
		t.Errorf("every answer carries trash_days, only the trash's rows deleted_at: %+v", res.Rows[0])
	}
	var p capture.Page
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref, Before: -1, N: 2}, &p); err != nil || len(p.Msgs) == 0 {
		t.Fatalf("a trashed session's conversation reads from the trash: %+v %v", p, err)
	}
	if err := c.Call(ctx, MTrash, TrashParams{Ref: ref}, nil); code(err) != wire.CodeNotFound {
		t.Errorf("trashed twice: %v", err)
	}
	if err := c.Call(ctx, MPut, PutParams{Ref: ref, Patch: tend.Patch{Favorite: new(true)}}, nil); code(err) != wire.CodeNotFound {
		t.Errorf("a put on a trashed session: %v", err)
	}

	var rr RestoreResult
	if err := c.Call(ctx, MRestore, RestoreParams{Ref: ref}, &rr); err != nil || rr.Files != tr.Files || rr.Title != tr.Title {
		t.Fatalf("restore: %+v %v", rr, err)
	}
	if _, err := os.Stat(s.Path); err != nil {
		t.Fatalf("the transcript is back: %v", err)
	}
	if _, keys := listed("status:all"); !slices.Contains(keys, key) {
		t.Fatalf("listed again: %v", keys)
	}
	if _, keys := listed("status:trash"); len(keys) != 0 {
		t.Fatalf("the trash is empty: %v", keys)
	}
	if err := c.Call(ctx, MRestore, RestoreParams{Ref: ref}, nil); code(err) != wire.CodeNotFound {
		t.Errorf("restored twice: %v", err)
	}

	running := d.Get("pagination")
	pid := filepath.Join(d.Claude, "sessions", fmt.Sprint(os.Getpid())+".json")
	if err := os.MkdirAll(filepath.Dir(pid), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pid, []byte(fmt.Sprintf(`{"pid":%d,"sessionId":%q}`, os.Getpid(), running.ID)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, MTrash, TrashParams{Ref: Ref{running.Provider, running.ID}}, nil); code(err) != wire.CodeBusy {
		t.Errorf("a running session: %v", err)
	}
	if _, err := os.Stat(running.Path); err != nil {
		t.Errorf("a running session's files stay: %v", err)
	}
	for _, e := range []struct {
		method string
		params any
		want   string
	}{
		{MTrash, TrashParams{}, wire.CodeBadRequest},
		{MRestore, RestoreParams{}, wire.CodeBadRequest},
		{MTrash, TrashParams{Ref: Ref{tend.ProviderClaude, "nope"}}, wire.CodeNotFound},
	} {
		if err := c.Call(ctx, e.method, e.params, nil); code(err) != e.want {
			t.Errorf("%s %+v: %v, want %s", e.method, e.params, err, e.want)
		}
	}
}

func keysOfHits(hits []GrepHit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, tend.SessionKey(h.Row.Provider, h.Row.SessionID))
	}
	return out
}

// TestLocalGrepSearchesAsTendGrepDoes: the scope's defaults are message search's (any status, short ones too), hits
// rank and carry what the page shows; a first search within a short budget says the store is building and finds the
// rest once it is built; another process building the store leaves it searching what is there.
func TestLocalGrepSearchesAsTendGrepDoes(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	key := func(name string) string { s := d.Get(name); return tend.SessionKey(s.Provider, s.ID) }
	grep := func(p GrepParams) GrepResult {
		t.Helper()
		var res GrepResult
		if err := c.Call(ctx, MGrep, p, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}

	first := grep(GrepParams{Q: "回调 state", BudgetMS: 1})
	if first.Building == nil || first.Busy {
		t.Fatalf("a first search within 1 ms: the store is building: %+v", first)
	}
	res := grep(GrepParams{Q: "> 回调 state", BudgetMS: 60_000})
	if res.Building != nil || len(res.Hits) == 0 || res.Hits[0].Row.SessionID != d.Get("oauth").ID {
		t.Fatalf("the rest is found once it is built: %+v", res)
	}
	if len(res.Hits) < len(first.Hits) {
		t.Errorf("the second search finds at least what the first did: %d < %d", len(res.Hits), len(first.Hits))
	}
	h := res.Hits[0]
	if !h.AllInOne || h.Hits < 2 || h.Off <= 0 || h.At.IsZero() || h.Latest.Before(h.At) || h.File != fileio.ID(d.Get("oauth").Path) || h.Row.ID == "" {
		t.Errorf("the hit: %+v", h)
	}
	if len(h.Spans) == 0 || !strings.Contains(h.Snippet, "state") {
		t.Errorf("the snippet and what to highlight in it: %q %v", h.Snippet, h.Spans)
	}
	for _, sp := range h.Spans {
		if w := strings.ToLower(h.Snippet[sp[0]:sp[1]]); w != "state" && w != "回调" {
			t.Errorf("span %v marks %q", sp, w)
		}
	}

	all := grep(GrepParams{Q: "测试", All: true})
	if len(all.Hits) < 3 {
		t.Fatalf("测试: %v", keysOfHits(all.Hits))
	}
	if two := grep(GrepParams{Q: "测试", All: true, Limit: 2}); !slices.Equal(keysOfHits(two.Hits), keysOfHits(all.Hits)[:2]) {
		t.Errorf("limit keeps the best: %v of %v", keysOfHits(two.Hits), keysOfHits(all.Hits))
	}
	if r := grep(GrepParams{Q: "流水线"}); !slices.Contains(keysOfHits(r.Hits), key("archived")) {
		t.Errorf("an archived favorite is searched: %v", keysOfHits(r.Hits))
	}
	if r := grep(GrepParams{Q: "版本"}); slices.Contains(keysOfHits(r.Hits), key("short")) {
		t.Errorf("without all: favorites only")
	}
	if r := grep(GrepParams{Q: "版本", All: true}); !slices.Contains(keysOfHits(r.Hits), key("short")) {
		t.Errorf("with all: a short session too: %v", keysOfHits(r.Hits))
	}

	web := []ProjectDirs{{ID: "p_web", Name: "Web", Dirs: []string{filepath.Join(d.Work, "webapp")}}}
	passed := grep(GrepParams{Q: "通过", All: true})
	inWeb := grep(GrepParams{Q: "通过 project:p_web", All: true, Projects: web})
	if !slices.Contains(keysOfHits(inWeb.Hits), key("oauth")) || !slices.Contains(keysOfHits(passed.Hits), key("missing-dir")) || len(inWeb.Hits) >= len(passed.Hits) {
		t.Errorf("project:p_web: %v of %v", keysOfHits(inWeb.Hits), keysOfHits(passed.Hits))
	}
	for _, h := range inWeb.Hits {
		if h.Row.Project != "p_web" {
			t.Errorf("%s: project %q", h.Row.SessionID, h.Row.Project)
		}
	}
	if none := grep(GrepParams{Q: "通过 project:none", All: true, Projects: web}); len(none.Hits)+len(inWeb.Hits) != len(passed.Hits) || !slices.Contains(keysOfHits(none.Hits), key("missing-dir")) {
		t.Errorf("project:none: %v", keysOfHits(none.Hits))
	}

	if r := grep(GrepParams{Q: "boundaires", All: true}); !slices.Equal(r.Fixes, []string{"boundaries"}) || len(r.Hits) != 1 || r.Hits[0].Row.SessionID != d.Get("pagination").ID {
		t.Errorf("a typo searches the word the sessions use and says so: %+v %v", r.Fixes, keysOfHits(r.Hits))
	}
	var many []string
	for i := range 65 {
		many = append(many, fmt.Sprintf("w%d", i))
	}
	var raw json.RawMessage
	if err := c.Call(ctx, MGrep, GrepParams{Q: strings.Join(many, " ")}, &raw); err != nil || !strings.Contains(string(raw), `"too_long":true`) || !strings.Contains(string(raw), `"hits":[]`) {
		t.Errorf("too many keywords: %s %v", raw, err)
	}
	if r := grep(GrepParams{Q: "project:p_web"}); len(r.Hits) != 0 || r.TooLong {
		t.Errorf("no keyword: nothing searched: %+v", r)
	}

	unlock, err := filelock.TryLock(filepath.Join(fulltext.Dir(), ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	busy := grep(GrepParams{Q: "回调 state"})
	unlock()
	if !busy.Busy || busy.Building != nil || len(busy.Hits) == 0 {
		t.Errorf("another process holds the store: searched what is there: %+v", busy)
	}

	for _, p := range []GrepParams{{Q: "x", Limit: -1}, {Q: "x", Limit: 501}, {Q: "x", BudgetMS: -1}} {
		if err := c.Call(ctx, MGrep, p, nil); code(err) != wire.CodeBadRequest {
			t.Errorf("%+v: %v", p, err)
		}
	}
}

// TestLocalHitsAndFindMarkTheKeywords: hits lists one session's matching messages newest first, each with what to
// highlight; messages with find carries the same marks, and leaves them out without it.
func TestLocalHitsAndFindMarkTheKeywords(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	oauth := d.Get("oauth")
	ref := Ref{oauth.Provider, oauth.ID}
	var g GrepResult
	if err := c.Call(ctx, MGrep, GrepParams{Q: "state", BudgetMS: 60_000}, &g); err != nil || g.Building != nil {
		t.Fatalf("build the store: %v %+v", err, g)
	}

	var hs HitsResult
	if err := c.Call(ctx, MHits, HitsParams{Ref: ref, Q: "state"}, &hs); err != nil {
		t.Fatal(err)
	}
	if hs.Total < 3 || len(hs.Hits) != hs.Total {
		t.Fatalf("every hit: %+v", hs)
	}
	roles := map[string]bool{}
	for i, h := range hs.Hits {
		roles[h.Role] = true
		if i > 0 && h.At.After(hs.Hits[i-1].At) {
			t.Errorf("newest first: %v after %v", h.At, hs.Hits[i-1].At)
		}
		if h.File != fileio.ID(oauth.Path) || len(h.Spans) == 0 {
			t.Errorf("hit %d: %+v", i, h)
		}
		for _, sp := range h.Spans {
			if w := strings.ToLower(h.Text[sp[0]:sp[1]]); w != "state" {
				t.Errorf("hit %d: span %v marks %q", i, sp, w)
			}
		}
	}
	if !roles["user"] || !roles["assistant"] || !roles["tool"] || len(roles) != 3 {
		t.Errorf("who said it: %v", roles)
	}
	var one HitsResult
	if err := c.Call(ctx, MHits, HitsParams{Ref: ref, Q: "state", Limit: 1}, &one); err != nil || len(one.Hits) != 1 || one.Total != hs.Total || one.Hits[0].Off != hs.Hits[0].Off {
		t.Errorf("limit keeps the newest, total counts all: %+v %v", one, err)
	}

	var p capture.Page
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref, Before: -1, N: 50, Find: "state #oauth"}, &p); err != nil {
		t.Fatal(err)
	}
	marked := 0
	for _, m := range p.Msgs {
		has := strings.Contains(strings.ToLower(m.Text), "state")
		if has != (len(m.Spans) > 0) {
			t.Errorf("%q: spans %v", m.Text, m.Spans)
		}
		for _, sp := range m.Spans {
			if w := strings.ToLower(m.Text[sp[0]:sp[1]]); w != "state" {
				t.Errorf("span %v marks %q", sp, w)
			}
		}
		if has {
			marked++
		}
	}
	userHit := slices.IndexFunc(hs.Hits, func(h Hit) bool { return h.Role == "user" })
	if marked == 0 || userHit < 0 || !slices.ContainsFunc(p.Msgs, func(m capture.Message) bool { return m.Off == hs.Hits[userHit].Off && len(m.Spans) > 0 }) {
		t.Errorf("a hit's offset is its message's: %d marked, hits %+v", marked, hs.Hits)
	}
	for _, h := range hs.Hits {
		var at capture.Page
		if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref, Before: h.Off + 1, N: 3, Find: "state"}, &at); err != nil || len(at.Msgs) == 0 || at.Msgs[0].Off != h.Off {
			t.Errorf("a page read to just past a hit ends with its message: %+v %v", at.Msgs, err)
		}
	}
	var raw json.RawMessage
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref, Before: -1, N: 50}, &raw); err != nil || strings.Contains(string(raw), `"spans"`) {
		t.Errorf("without find, no spans: %v", err)
	}

	for _, e := range []struct {
		name string
		p    HitsParams
		want string
	}{
		{"no ref", HitsParams{Q: "state"}, wire.CodeBadRequest},
		{"unknown session", HitsParams{Ref: Ref{tend.ProviderClaude, "nope"}, Q: "state"}, wire.CodeNotFound},
		{"bad limit", HitsParams{Ref: ref, Q: "state", Limit: -1}, wire.CodeBadRequest},
	} {
		if err := c.Call(ctx, MHits, e.p, nil); code(err) != e.want {
			t.Errorf("%s: %v, want %s", e.name, err, e.want)
		}
	}
}
