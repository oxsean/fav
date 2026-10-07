package remote

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/migrate"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// NewLocal is this machine answering the protocol (`tend rpc`); version is tend's build version.
func NewLocal(version string) Handler { return &localHandler{version: version} }

type localHandler struct {
	version string
	mu      sync.Mutex
	store   *tend.Store
	idx     *index.Index
	unfav   []*tend.Rec
	indexed time.Time        // the index's last refresh
	text    fulltext.Builder // the message-search store, one update at a time in this process
}

var methods = []string{MHello, MList, MMessages, MText, MSteps, MPulse, MChecks, MLive, MEcho, MQuery, MPut, MGrep, MHits, MTrash, MRestore,
	MMemoryList, MMemoryRead, MMemoryTrash, MMemoryRestore, MMemoryPut, MHandoffFacts, MHandoffPut,
	MEnv, MEnvFile, MExportPlan, MExportRead, MExportDone, MCopies, MImportBegin, MImportChunk, MImportCommit, MImportAbort}

// Scoped is a Handler that answers a method over some of this machine's sessions only: keep names them by session id.
type Scoped interface {
	Handler
	HandleIn(ctx context.Context, method string, params json.RawMessage, keep func(sessionID string) bool) (any, error)
}

func (h *localHandler) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return h.HandleIn(ctx, method, params, nil)
}

// HandleIn answers method; query, put, trash, restore and grep see only the sessions keep keeps (nil: every one).
func (h *localHandler) HandleIn(ctx context.Context, method string, params json.RawMessage, keep func(string) bool) (any, error) {
	switch method {
	case MHello:
		var p HelloParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		if p.Lang != "" {
			i18n.Set(p.Lang)
		}
		return hello(h.version), nil
	case MList:
		return h.list()
	case MQuery:
		var p QueryParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.query(p, keep)
	case MPut:
		var p PutParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.put(p, keep)
	case MTrash:
		var p TrashParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.trash(p.Ref, keep)
	case MRestore:
		var p RestoreParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.restore(p.Ref, keep)
	case MGrep:
		var p GrepParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.grep(ctx, p, keep)
	case MHits:
		var p HitsParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.hits(ctx, p)
	case MLive:
		return Live{Live: capture.LiveSessions()}, nil
	case MEcho:
		var t Text
		err := decode(params, &t)
		return t, err
	case MMessages:
		var p MessagesParams
		src, err := h.source(params, &p, &p.Ref)
		if err != nil {
			return nil, err
		}
		if p.N <= 0 {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "n"}
		}
		id, err := sameFile(src, p.File)
		if err != nil {
			return nil, err
		}
		page := src.Messages(p.Before, p.N)
		page.File = id
		if kws := keywords(p.Find); len(kws) > 0 {
			for i := range page.Msgs {
				page.Msgs[i].Spans = fulltext.Spans(kws, page.Msgs[i].Text)
			}
		}
		return page, nil
	case MText:
		var p TextParams
		src, err := h.source(params, &p, &p.Ref)
		if err != nil {
			return nil, err
		}
		if _, err := sameFile(src, p.File); err != nil {
			return nil, err
		}
		return Text{Text: src.TextFull(p.Off, p.Fallback)}, nil
	case MSteps:
		var p StepsParams
		src, err := h.source(params, &p, &p.Ref)
		if err != nil {
			return nil, err
		}
		if _, err := sameFile(src, p.File); err != nil {
			return nil, err
		}
		return Steps{Texts: src.StepsFull(p.Steps)}, nil
	case MPulse:
		var p Ref
		src, err := h.source(params, &p, &p)
		if err != nil {
			return nil, err
		}
		pulse, ok := src.Pulse()
		return PulseResult{Pulse: pulse, OK: ok}, nil
	case MChecks:
		var p Ref
		src, err := h.source(params, &p, &p)
		if err != nil {
			return nil, err
		}
		return Checks{Checks: src.Checks()}, nil
	case MHandoffFacts, MHandoffPut:
		return h.handoff(ctx, method, params)
	case MMemoryList, MMemoryRead, MMemoryTrash, MMemoryRestore, MMemoryPut:
		return h.memories(ctx, method, params)
	case MEnv, MEnvFile:
		return h.env(ctx, method, params)
	case MExportPlan, MExportRead, MExportDone, MCopies, MImportBegin, MImportChunk, MImportCommit, MImportAbort:
		return h.migration(ctx, method, params)
	}
	return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: method}
}

