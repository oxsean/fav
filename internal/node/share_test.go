package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

type twoSessions struct{}

func (twoSessions) Handle(_ context.Context, method string, _ json.RawMessage) (any, error) {
	if method == remote.MList {
		return remote.List{Sessions: []remote.Session{{SessionID: "s-run"}, {SessionID: "s-mine"}}}, nil
	}
	return remote.Text{Text: "hi"}, nil
}

func TestANodeSharingRunsAnswersOnlyItsRunsSessions(t *testing.T) {
	dir := filepath.Join(tend.Home(), "node")
	os.MkdirAll(dir, 0o700)
	if err := capture.KeepRunSession(dir, "s-run", capture.RunSession{Run: "r_1"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := shareSessions(ctx, ShareRuns, twoSessions{}, remote.MList, nil)
	if l, ok := res.(remote.List); err != nil || !ok || len(l.Sessions) != 1 || l.Sessions[0].SessionID != "s-run" {
		t.Fatalf("%+v %v", res, err)
	}
	ref := func(sid string) json.RawMessage {
		b, _ := json.Marshal(remote.MessagesParams{Ref: remote.Ref{SessionID: sid}})
		return b
	}
	if _, err := shareSessions(ctx, ShareRuns, twoSessions{}, remote.MMessages, ref("s-mine")); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("a session no run left: %v", err)
	}
	if _, err := shareSessions(ctx, ShareRuns, twoSessions{}, remote.MMessages, ref("s-run")); err != nil {
		t.Fatalf("a run's session: %v", err)
	}
	if _, err := shareSessions(ctx, ShareNone, twoSessions{}, remote.MMessages, ref("s-run")); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("none: %v", err)
	}
	if _, err := shareSessions(ctx, ShareAll, twoSessions{}, remote.MMessages, ref("s-mine")); err != nil {
		t.Fatalf("all: %v", err)
	}
}

type liveSessions struct{ twoSessions }

func (l liveSessions) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == remote.MLive {
		return remote.Live{Live: map[string]capture.Live{"s-run": {Run: "r_1"}, "s-mine": {Title: "private", Cwd: "/home/me/secret"}}}, nil
	}
	return l.twoSessions.Handle(ctx, method, params)
}

