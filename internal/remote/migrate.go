package remote

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/migrate"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// migration answers export.plan, export.read, export.done, copies and import.*.
func (h *localHandler) migration(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case MExportPlan:
		var p ExportPlanParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.exportPlan(ctx, p)
	case MExportRead:
		var p ExportReadParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		if err := migrate.Pending(p.Migration, p.Provider, p.SessionID); err != nil {
			return nil, err
		}
		r, idx, err := h.session(p.Ref)
		if err != nil {
			return nil, err
		}
		data, eof, err := migrate.Read(idx, r, p.File, p.Off, p.N, p.ID)
		return ExportChunk{Data: data, EOF: eof}, err
	case MExportDone:
		var p ExportDoneParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.exportDone(p)
	case MCopies:
		var p Ref
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		if p.Provider == "" || p.SessionID == "" {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "ref"}
		}
		rels, err := migrate.Copies(p.Provider, p.SessionID)
		out := Copies{Copies: []Copy{}}
		for _, r := range rels {
			out.Copies = append(out.Copies, Copy{Migration: r.Migration, Role: r.Role, State: r.State, Peer: r.Peer, At: r.At,
				Changed: r.Changed, Moved: r.Moved})
		}
		return out, err
	case MImportBegin:
		var p ImportBeginParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		b, err := migrate.Begin(migrate.Incoming{Migration: p.Migration, From: p.From, Provider: p.Provider, SessionID: p.SessionID,
			Title: p.Title, Cwd: p.Cwd, Dir: p.Dir, Pairs: p.Pairs, Manifest: p.Manifest})
		if err != nil || !b.Committed {
			return ImportBegin{Staged: b.Staged, Clash: b.Clash}, err
		}
		row, err := h.row(p.Ref)
		return ImportBegin{Committed: &row, Source: b.Source}, err
	case MImportChunk:
		var p ImportChunkParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		off, err := migrate.Chunk(p.Migration, p.File, p.Off, p.Data)
		return ImportChunk{Off: off}, err
	case MImportCommit:
		var p ImportCommitParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.importCommit(p)
	case MImportAbort:
		var p ImportAbortParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return struct{}{}, migrate.Abort(p.Migration)
	}
	return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: method}
}

// session is ref's record here and the index it was found in; the copy stays the caller's (work outside mu).
func (h *localHandler) session(ref Ref) (*tend.Rec, *index.Index, error) {
	if ref.Provider == "" || ref.SessionID == "" {
		return nil, nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "ref"}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	r, err := h.lookup(ref, queryFresh)
	if err != nil {
		return nil, nil, err
	}
	cp := *r
	return &cp, h.idx, nil
}

// exportPlan answers what migrating a session here copies, whether it runs, and git in its directory; with a
// migration and a target it records the intent too, unless the session runs or that cannot be told.
func (h *localHandler) exportPlan(ctx context.Context, p ExportPlanParams) (ExportPlan, error) {
	r, idx, err := h.session(p.Ref)
	if err != nil {
		return ExportPlan{}, err
	}
	man, err := migrate.Plan(idx, r)
	if err != nil {
		return ExportPlan{}, err
	}
	plan := ExportPlan{Manifest: man, Cwd: r.Cwd, Repo: r.Repo}
	plan.Live, plan.Why = capture.LiveState(r.Provider, r.SessionID)
	if r.Cwd != "" {
		plan.Git, _ = envcheck.GitOf(ctx, r.Cwd)
	}
	switch {
	case p.Migration == "":
	case p.To != nil && plan.Live == capture.LiveNo:
		err = migrate.Intend(p.Migration, r, *p.To, man)
	case p.To == nil:
		err = migrate.Pending(p.Migration, r.Provider, r.SessionID)
	}
	return plan, err
}

