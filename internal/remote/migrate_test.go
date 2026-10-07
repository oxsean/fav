package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/migrate"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/testkit"
	"github.com/oxsean/fav/internal/wire"
)

// box is one machine of a migration in this process: its own homes and a handler answering under them, reached
// through a peer whose calls switch to them first. fault, when set, may answer a call instead.
type box struct {
	t                              *testing.T
	name, claude, data, home, root string
	h                              Handler
	fault                          func(method string, params any) error
}

func newBox(t *testing.T, name string) *box {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &box{t: t, name: name, root: root, claude: filepath.Join(root, "claude"), data: filepath.Join(root, "tend"), home: filepath.Join(root, "home"),
		h: NewLocal("test")}
	for _, d := range []string{filepath.Join(b.claude, "sessions"), b.data, b.home, filepath.Join(root, "codex")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func (b *box) on() {
	b.t.Setenv("CLAUDE_CONFIG_DIR", b.claude)
	b.t.Setenv("TEND_HOME", b.data)
	b.t.Setenv("CODEX_HOME", filepath.Join(b.root, "codex"))
	b.t.Setenv("HOME", b.home)
	b.t.Setenv("USERPROFILE", b.home)
}

func (b *box) peer() Peer {
	b.on()
	return PeerOf(b.name, LocalHello("test"), func(ctx context.Context, method string, params, out any) error {
		if b.fault != nil {
			if err := b.fault(method, params); err != nil {
				return err
			}
		}
		b.on()
		return InProcess(b.h)(ctx, method, params, out)
	})
}

func (b *box) dir(parts ...string) string {
	d := filepath.Join(append([]string{b.home, "dev"}, parts...)...)
	if err := os.MkdirAll(d, 0o755); err != nil {
		b.t.Fatal(err)
	}
	return d
}

// session writes a Claude session sid in cwd with a sub-agent transcript, a file-history entry and a todo.
func (b *box) session(sid, cwd string) {
	b.t.Helper()
	l := fixture.NewLiveClaude(b.claude, sid, cwd, "cli")
	for _, s := range []string{"one", "two", "three"} {
		if l.User("ask "+s) != nil || l.Reply("answer "+s) != nil {
			b.t.Fatal("transcript")
		}
	}
	files := map[string]string{
		filepath.Join(filepath.Dir(l.Path()), sid, "subagents", "agent-1.jsonl"): `{"type":"user","cwd":` + testkit.JSONString(cwd) + "}\n",
		filepath.Join(b.claude, "file-history", sid, "abc@v1"):                   "before the edit\n",
		filepath.Join(b.claude, "todos", sid+"-agent-"+sid+".json"):              "[]",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			b.t.Fatal(err)
		}
	}
}

// transcript is sid's own transcript here for a session in dir.
func (b *box) transcript(sid, dir string) string {
	return filepath.Join(b.claude, "projects", index.ClaudeProjectName(dir), sid+".jsonl")
}

// goOn appends a line to sid's transcript for a session in dir, as a resume there would.
func (b *box) goOn(sid, dir, text string) {
	b.t.Helper()
	f, err := os.OpenFile(b.transcript(sid, dir), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		b.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(`{"type":"user","cwd":` + testkit.JSONString(dir) + `,"sessionId":"` + sid + `","message":{"role":"user","content":"` + text + `"}}` + "\n"); err != nil {
		b.t.Fatal(err)
	}
}

func (b *box) records() []migrate.Record {
	b.on()
	recs, err := migrate.Records()
	if err != nil {
		b.t.Fatal(err)
	}
	return recs
}

const msid = "5e551011-0c1a-4de0-8000-0000000000aa"

var mref = Ref{Provider: tend.ProviderClaude, SessionID: msid}

func refusedAs(err error) string {
	var me *MigrateError
	if errors.As(err, &me) {
		return me.Code
	}
	return "not a refusal: " + code(err)
}

// pair is a session in src's webapp and an empty webapp directory on dst.
func pair(t *testing.T) (src, dst *box, cwd, dir string) {
	src, dst = newBox(t, "src"), newBox(t, "dst")
	cwd, dir = src.dir("webapp"), dst.dir("site", "webapp")
	src.session(msid, cwd)
	return src, dst, cwd, dir
}

func migrateOnce(t *testing.T, from, to *box, dir string, move bool) Migrated {
	t.Helper()
	ctx := context.Background()
	m, err := StartMigration(ctx, from.peer(), to.peer(), mref, move)
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Run(ctx, MigrateOptions{Dir: dir, Move: move})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The driver copies a session through both machines' methods: the target lists it in its new directory with the cwd
// carried over and keeps the note, both ends record it, and both copies stand the same. Migrating it again is
// refused; asked to move, only the move is left, and the original goes to the source's trash.
func TestMigrateCopiesThenRefusesThenMoves(t *testing.T) {
	src, dst, cwd, dir := pair(t)
	ctx := context.Background()
	m, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
	if err != nil || m.ID != "" || m.Last != nil || len(m.Plan.Manifest.Files) != 4 || m.Plan.Cwd != cwd {
		t.Fatalf("start: %+v %v", m, err)
	}
	var seen []Transfer
	res, err := m.Run(ctx, MigrateOptions{Dir: dir, Progress: func(p Transfer) { seen = append(seen, p) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Row == nil || res.Row.SessionID != msid || res.Files != 4 || res.Sent != res.Bytes || res.Bytes == 0 {
		t.Fatalf("migrated: %+v", res)
	}
	if last := seen[len(seen)-1]; last.BytesDone != res.Bytes || last.FilesDone != 4 {
		t.Errorf("progress: %+v", seen)
	}
	text, err := os.ReadFile(dst.transcript(msid, dir))
	if err != nil || !strings.Contains(string(text), testkit.JSONString(dir)) || strings.Contains(string(text), testkit.JSONString(cwd)) {
		t.Fatalf("the copy runs in its new directory: %s %v", text, err)
	}
	srcRecs, dstRecs := src.records(), dst.records()
	if len(srcRecs) != 1 || srcRecs[0].Role != migrate.RoleTo || srcRecs[0].State != migrate.StateDone || srcRecs[0].Peer.Name != "dst" ||
		len(dstRecs) != 1 || dstRecs[0].Role != migrate.RoleFrom || dstRecs[0].Peer.Name != "src" || dstRecs[0].Dir != dir {
		t.Fatalf("records: %+v | %+v", srcRecs, dstRecs)
	}
	note, err := os.ReadFile(dstRecs[0].Note)
	if err != nil || !strings.Contains(string(note), i18n.T("migrate.note.title")) || !strings.Contains(string(note), cwd+" -> "+dir) {
		t.Errorf("note: %s %v", note, err)
	}
	dst.on()
	if prompt, _ := migrate.FirstResume(tend.ProviderClaude, msid); !strings.Contains(prompt, dstRecs[0].Note) {
		t.Errorf("the first resume there reads the note: %q", prompt)
	}

	states, err := CheckCopies(ctx, src.peer(), mref, func(p PeerRef) (Peer, error) { return dst.peer(), nil })
	if err != nil || len(states) != 1 || states[0].Status != CopySame {
		t.Fatalf("copies: %+v %v", states, err)
	}
	if _, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false); refusedAs(err) != RefusedSame {
		t.Fatalf("again: %v", err)
	}
	m, err = StartMigration(ctx, src.peer(), dst.peer(), mref, true)
	if err != nil || !m.MoveOnly || m.ID != srcRecs[0].Migration {
		t.Fatalf("only the move is left: %+v %v", m, err)
	}
	if res, err := m.Run(ctx, MigrateOptions{Move: true}); err != nil || res.Trashed != 4 || res.Row != nil {
		t.Fatalf("moved: %+v %v", res, err)
	}
	if _, err := os.Stat(src.transcript(msid, cwd)); !os.IsNotExist(err) {
		t.Errorf("the original is in the trash: %v", err)
	}
	if recs := src.records(); !recs[0].Moved {
		t.Errorf("recorded moved: %+v", recs)
	}
}

// A transfer that breaks goes on from what the target staged; abandoned instead, nothing of it is left there and a
// new migration starts over.
func TestMigrateResumesOrAbandons(t *testing.T) {
	for _, abandon := range []bool{false, true} {
		src, dst, _, dir := pair(t)
		ctx := context.Background()
		chunks := 0
		dst.fault = func(method string, _ any) error {
			if method == MImportChunk {
				if chunks++; chunks == 3 {
					return &wire.Error{Code: wire.CodeClosed}
				}
			}
			return nil
		}
		m, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Run(ctx, MigrateOptions{Dir: dir}); refusedAs(err) != wire.CodeClosed {
			t.Fatalf("broken: %v", err)
		}
		if recs := src.records(); len(recs) != 1 || recs[0].State != migrate.StatePending {
			t.Fatalf("the source waits: %+v", recs)
		}
		dst.fault = nil
		if abandon {
			to := dst.peer()
			if err := Abandon(ctx, src.peer(), &to, mref, m.ID); err != nil {
				t.Fatal(err)
			}
			if recs := src.records(); recs[0].State != migrate.StateAborted {
				t.Fatalf("aborted: %+v", recs)
			}
			if _, err := os.Stat(filepath.Join(dst.data, "import", m.ID)); !os.IsNotExist(err) {
				t.Fatalf("the staging went: %v", err)
			}
			again, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
			if err != nil || again.Resumed || again.ID != "" {
				t.Fatalf("a new one: %+v %v", again, err)
			}
			continue
		}
		again, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
		if err != nil || !again.Resumed || again.ID != m.ID {
			t.Fatalf("goes on: %+v %v", again, err)
		}
		res, err := again.Run(ctx, MigrateOptions{Dir: dir})
		if err != nil || res.ID != m.ID || res.Sent == 0 || res.Sent >= res.Bytes {
			t.Fatalf("only what was missing is sent: %+v %v", res, err)
		}
	}
}

// A source that changed during the transfer is sent again once; one that keeps changing is refused, still pending.
func TestMigrateRetriesOnceWhenTheSourceChanges(t *testing.T) {
	for _, times := range []int{1, 2} {
		src, dst, cwd, dir := pair(t)
		changed := 0
		src.fault = func(method string, params any) error {
			if p, ok := params.(ExportPlanParams); ok && p.Migration != "" && p.To == nil && changed < times {
				changed++
				src.goOn(msid, cwd, "meanwhile")
			}
			return nil
		}
		ctx := context.Background()
		m, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
		if err != nil {
			t.Fatal(err)
		}
		res, err := m.Run(ctx, MigrateOptions{Dir: dir})
		if times == 2 {
			if refusedAs(err) != RefusedChanged || src.records()[0].State != migrate.StatePending {
				t.Fatalf("keeps changing: %v %+v", err, src.records())
			}
			continue
		}
		text, _ := os.ReadFile(dst.transcript(msid, dir))
		if err != nil || res.Row == nil || !strings.Contains(string(text), "meanwhile") {
			t.Fatalf("sent again: %+v %v\n%s", res, err, text)
		}
	}
}

// The source grows while its file is sent: the target holds the planned bytes, the check after the transfer finds the
// file changed, and the run goes again with the new plan.
func TestMigrateRetriesWhenTheSourceGrowsMidTransfer(t *testing.T) {
	src, dst, cwd, dir := pair(t)
	grown := false
	src.fault = func(method string, _ any) error {
		if method == MExportRead && !grown {
			grown = true
			src.goOn(msid, cwd, "meanwhile")
		}
		return nil
	}
	ctx := context.Background()
	m, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Run(ctx, MigrateOptions{Dir: dir})
	text, _ := os.ReadFile(dst.transcript(msid, dir))
	if err != nil || res.Row == nil || !strings.Contains(string(text), "meanwhile") {
		t.Fatalf("sent again: %+v %v\n%s", res, err, text)
	}
}

// The target committed, the source did not record it and went on: the next run finishes that commit, says it is
// behind and keeps the original even with move, and the source records what the target holds, so the copies read as
// gone on here and the run after that carries the rest over.
func TestMigrateRecordsWhatTheTargetCommitted(t *testing.T) {
	src, dst, cwd, dir := pair(t)
	ctx := context.Background()
	src.fault = func(method string, _ any) error {
		if method == MExportDone {
			return &wire.Error{Code: wire.CodeClosed}
		}
		return nil
	}
	m, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Run(ctx, MigrateOptions{Dir: dir}); refusedAs(err) != RefusedUnrecorded {
		t.Fatalf("unrecorded: %v", err)
	}
	src.fault = nil
	src.goOn(msid, cwd, "meanwhile")
	again, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
	if err != nil || !again.Resumed {
		t.Fatalf("goes on: %v", err)
	}
	if res, err := again.Run(ctx, MigrateOptions{Dir: dir, Move: true}); err != nil || res.Row == nil || !res.Behind || res.Trashed != 0 {
		t.Fatalf("finished, behind the source, the original kept: %+v %v", res, err)
	}
	if _, err := os.Stat(src.transcript(msid, cwd)); err != nil {
		t.Fatalf("the newer original stays: %v", err)
	}
	if text, _ := os.ReadFile(dst.transcript(msid, dir)); strings.Contains(string(text), "meanwhile") {
		t.Fatalf("the target holds what it committed before:\n%s", text)
	}
	states, err := CheckCopies(ctx, src.peer(), mref, func(PeerRef) (Peer, error) { return dst.peer(), nil })
	if err != nil || len(states) != 1 || states[0].Status != CopyHere {
		t.Fatalf("gone on here since: %+v %v", states, err)
	}
	if mv, err := StartMigration(ctx, src.peer(), dst.peer(), mref, true); err != nil || mv.MoveOnly {
		t.Fatalf("--move migrates again before it moves: %+v %v", mv, err)
	}
	last, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := last.Run(ctx, MigrateOptions{Dir: dir}); err != nil || res.Row == nil || res.Behind {
		t.Fatalf("forward: %+v %v", res, err)
	}
	if text, _ := os.ReadFile(dst.transcript(msid, dir)); !strings.Contains(string(text), "meanwhile") {
		t.Fatalf("carried over:\n%s", text)
	}
}

// A commit the source did not record is finished by the next run: the target answers it committed.
func TestMigrateFinishesACommitTheSourceMissed(t *testing.T) {
	src, dst, _, dir := pair(t)
	ctx := context.Background()
	src.fault = func(method string, _ any) error {
		if method == MExportDone {
			return &wire.Error{Code: wire.CodeClosed}
		}
		return nil
	}
	m, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Run(ctx, MigrateOptions{Dir: dir}); refusedAs(err) != RefusedUnrecorded {
		t.Fatalf("unrecorded: %v", err)
	}
	if recs := dst.records(); len(recs) != 1 || recs[0].State != migrate.StateDone {
		t.Fatalf("committed there: %+v", recs)
	}
	src.fault = nil
	again, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
	if err != nil || !again.Resumed {
		t.Fatalf("%+v %v", again, err)
	}
	res, err := again.Run(ctx, MigrateOptions{Dir: dir})
	if err != nil || res.Row == nil || res.Sent != 0 || src.records()[0].State != migrate.StateDone {
		t.Fatalf("finished: %+v %v %+v", res, err, src.records())
	}
}

// What a migration refuses: its own machine, a target too old, a session whose running cannot be told, the same id
// already on the target, a copy the target went on with, and two that forked.
func TestMigrateRefuses(t *testing.T) {
	ctx := context.Background()
	src, dst, cwd, dir := pair(t)
	if _, err := StartMigration(ctx, src.peer(), src.peer(), mref, false); refusedAs(err) != RefusedSameMachine {
		t.Errorf("same machine: %v", err)
	}
	old := PeerOf("old", Hello{Version: "v0.0.1", Endpoint: "e-old", Methods: []string{MHello, MList}}, nil)
	if _, err := StartMigration(ctx, src.peer(), old, mref, false); refusedAs(err) != wire.CodeUnknownMethod || !strings.Contains(err.Error(), "old") {
		t.Errorf("too old: %v", err)
	}
	os.RemoveAll(filepath.Join(src.claude, "sessions"))
	if _, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false); refusedAs(err) != RefusedLiveUnknown {
		t.Errorf("live unknown: %v", err)
	}
	os.MkdirAll(filepath.Join(src.claude, "sessions"), 0o755)

	dst.session(msid, dir)
	m, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Run(ctx, MigrateOptions{Dir: dir}); refusedAs(err) != RefusedExists {
		t.Fatalf("exists: %v", err)
	}
	if recs := src.records(); recs[0].State != migrate.StateAborted {
		t.Errorf("the source records it aborted: %+v", recs)
	}

	src, dst, cwd, dir = pair(t)
	migrateOnce(t, src, dst, dir, false)
	dst.goOn(msid, dir, "went on there")
	if _, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false); refusedAs(err) != RefusedThere {
		t.Errorf("went on there: %v", err)
	}
	src.goOn(msid, cwd, "and here")
	if _, err := StartMigration(ctx, src.peer(), dst.peer(), mref, false); refusedAs(err) != RefusedDiverged {
		t.Errorf("forked: %v", err)
	}
}

// Migrating back the copy that went on replaces the unchanged original, which waits in the trash; going on at the
// source then migrating again replaces the target's unchanged copy the same way.
func TestMigrateBackAndForward(t *testing.T) {
	src, dst, cwd, dir := pair(t)
	migrateOnce(t, src, dst, dir, false)
	dst.goOn(msid, dir, "went on there")
	if res := migrateOnce(t, dst, src, cwd, false); res.Row == nil {
		t.Fatalf("back: %+v", res)
	}
	text, _ := os.ReadFile(src.transcript(msid, cwd))
	if !strings.Contains(string(text), "went on there") || strings.Contains(string(text), testkit.JSONString(dir)) {
		t.Fatalf("home again, in its own directory:\n%s", text)
	}
	src.on()
	if entries, _ := tend.LoadTrash(); len(entries) != 1 || !strings.Contains(entries[0].Title, "ask one") {
		t.Errorf("the replaced original waits in the trash, under its title: %+v", entries)
	}
	src.goOn(msid, cwd, "and on here")
	migrateOnce(t, src, dst, dir, false)
	text, _ = os.ReadFile(dst.transcript(msid, dir))
	if !strings.Contains(string(text), "and on here") {
		t.Fatalf("forward:\n%s", text)
	}
}