func TestANodeSharingRunsTellsOnlyItsRunsSessionsLiveOrChecked(t *testing.T) {
	dir := filepath.Join(tend.Home(), "node")
	os.MkdirAll(dir, 0o700)
	if err := capture.KeepRunSession(dir, "s-run", capture.RunSession{Run: "r_1"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := shareSessions(ctx, ShareRuns, liveSessions{}, remote.MLive, nil)
	if l, ok := res.(remote.Live); err != nil || !ok || len(l.Live) != 1 || l.Live["s-run"].Run != "r_1" {
		t.Fatalf("live under runs: %+v %v", res, err)
	}
	if res, err := shareSessions(ctx, ShareNone, liveSessions{}, remote.MLive, nil); err != nil || len(res.(remote.Live).Live) != 0 {
		t.Fatalf("live under none: %+v %v", res, err)
	}
	if res, err := shareSessions(ctx, ShareAll, liveSessions{}, remote.MLive, nil); err != nil || len(res.(remote.Live).Live) != 2 {
		t.Fatalf("live under all: %+v %v", res, err)
	}
	b, _ := json.Marshal(remote.Ref{SessionID: "s-mine"})
	if _, err := shareSessions(ctx, ShareRuns, liveSessions{}, remote.MChecks, b); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("checks of a session no run left: %v", err)
	}
}

// TestASharedQueryCountsAndPagesOnlyTheRunsSessions: under runs a query leaves the other sessions out before it counts
// and pages (the trash's too), and a put, trash or restore of one of them is refused; none refuses them all.
func TestASharedQueryCountsAndPagesOnlyTheRunsSessions(t *testing.T) {
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	dir := filepath.Join(tend.Home(), "node")
	os.MkdirAll(dir, 0o700)
	for _, name := range []string{"oauth", "pagination"} {
		if err := capture.KeepRunSession(dir, d.Get(name).ID, capture.RunSession{Run: "r_" + name}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	local := remote.NewLocal("test")
	query := func(share string, p remote.QueryParams) (remote.QueryResult, error) {
		b, _ := json.Marshal(p)
		res, err := shareSessions(ctx, share, local, remote.MQuery, b)
		r, _ := res.(remote.QueryResult)
		return r, err
	}
	all, err := query(ShareAll, remote.QueryParams{All: true, Limit: 500})
	if err != nil || all.Total < 5 {
		t.Fatalf("all: %d %v", all.Total, err)
	}
	first, err := query(ShareRuns, remote.QueryParams{All: true, Limit: 1})
	if err != nil || first.Total != 2 || first.Matched != 2 || len(first.Rows) != 1 || first.Next == nil {
		t.Fatalf("runs, first page: %+v %v", first, err)
	}
	second, _ := query(ShareRuns, remote.QueryParams{All: true, Limit: 1, After: first.Next})
	if len(second.Rows) != 1 || second.Next != nil || second.Rows[0].SessionID == first.Rows[0].SessionID {
		t.Fatalf("runs, second page: %+v", second)
	}
	if n := first.Facets.Projects[""]; n != 2 {
		t.Errorf("facets count the runs' sessions only: %+v", first.Facets)
	}
	if _, err := query(ShareNone, remote.QueryParams{All: true, Limit: 5}); wire.Code(err) != wire.CodeUnauthorized {
		t.Errorf("none: %v", err)
	}

	put := func(name string) error {
		s := d.Get(name)
		b, _ := json.Marshal(remote.PutParams{Ref: remote.Ref{Provider: s.Provider, SessionID: s.ID}, Patch: tend.Patch{Favorite: new(true)}})
		_, err := shareSessions(ctx, ShareRuns, local, remote.MPut, b)
		return err
	}
	if err := put("codex-desktop"); wire.Code(err) != wire.CodeUnauthorized {
		t.Errorf("a put on a session no run left: %v", err)
	}
	if err := put("pagination"); err != nil {
		t.Errorf("a put on a run's session: %v", err)
	}

	call := func(share, method, name string) error {
		s := d.Get(name)
		b, _ := json.Marshal(remote.Ref{Provider: s.Provider, SessionID: s.ID})
		_, err := shareSessions(ctx, share, local, method, b)
		return err
	}
	for _, m := range []string{remote.MTrash, remote.MRestore} {
		if err := call(ShareRuns, m, "codex-desktop"); wire.Code(err) != wire.CodeUnauthorized {
			t.Errorf("%s of a session no run left: %v", m, err)
		}
		if err := call(ShareNone, m, "pagination"); wire.Code(err) != wire.CodeUnauthorized {
			t.Errorf("%s under none: %v", m, err)
		}
	}
	if err := call(ShareRuns, remote.MTrash, "pagination"); err != nil {
		t.Fatalf("trash of a run's session: %v", err)
	}
	if err := call(ShareAll, remote.MTrash, "codex-desktop"); err != nil {
		t.Fatalf("trash under all: %v", err)
	}
	if trash, err := query(ShareRuns, remote.QueryParams{Q: "status:trash", Limit: 5}); err != nil || trash.Total != 1 ||
		len(trash.Rows) != 1 || trash.Rows[0].SessionID != d.Get("pagination").ID || trash.Rows[0].DeletedAt == nil {
		t.Errorf("the trash under runs lists the runs' sessions only: %+v %v", trash, err)
	}
	if err := call(ShareRuns, remote.MRestore, "pagination"); err != nil {
		t.Errorf("restore of a run's session: %v", err)
	}
}

// TestASharedGrepRanksOnlyTheRunsSessions: under runs a message search ranks only the runs' sessions, hits in another
// one are refused; none refuses the search.
func TestASharedGrepRanksOnlyTheRunsSessions(t *testing.T) {
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	dir := filepath.Join(tend.Home(), "node")
	os.MkdirAll(dir, 0o700)
	if err := capture.KeepRunSession(dir, d.Get("oauth").ID, capture.RunSession{Run: "r_oauth"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	local := remote.NewLocal("test")
	grep := func(share string) (remote.GrepResult, error) {
		b, _ := json.Marshal(remote.GrepParams{Q: "通过", All: true, BudgetMS: 60_000})
		res, err := shareSessions(ctx, share, local, remote.MGrep, b)
		r, _ := res.(remote.GrepResult)
		return r, err
	}
	all, err := grep(ShareAll)
	if err != nil || len(all.Hits) < 2 {
		t.Fatalf("all: %+v %v", all, err)
	}
	runs, err := grep(ShareRuns)
	if err != nil || len(runs.Hits) != 1 || runs.Hits[0].Row.SessionID != d.Get("oauth").ID {
		t.Fatalf("runs: only the run's session is ranked: %+v %v", runs, err)
	}
	if _, err := grep(ShareNone); wire.Code(err) != wire.CodeUnauthorized {
		t.Errorf("none: %v", err)
	}

	hits := func(name string) error {
		s := d.Get(name)
		b, _ := json.Marshal(remote.HitsParams{Ref: remote.Ref{Provider: s.Provider, SessionID: s.ID}, Q: "通过"})
		_, err := shareSessions(ctx, ShareRuns, local, remote.MHits, b)
		return err
	}
	if err := hits("missing-dir"); wire.Code(err) != wire.CodeUnauthorized {
		t.Errorf("hits in a session no run left: %v", err)
	}
	if err := hits("oauth"); err != nil {
		t.Errorf("hits in a run's session: %v", err)
	}
}

func TestWatchPushesARecordsChange(t *testing.T) {
	defer func(d time.Duration) { watchEvery = d }(watchEvery)
	watchEvery = 20 * time.Millisecond
	n := New(t.TempDir())
	os.MkdirAll(n.Dir, 0o700)
	done := make(chan struct{})
	defer close(done)
	got := make(chan Changed, 10)
	go n.Watch(done, func(ch Changed) { got <- ch })
	time.Sleep(3 * watchEvery)
	if err := os.WriteFile(filepath.Join(filepath.Dir(n.Dir), tend.RecordsFile), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case ch := <-got:
		if !ch.Records || len(ch.Runs) != 0 {
			t.Fatalf("%+v", ch)
		}
		b, _ := json.Marshal(ch)
		if string(b) != `{"runs":null,"records":true}` {
			t.Errorf("the push: %s", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no push after records.jsonl was written")
	}
	select {
	case ch := <-got:
		t.Fatalf("nothing changed since, yet %+v", ch)
	case <-time.After(5 * watchEvery):
	}
}