// exportDone ends a migration here, its source; done with move puts the original in this machine's trash, unless
// it runs or that cannot be told.
func (h *localHandler) exportDone(p ExportDoneParams) (ExportDone, error) {
	rec, err := migrate.Finish(p.Migration, p.Provider, p.SessionID, p.State, p.Committed)
	if err != nil || p.State != migrate.StateDone || !p.Move || rec.Moved {
		return ExportDone{}, err
	}
	if state, _ := capture.LiveState(p.Provider, p.SessionID); state != capture.LiveNo {
		return ExportDone{}, &wire.Error{Code: wire.CodeBusy, Detail: "session " + p.SessionID}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	r, err := h.lookup(p.Ref, queryFresh)
	if err != nil {
		return ExportDone{}, err
	}
	n, next, err := migrate.MoveAway(h.store, h.idx, r, rec)
	if next != nil {
		h.idx = next
	}
	h.unfav = h.idx.Attach(h.store, h.unfav)
	return ExportDone{Trashed: n}, err
}

// importCommit puts a migration's staged files in place here and answers the session's row as listed now.
func (h *localHandler) importCommit(p ImportCommitParams) (ImportCommit, error) {
	s, err := tend.Open() // ⚠️ its own store: the commit runs outside mu
	if err != nil {
		return ImportCommit{}, err
	}
	c, err := migrate.Commit(s, p.Migration, p.Note, hello(h.version).End(), func(force map[string]bool) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		if err := h.load(queryFresh); err != nil {
			return err
		}
		next, err := h.idx.RescanSave(force)
		h.idx, h.indexed = next, time.Now()
		h.unfav = h.idx.Attach(h.store, h.unfav)
		return err
	})
	if err != nil {
		return ImportCommit{}, err
	}
	row, err := h.row(Ref{Provider: c.Record.Provider, SessionID: c.Record.SessionID})
	return ImportCommit{Row: row, Files: c.Files, Source: c.Record.Source, Unmapped: c.Unmapped}, err
}

// row is ref's row here, the index read again.
func (h *localHandler) row(ref Ref) (Row, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.load(0); err != nil {
		return Row{}, err
	}
	r, err := h.lookup(ref, 0)
	if err != nil {
		return Row{}, err
	}
	return rowOf(r, nil), nil
}

// Timeouts of a migration's calls: a plan and a commit read or write every file of the session.
const (
	migrateLong  = 10 * time.Minute
	migrateShort = 2 * time.Minute
)

func call(ctx context.Context, p Peer, d time.Duration, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return p.Call(ctx, method, params, out)
}

// MigrateError is why a migration stopped: Code is stable, never localized (a wire code, or one of the refusals
// below), Machine where it stopped, the text what to do.
type MigrateError struct {
	Code    string
	Machine string
	text    string
	err     error
}

func (e *MigrateError) Error() string { return e.text }
func (e *MigrateError) Unwrap() error { return e.err }

// Refusals of a migration, as MigrateError codes.
const (
	RefusedSameMachine = "same_machine"
	RefusedRunning     = "running"
	RefusedLiveUnknown = "live_unknown"
	RefusedSame        = "same"
	RefusedThere       = "there"
	RefusedDiverged    = "diverged"
	RefusedExists      = "exists"
	RefusedNoDir       = "no_dir"
	RefusedEnvUnread   = "env_unread"
	RefusedBlocked     = "blocked"
	RefusedOpened      = "opened"
	RefusedChanged     = "changed"
	RefusedUnrecorded  = "unrecorded"
	RefusedNotMoved    = "not_moved"
	RefusedCommitted   = "committed"
)

func refused(code, machine, text string) error {
	return &MigrateError{Code: code, Machine: machine, text: text}
}

// failed is err from method on p as a MigrateError naming p; nil stays nil.
func failed(p Peer, method string, err error) error {
	var me *MigrateError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &me):
		return err
	case wire.Code(err) == wire.CodeUnknownMethod:
		return &MigrateError{Code: wire.CodeUnknownMethod, Machine: p.Label(), text: TooOld(p.Label(), method), err: err}
	}
	return &MigrateError{Code: cmp.Or(wire.Code(err), "error"), Machine: p.Label(), text: i18n.F("remote.migrate.failed", p.Label(), method, errText(err)), err: err}
}

// errText is err in words: a wire error's reason and detail, another error as it is.
func errText(err error) string {
	var e *wire.Error
	if !errors.As(err, &e) {
		return err.Error()
	}
	if e.Detail == "" {
		return Reason(err)
	}
	return Reason(err) + " (" + e.Detail + ")"
}