// sameFile is the id of src's transcript; not_found when it cannot be read (not the head of an empty file), stale when
// it is not the file want names.
func sameFile(src Source, want string) (string, error) {
	path := ""
	if l, ok := src.(local); ok {
		path = l.path()
	}
	id := fileio.ID(path)
	switch {
	case id == "":
		return "", &wire.Error{Code: wire.CodeNotFound, Detail: "transcript"}
	case want != "" && want != id:
		return "", &wire.Error{Code: wire.CodeStale}
	}
	return id, nil
}

func decode(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
	}
	return nil
}

// LocalHello is how this machine greets.
func LocalHello(version string) Hello { return hello(version) }

func hello(version string) Hello {
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	wsl := os.Getenv("WSL_DISTRO_NAME")
	claude, codex := capture.ClaudeHome(), capture.CodexHome()
	sum := sha256.Sum256([]byte(strings.Join([]string{host, runtime.GOOS, wsl, tend.Home(), claude, codex}, "\x00")))
	return Hello{Proto: wire.Proto, Role: "node", Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Endpoint: hex.EncodeToString(sum[:6]), Hostname: host, WSL: wsl, Home: home, Sep: string(filepath.Separator),
		Claude: claude, Codex: codex, Methods: methods,
		CLIs: map[string]bool{tend.ProviderClaude: capture.Installed(tend.ProviderClaude), tend.ProviderCodex: capture.Installed(tend.ProviderCodex)}}
}

// queryFresh: a query reuses an index refreshed this recently, unless it asks for a fresh one.
const queryFresh = 5 * time.Second

// load opens the store and the index once, then brings the store up to date and the index too unless it was refreshed
// within maxAge; the caller holds mu.
func (h *localHandler) load(maxAge time.Duration) error {
	attach := false
	if h.store == nil {
		s, err := tend.Open()
		if err != nil {
			return err
		}
		idx, err := index.Open()
		if err != nil {
			return err
		}
		h.store, h.idx, h.indexed, attach = s, idx, time.Time{}, true
	} else if h.store.Changed() {
		if err := h.store.Reload(); err != nil {
			return err
		}
		attach = true
	}
	if maxAge <= 0 || time.Since(h.indexed) >= maxAge {
		idx, changed := h.idx.Refresh()
		if changed {
			if err := idx.Save(); err != nil {
				fmt.Fprintln(os.Stderr, i18n.F("cli.index_not_written", err))
			}
		}
		h.idx, h.indexed, attach = idx, time.Now(), true
	}
	if attach {
		h.unfav = h.idx.Attach(h.store, h.unfav)
	}
	return nil
}

// list: every session the TUI could list here (favorites, unfavorited, archived; not agent runs), whatever the filters.
func (h *localHandler) list() (List, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.load(0); err != nil {
		return List{}, err
	}
	rows := index.Rows{Copies: migrate.Marks()}
	recs, err := rows.List(h.store, h.idx, h.unfav, nil, tend.Query{Status: "all", All: true, Host: tend.HostLocal})
	if err != nil {
		return List{}, err
	}
	out := List{Sessions: make([]Session, len(recs))}
	for i, r := range recs {
		out.Sessions[i] = SessionOf(r)
	}
	return out, nil
}