var liveWhyKeys = map[string]string{
	capture.WhyNoSessionsDir: "remote.migrate.why_no_sessions_dir",
	capture.WhyUnreadable:    "remote.migrate.why_unreadable",
	capture.WhyHerdr:         "remote.migrate.why_herdr",
}

// Copy states CheckCopies concludes for a done migration.
const (
	CopySame       = "same"       // neither machine's copy moved on since
	CopyHere       = "here"       // only the machine asked went on
	CopyThere      = "there"      // only the other one did
	CopyDiverged   = "diverged"   // both did: forked
	CopyMoved      = "moved"      // the source's original went to its trash: one copy left
	CopySuperseded = "superseded" // a later done migration between the same two machines stands instead
	CopyUnchecked  = "unchecked"  // the other machine did not answer or has no record, or a side cannot tell
)

// CopyState is one of a session's migrations as a machine recorded it and, for a done one, how both copies stand.
type CopyState struct {
	Copy
	Status string // a Copy* state; "" for a pending or aborted migration
	Why    string // unchecked: why, in words
}

// StatusText is s's status in words, naming the machine it went on at.
func (s CopyState) StatusText() string {
	switch s.Status {
	case CopySame:
		return i18n.T("remote.copies.same")
	case CopyHere:
		return i18n.T("remote.copies.here")
	case CopyThere:
		return i18n.F("remote.copies.there", s.Peer.Name)
	case CopyDiverged:
		return i18n.T("remote.copies.diverged")
	case CopyMoved:
		return i18n.T("remote.copies.moved")
	case CopySuperseded:
		return i18n.T("remote.copies.superseded")
	case CopyUnchecked:
		return i18n.F("remote.copies.unchecked", s.Why)
	}
	return ""
}

// CheckCopies are ref's migrations recorded on at, newest first, each done one with how both copies stand: other finds
// the machine at its other end (an error leaves it unchecked). Only the latest done one with each other machine is
// judged (each other machine asked once), the earlier ones are superseded.
func CheckCopies(ctx context.Context, at Peer, ref Ref, other func(PeerRef) (Peer, error)) ([]CopyState, error) {
	var mine Copies
	if err := call(ctx, at, migrateShort, MCopies, ref, &mine); err != nil {
		return nil, failed(at, MCopies, err)
	}
	asked := map[string]bool{}
	out := make([]CopyState, len(mine.Copies))
	for i, c := range mine.Copies {
		out[i].Copy = c
		if c.State != migrate.StateDone {
			continue
		}
		key := cmp.Or(c.Peer.Endpoint, c.Peer.Name)
		if asked[key] {
			out[i].Status = CopySuperseded
			continue
		}
		asked[key] = true
		var theirs Copies
		p, err := other(c.Peer)
		if err == nil {
			err = call(ctx, p, migrateShort, MCopies, ref, &theirs)
		}
		out[i].Status, out[i].Why = judge(c, theirs.Copies, err)
	}
	return out, nil
}

// judge is how mine, a done migration, stands against theirs, the other machine's records of the session.
func judge(mine Copy, theirs []Copy, err error) (string, string) {
	if err != nil {
		return CopyUnchecked, Reason(err)
	}
	i := slices.IndexFunc(theirs, func(c Copy) bool { return c.Migration == mine.Migration })
	switch {
	case mine.Moved || i >= 0 && theirs[i].Moved:
		return CopyMoved, ""
	case i < 0:
		return CopyUnchecked, i18n.T("remote.copies.no_record")
	case mine.Changed == nil || theirs[i].Changed == nil:
		return CopyUnchecked, i18n.T("remote.copies.unreadable")
	}
	here, there := *mine.Changed, *theirs[i].Changed
	switch {
	case here && there:
		return CopyDiverged, ""
	case here:
		return CopyHere, ""
	case there:
		return CopyThere, ""
	}
	return CopySame, ""
}

// Migration is a Claude session on From copied whole to To: what From plans to send, and where earlier migrations of
// it between the two stand.
type Migration struct {
	*Handover
	ID       string     // a pending migration of the session to To going on; else Run makes one
	Plan     ExportPlan // as From answered first
	Resumed  bool       // ID is a pending migration's
	MoveOnly bool       // migrated before and neither side went on since: with move, only the move is left
	Last     *CopyState // the latest done migration between From and To, as From recorded it
	Report   *envcheck.Report
}

// StartMigration plans migrating ref from from to to. It refuses while the session runs on from or that cannot be
// told, and when the latest migration between the two left both copies the same (unless only the move is left), To
// went on with its copy, or both did. A pending migration of the session to to goes on.
func StartMigration(ctx context.Context, from, to Peer, ref Ref, move bool) (*Migration, error) {
	if from.Same(to) {
		return nil, refused(RefusedSameMachine, to.Label(), i18n.F("remote.migrate.same_machine", to.Label()))
	}
	if !to.Has(MImportBegin) {
		return nil, failed(to, MImportBegin, &wire.Error{Code: wire.CodeUnknownMethod, Detail: to.Hello.Version})
	}
	x, err := Handoff(ctx, from, to, ref)
	if err != nil {
		return nil, failed(from, MHandoffFacts, err)
	}
	m := &Migration{Handover: x}
	if err := call(ctx, from, migrateLong, MExportPlan, ExportPlanParams{Ref: ref}, &m.Plan); err != nil {
		return nil, failed(from, MExportPlan, err)
	}
	if err := m.idle(m.Plan); err != nil {
		return nil, err
	}
	states, err := CheckCopies(ctx, from, ref, func(p PeerRef) (Peer, error) {
		if p.Endpoint != "" && p.Endpoint == to.Hello.Endpoint {
			return to, nil
		}
		return Peer{}, &wire.Error{Code: wire.CodeNotFound, Detail: p.Name}
	})
	if err != nil {
		return nil, err
	}
	for _, s := range states {
		if s.Peer.Endpoint != to.Hello.Endpoint {
			continue
		}
		if s.State == migrate.StatePending && s.Role == migrate.RoleTo && m.ID == "" {
			m.ID, m.Resumed = s.Migration, true
		}
		if s.State == migrate.StateDone {
			m.Last = &s
			break
		}
	}
	if m.Last == nil {
		return m, nil
	}
	switch m.Last.Status {
	case CopySame:
		if move && m.Last.Role == migrate.RoleTo {
			m.ID, m.Resumed, m.MoveOnly = m.Last.Migration, false, true
			return m, nil
		}
		return nil, refused(RefusedSame, to.Label(), i18n.F("remote.migrate.same", to.Label(), render.WhenFull(m.Last.At.Local())))
	case CopyThere:
		return nil, refused(RefusedThere, to.Label(), i18n.F("remote.migrate.there", to.Label(), to.Label()))
	case CopyDiverged:
		return nil, refused(RefusedDiverged, to.Label(), i18n.F("remote.migrate.diverged", from.Label(), to.Label()))
	}
	return m, nil
}

// idle refuses a plan whose session runs on From, or of which that cannot be told.
func (m *Migration) idle(p ExportPlan) error {
	switch p.Live {
	case capture.LiveNo:
		return nil
	case capture.LiveYes:
		return refused(RefusedRunning, m.From.Label(), i18n.F("remote.migrate.running", m.From.Label()))
	}
	why := i18n.T("remote.err.other")
	if k, ok := liveWhyKeys[p.Why]; ok {
		why = i18n.T(k)
	}
	return refused(RefusedLiveUnknown, m.From.Label(), i18n.F("remote.migrate.live_unknown", m.From.Label(), why))
}

// Check compares the session's environment with dir's on To (Handover.Diagnose) and keeps the report: one that
// blocks stops Run.
func (m *Migration) Check(ctx context.Context, dir string) (envcheck.Report, error) {
	rep, err := m.Diagnose(ctx, dir)
	if err == nil {
		m.Report = &rep
	}
	return rep, err
}

// Transfer is how far a migration's transfer went.
type Transfer struct {
	Files, FilesDone int
	Bytes, BytesDone int64
}