// source decodes params into p and resolves its ref to this machine's record, or to its row in the trash.
func (h *localHandler) source(params json.RawMessage, p any, ref *Ref) (Source, error) {
	if err := decode(params, p); err != nil {
		return nil, err
	}
	if ref.Provider == "" || ref.SessionID == "" {
		return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "ref"}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	r, err := h.lookup(*ref, 0)
	if wire.Code(err) == wire.CodeNotFound {
		if tr, _ := index.Trashed(ref.Provider, ref.SessionID); tr != nil {
			return Local(tr), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return Local(r), nil
}

// lookup finds ref, loading first unless the store is open and the index younger than maxAge; the caller holds mu.
func (h *localHandler) lookup(ref Ref, maxAge time.Duration) (*tend.Rec, error) {
	if h.store == nil || maxAge > 0 {
		if err := h.load(maxAge); err != nil {
			return nil, err
		}
	}
	r := h.find(ref)
	if r == nil { // started or favorited since the last load
		if err := h.load(0); err != nil {
			return nil, err
		}
		r = h.find(ref)
	}
	if r == nil {
		return nil, &wire.Error{Code: wire.CodeNotFound, Detail: ref.Provider + ":" + ref.SessionID}
	}
	return r, nil
}

// query lists the sessions p picks, the way the TUI lists this machine's: tend.Parse, Rows.List over the query's scope,
// then index.Select. host: is the caller's: the query is read as this machine's own.
func (h *localHandler) query(p QueryParams, keep func(string) bool) (QueryResult, error) {
	sortBy, ok := tend.ParseSort(p.Sort)
	switch {
	case p.Limit < 1 || p.Limit > maxQueryLimit:
		return QueryResult{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "limit"}
	case !ok:
		return QueryResult{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "sort"}
	}
	if p.ID != "" {
		return h.found(p.ID, keep)
	}
	res := QueryResult{Rows: []Row{}, Tokens: tend.Tokens(p.Q)}
	if res.Tokens == nil {
		res.Tokens = []tend.Token{}
	}
	res.TrashDays = tend.LoadConfig().TrashDays
	q := tend.Parse(p.Q)
	if q.Status == tend.StatusAgent {
		res.Status = q.Status
		return res, nil
	}
	live := capture.LiveSessions()
	q.All, q.Host = p.All, tend.HostLocal
	q.Live = func(id string) bool { _, ok := live[id]; return ok }
	if len(p.Also) > 0 {
		q.Also = func(r *tend.Rec) string { return p.Also[r.SessionID] }
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.load(freshness(p.Fresh)); err != nil {
		return QueryResult{}, err
	}
	rows := index.Rows{Belong: belong(p.Projects), Copies: migrate.Marks()}
	recs, err := rows.List(h.store, h.idx, h.unfav, live, q.Scope())
	if err != nil {
		return QueryResult{}, err
	}
	if keep != nil {
		recs = slices.DeleteFunc(recs, func(r *tend.Rec) bool { return !keep(r.SessionID) })
	}
	trash := q.Status == tend.StatusTrash
	if trash { // the trash's rows match as any row: Rows.List picked them by the rest of q
		q.Status, q.Turns = "all", 0
	}
	sel := index.Select(nil, recs, q, index.Page{Sort: sortBy, After: p.After, Limit: p.Limit})
	res.Next, res.Total, res.Matched, res.Running, res.Facets = sel.Next, sel.Total, sel.Matched, sel.Running, sel.Facets
	for _, r := range sel.Rows {
		row := rowOf(r, live)
		if trash {
			row.DeletedAt = new(r.UpdatedAt)
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}

// found answers QueryParams.ID: the session ref names here by index.Find, as a listed row (its record, its
// migrations), when exactly one matches and keep keeps it.
func (h *localHandler) found(ref string, keep func(string) bool) (QueryResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.load(queryFresh); err != nil {
		return QueryResult{}, err
	}
	r, n, err := index.Find(h.store, func() (*index.Index, error) { return h.idx, nil }, ref)
	if err != nil {
		return QueryResult{}, err
	}
	if n == 1 && keep != nil && !keep(r.SessionID) {
		n = 0
	}
	res := QueryResult{ID: ref, Rows: []Row{}, Tokens: []tend.Token{}, Matched: n}
	if n != 1 {
		return res, nil
	}
	if listed := h.find(Ref{Provider: r.Provider, SessionID: r.SessionID}); listed != nil {
		r = listed
	}
	cp := *r
	rows := index.Rows{Copies: migrate.Marks()}
	rows.Place(&cp)
	res.Rows = append(res.Rows, rowOf(&cp, nil))
	return res, nil
}

// maxQueryLimit is the most rows one query answers.
const maxQueryLimit = 500

func freshness(fresh bool) time.Duration {
	if fresh {
		return 0
	}
	return queryFresh
}

func rowOf(r *tend.Rec, live map[string]capture.Live) Row {
	row := Row{Session: SessionOf(r), Project: r.ProjectID}
	if l, ok := live[r.SessionID]; ok {
		row.Live = &l
	}
	return row
}

// belong is Rows.Belong over the projects a query brings: the one whose directory on this machine holds the session's,
// by the coordinator's own rule (task.ProjectOf); nil without projects.
func belong(ps []ProjectDirs) func(*tend.Rec) (string, string) {
	if len(ps) == 0 {
		return nil
	}
	const here = "here"
	projects := make(map[string]*task.Project, len(ps))
	for _, p := range ps {
		tp := &task.Project{ID: p.ID, Name: p.Name}
		for _, d := range p.Dirs {
			tp.Repos = append(tp.Repos, task.Repo{Dirs: map[string]string{here: d}})
		}
		projects[p.ID] = tp
	}
	memo := map[string]*task.Project{}
	return func(r *tend.Rec) (string, string) {
		dir := cmp.Or(r.Repo, r.Cwd)
		if dir == "" {
			return "", ""
		}
		p, ok := memo[dir]
		if !ok {
			p = task.ProjectOf(projects, here, runtime.GOOS, dir)
			memo[dir] = p
		}
		if p == nil {
			return "", ""
		}
		return p.ID, p.Name
	}
}

// put writes p's patch to the session's record, the way the TUI's edits do (Store.Update): a session without one gets
// one, not a favorite. The answer is the row as written.
func (h *localHandler) put(p PutParams, keep func(string) bool) (Row, error) {
	switch {
	case p.Provider == "" || p.SessionID == "":
		return Row{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "ref"}
	case p.Patch.Empty():
		return Row{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "patch"}
	case p.Patch.Status != nil && !tend.ValidStatus(*p.Patch.Status):
		return Row{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "status"}
	case p.Patch.Title != nil && strings.TrimSpace(*p.Patch.Title) == "":
		return Row{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "title"}
	case keep != nil && !keep(p.SessionID):
		return Row{}, &wire.Error{Code: wire.CodeUnauthorized, Detail: "session " + p.SessionID}
	}
	live := capture.LiveSessions()
	h.mu.Lock()
	defer h.mu.Unlock()
	r, err := h.lookup(p.Ref, queryFresh)
	if err != nil {
		return Row{}, err
	}
	unsaved := r.ID == "" // nothing to overwrite unless a record appears meanwhile: expect is the record's time
	saved, err := h.store.Edit(r, func(cur *tend.Rec) error {
		if p.Expect != nil && !(unsaved && cur == r) && !cur.UpdatedAt.Equal(*p.Expect) {
			return &wire.Error{Code: wire.CodeStale}
		}
		p.Patch.Apply(cur, time.Now())
		return nil
	})
	h.unfav = h.idx.Attach(h.store, h.unfav) // a reload under the edit made new record objects; a new record leaves unfav
	if errors.Is(err, tend.ErrDeleted) {
		return Row{}, &wire.Error{Code: wire.CodeNotFound, Detail: p.Provider + ":" + p.SessionID}
	}
	if err != nil {
		return Row{}, err
	}
	return rowOf(saved, live), nil
}

// trash moves a session's files into the trash the way the TUI's delete does (index.TrashSession), a running one
// refused.
func (h *localHandler) trash(ref Ref, keep func(string) bool) (TrashResult, error) {
	switch {
	case ref.Provider == "" || ref.SessionID == "":
		return TrashResult{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "ref"}
	case keep != nil && !keep(ref.SessionID):
		return TrashResult{}, &wire.Error{Code: wire.CodeUnauthorized, Detail: "session " + ref.SessionID}
	}
	if _, ok := capture.LocalLive()[ref.SessionID]; ok {
		return TrashResult{}, &wire.Error{Code: wire.CodeBusy, Detail: "session " + ref.SessionID}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	r, err := h.lookup(ref, queryFresh)
	if err != nil {
		return TrashResult{}, err
	}
	e, next, err := index.TrashSession(h.store, h.idx, r)
	h.idx = next
	h.unfav = h.idx.Attach(h.store, h.unfav)
	if err != nil {
		return TrashResult{}, err
	}
	return TrashResult{Title: e.Title, Files: len(e.Files)}, nil
}

// restore puts a trashed session back the way the TUI does (index.RestoreSession) and refreshes the index to list it.
func (h *localHandler) restore(ref Ref, keep func(string) bool) (RestoreResult, error) {
	switch {
	case ref.Provider == "" || ref.SessionID == "":
		return RestoreResult{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "ref"}
	case keep != nil && !keep(ref.SessionID):
		return RestoreResult{}, &wire.Error{Code: wire.CodeUnauthorized, Detail: "session " + ref.SessionID}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.load(queryFresh); err != nil {
		return RestoreResult{}, err
	}
	if tr, err := index.Trashed(ref.Provider, ref.SessionID); tr == nil {
		if err != nil {
			return RestoreResult{}, err
		}
		return RestoreResult{}, &wire.Error{Code: wire.CodeNotFound, Detail: ref.Provider + ":" + ref.SessionID}
	}
	e, next, err := index.RestoreSession(h.store, h.idx, ref.Provider, ref.SessionID)
	h.idx = next
	if lerr := h.load(0); err == nil {
		err = lerr
	}
	if err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{Title: e.Title, Files: len(e.Files)}, nil
}

// Message search: grep's defaults, and the most hits answers.
const (
	grepLimit    = 50
	grepBudgetMS = 3000
	hitsLimit    = 100
	maxHitsLimit = 500
)

// grep searches the messages of the sessions p's scope picks the way tend grep does (fulltext.Split, Rows.List,
// fulltext.Find), with the context of a query (projects, also) and only the sessions keep keeps. The store gets up to
// budget_ms to catch up first; what it has not read by then is built on in the background and the search covers the rest.
func (h *localHandler) grep(ctx context.Context, p GrepParams, keep func(string) bool) (GrepResult, error) {
	switch {
	case p.Limit < 0 || p.Limit > maxQueryLimit:
		return GrepResult{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "limit"}
	case p.BudgetMS < 0:
		return GrepResult{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "budget_ms"}
	}
	q, _ := fulltext.Prefixed(p.Q)
	kw, scope := fulltext.Split(q)
	res := GrepResult{Hits: []GrepHit{}}
	switch {
	case kw == "":
		return res, nil
	case fulltext.TooLong(kw):
		res.TooLong = true
		return res, nil
	}
	sq := tend.Parse(scope)
	live := capture.LiveSessions()
	sq.All, sq.Host = p.All, tend.HostLocal
	sq.Live = func(id string) bool { _, ok := live[id]; return ok }
	if len(p.Also) > 0 {
		sq.Also = func(r *tend.Rec) string { return p.Also[r.SessionID] }
	}
	h.mu.Lock()
	if err := h.load(queryFresh); err != nil {
		h.mu.Unlock()
		return GrepResult{}, err
	}
	rows := index.Rows{Belong: belong(p.Projects), Copies: migrate.Marks()}
	recs, err := rows.List(h.store, h.idx, h.unfav, live, sq)
	if err != nil {
		h.mu.Unlock()
		return GrepResult{}, err
	}
	cands := make([]*tend.Rec, 0, len(recs)) // ⚠️ copies: the search runs without mu, while a put may edit the records
	for _, r := range recs {
		if keep == nil || keep(r.SessionID) {
			cp := *r
			cands = append(cands, &cp)
		}
	}
	var pinned []*tend.Rec
	for _, r := range h.store.All() {
		if r.PinnedPath != "" {
			cp := *r
			pinned = append(pinned, &cp)
		}
	}
	indexed, bySession := h.idx.Paths(), h.idx.PathsBySession()
	h.mu.Unlock()

	if building, busy := h.text.Build(ctx, time.Duration(cmp.Or(p.BudgetMS, grepBudgetMS))*time.Millisecond, indexed, pinned, tend.LoadConfig().ToolOutput); building != nil {
		res.Building = &Progress{Done: building.Done, Total: building.Total}
	} else {
		res.Busy = busy
	}
	dir := fulltext.Dir()
	found := fulltext.Find(ctx, dir, cands, bySession, kw)
	if err := ctx.Err(); err != nil {
		return GrepResult{}, err
	}
	res.Fixes = found.Fixes
	kws := fulltext.Expand(dir, fulltext.ParseQuery(kw)).Kws
	ids := map[string]string{}
	for _, x := range found.Results[:min(len(found.Results), cmp.Or(p.Limit, grepLimit))] {
		r := cands[x.Cand]
		res.Hits = append(res.Hits, GrepHit{Row: rowOf(r, live), Hits: x.Hits, AllInOne: x.AllInOne, Snippet: x.Snippet,
			Spans: fulltext.Spans(kws, x.Snippet), Off: x.Off, File: fileID(ids, x.Path), At: x.At, Latest: x.Latest})
	}
	return res, nil
}

// hits lists the messages of one session matching p's keywords, newest first, each as the text around its first
// match: a whole message is read with messages or text.
func (h *localHandler) hits(ctx context.Context, p HitsParams) (HitsResult, error) {
	switch {
	case p.Provider == "" || p.SessionID == "":
		return HitsResult{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "ref"}
	case p.Limit < 0 || p.Limit > maxHitsLimit:
		return HitsResult{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "limit"}
	}
	h.mu.Lock()
	r, err := h.lookup(p.Ref, queryFresh)
	var paths []string
	if err == nil {
		paths = fulltext.Cands([]*tend.Rec{r}, h.idx.PathsBySession())[0].Paths
	}
	h.mu.Unlock()
	res := HitsResult{Hits: []Hit{}}
	kw := keywordText(p.Q)
	if err != nil || kw == "" {
		return res, err
	}
	dir := fulltext.Dir()
	found, total := fulltext.Hits(ctx, dir, paths, kw, cmp.Or(p.Limit, hitsLimit), fulltext.Hit{})
	if err := ctx.Err(); err != nil {
		return HitsResult{}, err
	}
	kws := fulltext.Expand(dir, fulltext.ParseQuery(kw)).Kws
	ids := map[string]string{}
	for _, x := range found {
		text := fulltext.Snippet(kw, x.Text)
		res.Hits = append(res.Hits, Hit{Off: x.Off, Role: roleName(x.Role), At: x.At, Text: text, Spans: fulltext.Spans(kws, text),
			File: fileID(ids, x.Path)})
	}
	res.Total = total
	return res, nil
}

// keywordText is the keywords of a message search, whether or not it starts with > or carries filter tokens.
func keywordText(q string) string {
	q, _ = fulltext.Prefixed(q)
	kw, _ := fulltext.Split(q)
	return kw
}

// keywords are what a message search highlights, spelling fixes included, as the TUI highlights them.
func keywords(q string) []fulltext.Keyword {
	kw := keywordText(q)
	if kw == "" {
		return nil
	}
	return fulltext.Expand(fulltext.Dir(), fulltext.ParseQuery(kw)).Kws
}

// fileID is fileio.ID of path, asked once per path.
func fileID(ids map[string]string, path string) string {
	id, ok := ids[path]
	if !ok {
		id = fileio.ID(path)
		ids[path] = id
	}
	return id
}

// roleName is a full-text entry's role as Hit names it: user, assistant (a context recap too) or tool (its output too).
func roleName(role byte) string {
	switch role {
	case 'u':
		return "user"
	case 't', 'o':
		return "tool"
	}
	return "assistant"
}

// find: the store record, else the indexed session, else a file the list leaves out (an agent run); the caller holds mu.
func (h *localHandler) find(ref Ref) *tend.Rec {
	if r := h.store.BySession(ref.Provider, ref.SessionID); r != nil && !r.Deleted {
		return r
	}
	for _, r := range h.unfav {
		if r.Provider == ref.Provider && r.SessionID == ref.SessionID {
			return r
		}
	}
	if f, _ := h.idx.FileByPrefix(ref.SessionID); f != nil && f.Provider == ref.Provider && f.SessionID == ref.SessionID {
		return f.Rec()
	}
	return nil
}