type MigrateOptions struct {
	Dir      string    // the session's directory on To
	Pairs    []DirPair // mappings the rewrite tries after the session's directory to Dir
	Move     bool      // once To committed, the original goes to From's trash
	Progress func(Transfer)
}

// Migrated is what a migration did.
type Migrated struct {
	ID       string
	Row      *Row // the session as To lists it now; nil when only the move was left
	Files    int
	Bytes    int64 // of the session
	Sent     int64 // this run; staged bytes are not sent again
	Unmapped []string
	Trashed  int  // files of the original now in From's trash
	Behind   bool // To finished an earlier run's commit and From went on since: the next run carries the rest
}

// Run migrates the session into dir on To: From records the intent, To stages every file, From is asked again
// whether anything changed (a changed file is sent again once), To commits, From records it done and, with move,
// trashes the original. Any step that fails leaves the original as it was and what was staged for the next run.
func (m *Migration) Run(ctx context.Context, o MigrateOptions) (Migrated, error) {
	res := Migrated{ID: m.ID}
	if m.MoveOnly {
		return m.finish(ctx, res, true, nil)
	}
	if !pathmap.Abs(o.Dir) {
		return res, &wire.Error{Code: wire.CodeBadRequest, Detail: "dir"}
	}
	if m.Report == nil {
		if _, err := m.Check(ctx, o.Dir); err != nil {
			return res, refused(RefusedEnvUnread, m.To.Label(), i18n.F("remote.migrate.env_unread", errText(err)))
		}
	}
	if m.Report.Blocked() {
		return res, refused(RefusedBlocked, m.To.Label(), i18n.F("remote.migrate.blocked", m.Report.Block, m.To.Label()))
	}
	if m.ID == "" {
		m.ID = migrate.NewID(time.Now())
	}
	res.ID = m.ID
	pairs := []DirPair{{From: m.Plan.Cwd, To: o.Dir}}
	for _, p := range o.Pairs {
		if !slices.Contains(pairs, p) {
			pairs = append(pairs, p)
		}
	}
	note := m.Note(o.Dir, pairs)
	to := m.To.Ref()
	for try := 0; ; try++ {
		var plan ExportPlan
		if err := call(ctx, m.From, migrateLong, MExportPlan, ExportPlanParams{Ref: m.Ref, Migration: m.ID, To: &to}, &plan); err != nil {
			return res, failed(m.From, MExportPlan, err)
		}
		if err := m.idle(plan); err != nil {
			return res, err
		}
		m.Plan = plan
		res.Files, res.Bytes = len(plan.Manifest.Files), plan.Manifest.Size()
		var b ImportBegin
		err := call(ctx, m.To, migrateShort, MImportBegin, ImportBeginParams{Migration: m.ID, From: m.From.Ref(), Ref: m.Ref,
			Title: m.Facts.Title, Cwd: plan.Cwd, Dir: o.Dir, Pairs: pairs, Manifest: plan.Manifest}, &b)
		switch {
		case wire.Code(err) == wire.CodeNotFound:
			return res, refused(RefusedNoDir, m.To.Label(), i18n.F("remote.migrate.no_dir", m.To.Label(), o.Dir))
		case err != nil:
			return res, failed(m.To, MImportBegin, err)
		case b.Committed != nil:
			res.Row = b.Committed
			return m.finish(ctx, res, o.Move, b.Source)
		case b.Clash == migrate.ClashExists || b.Clash == migrate.ClashDiverged:
			call(ctx, m.From, migrateShort, MExportDone, ExportDoneParams{Ref: m.Ref, Migration: m.ID, State: migrate.StateAborted}, nil)
			if b.Clash == migrate.ClashDiverged {
				return res, refused(RefusedDiverged, m.To.Label(), i18n.F("remote.migrate.diverged", m.From.Label(), m.To.Label()))
			}
			return res, refused(RefusedExists, m.To.Label(), i18n.F("remote.migrate.exists", m.To.Label(), m.From.Label()))
		}
		err = m.send(ctx, plan.Manifest, b.Staged, o.Progress, &res.Sent)
		if err == nil {
			var again ExportPlan
			if err := call(ctx, m.From, migrateLong, MExportPlan, ExportPlanParams{Ref: m.Ref, Migration: m.ID}, &again); err != nil {
				return res, failed(m.From, MExportPlan, err)
			}
			if again.Live == capture.LiveYes {
				return res, refused(RefusedOpened, m.From.Label(), i18n.F("remote.migrate.opened", m.From.Label()))
			}
			if err := m.idle(again); err != nil {
				return res, err
			}
			if sameFiles(plan.Manifest, again.Manifest) {
				break
			}
			err = &wire.Error{Code: wire.CodeStale}
		}
		switch code := wire.Code(err); {
		case code != wire.CodeStale && code != wire.CodeConflict:
			return res, err
		case try > 0:
			return res, refused(RefusedChanged, m.From.Label(), i18n.F("remote.migrate.changed", m.From.Label()))
		}
	}
	var c ImportCommit
	if err := call(ctx, m.To, migrateLong, MImportCommit, ImportCommitParams{Migration: m.ID, Note: note}, &c); err != nil {
		return res, failed(m.To, MImportCommit, err)
	}
	proc.CrashAt("migrate.committed")
	res.Row, res.Files, res.Unmapped = &c.Row, c.Files, c.Unmapped
	return m.finish(ctx, res, o.Move, c.Source)
}

// send moves every byte of man To has not staged, a chunk at a time, and counts it in sent.
func (m *Migration) send(ctx context.Context, man migrate.Manifest, staged map[string]int64, progress func(Transfer), sent *int64) error {
	p := Transfer{Files: len(man.Files), Bytes: man.Size()}
	tell := func() {
		if progress != nil {
			progress(p)
		}
	}
	for _, f := range man.Files {
		if off := min(staged[f.Path], f.Size); off == f.Size {
			p.FilesDone++
			p.BytesDone += off
		}
	}
	tell()
	for _, f := range man.Files {
		off := staged[f.Path]
		if off >= f.Size {
			continue
		}
		p.BytesDone += off
		for off < f.Size {
			var chunk ExportChunk
			if err := call(ctx, m.From, migrateShort, MExportRead, ExportReadParams{Ref: m.Ref, Migration: m.ID, File: f.Path, Off: off,
				N: int(min(migrate.ChunkSize, f.Size-off)), ID: f.ID}, &chunk); err != nil {
				return failed(m.From, MExportRead, err)
			}
			if len(chunk.Data) == 0 {
				return &wire.Error{Code: wire.CodeStale, Detail: f.Path}
			}
			var got ImportChunk
			if err := call(ctx, m.To, migrateShort, MImportChunk, ImportChunkParams{Migration: m.ID, File: f.Path, Off: off, Data: chunk.Data}, &got); err != nil {
				return failed(m.To, MImportChunk, err)
			}
			*sent += got.Off - off
			p.BytesDone += got.Off - off
			off = got.Off
			tell()
		}
		p.FilesDone++
		tell()
	}
	return nil
}

// sameFiles: a and b list the same files with the same identity, size and sha.
func sameFiles(a, b migrate.Manifest) bool {
	return slices.EqualFunc(a.Files, b.Files, func(x, y migrate.File) bool {
		return x.Path == y.Path && x.ID == y.ID && x.Size == y.Size && x.SHA == y.SHA
	})
}

// finish has From record the migration done with what To committed of the main transcript and, with move, trash the
// original; when To committed an earlier plan than the one From is at now (Behind), the original stays.
func (m *Migration) finish(ctx context.Context, res Migrated, move bool, committed *migrate.Sum) (Migrated, error) {
	if main, ok := m.Plan.Manifest.Main(m.Ref.SessionID); committed != nil && ok && main.SHA != committed.SHA {
		res.Behind, move = true, false
	}
	var d ExportDone
	err := call(ctx, m.From, migrateShort, MExportDone, ExportDoneParams{Ref: m.Ref, Migration: m.ID, State: migrate.StateDone, Move: move,
		Committed: committed}, &d)
	res.Trashed = d.Trashed
	switch {
	case err == nil:
		return res, nil
	case wire.Code(err) == wire.CodeBusy:
		return res, refused(RefusedNotMoved, m.From.Label(), i18n.F("remote.migrate.not_moved", m.From.Label()))
	}
	return res, &MigrateError{Code: RefusedUnrecorded, Machine: m.From.Label(), err: err,
		text: i18n.F("remote.migrate.unrecorded", m.To.Label(), m.From.Label(), errText(err))}
}

// PendingMigrations are ref's migrations pending on from, its source, newest first: each goes on with the next
// migration to its machine, or Abandon drops it.
func PendingMigrations(ctx context.Context, from Peer, ref Ref) ([]Copy, error) {
	var cs Copies
	if err := call(ctx, from, migrateShort, MCopies, ref, &cs); err != nil {
		return nil, failed(from, MCopies, err)
	}
	var out []Copy
	for _, c := range cs.Copies {
		if c.Role == migrate.RoleTo && c.State == migrate.StatePending {
			out = append(out, c)
		}
	}
	return out, nil
}

// Abandon drops migration id of ref, pending on from: to, when given, drops its staging and takes back what a commit
// in progress put in place, then from records it aborted. A migration to committed is not dropped: running it again
// finishes it. Without to (unreachable), to's staging goes after migrate.StaleAfter.
func Abandon(ctx context.Context, from Peer, to *Peer, ref Ref, id string) error {
	if to != nil {
		err := call(ctx, *to, migrateShort, MImportAbort, ImportAbortParams{Migration: id}, nil)
		switch {
		case wire.Code(err) == wire.CodeConflict:
			return refused(RefusedCommitted, to.Label(), i18n.F("remote.migrate.committed", to.Label()))
		case err != nil:
			return failed(*to, MImportAbort, err)
		}
	}
	return failed(from, MExportDone, call(ctx, from, migrateShort, MExportDone, ExportDoneParams{Ref: ref, Migration: id, State: migrate.StateAborted}, nil))
}

// Note is the migration note the first resume on To starts with, in this end's language: where the session came
// from, the directories mapped, what git on From had that the files do not carry, what stayed behind, how the
// environment differs, and the memories. To adds the cwd values nothing mapped.
func (m *Migration) Note(dir string, pairs []DirPair) string {
	var b strings.Builder
	section := func(key string) { b.WriteString("\n## " + i18n.T(key) + "\n\n") }
	b.WriteString("# " + i18n.T("migrate.note.title") + "\n\n")
	b.WriteString(i18n.F("migrate.note.from", m.From.Label(), m.Plan.Cwd, dir, render.WhenFull(time.Now())) + "\n")
	section("migrate.note.dirs")
	for _, p := range pairs {
		b.WriteString("- " + p.From + " -> " + p.To + "\n")
	}
	if g := m.Plan.Git; g.Dirty > 0 || g.Unpushed > 0 {
		section("migrate.note.git")
		if g.Dirty > 0 {
			b.WriteString(i18n.F("migrate.note.dirty", g.Dirty, m.From.Label()) + "\n")
			for _, f := range g.DirtyList[:min(len(g.DirtyList), 10)] {
				b.WriteString("- " + f + "\n")
			}
		}
		if g.Unpushed > 0 {
			b.WriteString(i18n.F("migrate.note.unpushed", g.Unpushed, m.From.Label()) + "\n")
		}
	}
	if len(m.Plan.Manifest.Left) > 0 {
		section("migrate.note.left")
		for _, p := range m.Plan.Manifest.Left {
			b.WriteString("- " + p + "\n")
		}
	}
	if m.Report != nil && len(m.Report.Items) > 0 {
		section("migrate.note.env")
		b.WriteString(m.Report.Summary() + "\n")
		for _, it := range m.Report.Items {
			if it.Level != envcheck.LevelHint {
				b.WriteString("- " + envcheck.LevelText(it.Level) + ": " + it.What + "\n")
			}
		}
	}
	section("migrate.note.memory")
	b.WriteString(i18n.F("migrate.note.memory_diff", dir, m.From.Label()) + "\n")
	return b.String()
}
