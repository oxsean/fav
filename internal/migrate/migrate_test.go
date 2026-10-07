package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/testkit"
	"github.com/oxsean/fav/internal/wire"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// machine is one end of a migration in this process: its Claude home, data directory and home, selected by on.
type machine struct {
	t                        *testing.T
	name, claude, data, home string
}

func newMachine(t *testing.T, name string) *machine {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &machine{t: t, name: name, claude: filepath.Join(root, "claude"), data: filepath.Join(root, "tend"), home: filepath.Join(root, "home")}
	for _, d := range []string{m.claude, m.data, m.home, filepath.Join(m.claude, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func (m *machine) on(f func()) {
	m.t.Setenv("CLAUDE_CONFIG_DIR", m.claude)
	m.t.Setenv("TEND_HOME", m.data)
	f()
}

func (m *machine) end() End {
	host, _ := os.Hostname()
	return End{OS: runtime.GOOS, Home: m.home, Host: host + "-" + m.name}
}

func (m *machine) peer() Peer {
	return Peer{Name: m.name, Endpoint: "ep-" + m.name, End: m.end()}
}

func (m *machine) dir(parts ...string) string {
	d := filepath.Join(append([]string{m.home, "dev"}, parts...)...)
	if err := os.MkdirAll(d, 0o755); err != nil {
		m.t.Fatal(err)
	}
	return d
}

// session writes a Claude session sid in cwd with a sub-agent transcript, a file-history entry, a todo and a
// session-env directory a migration leaves behind.
func (m *machine) session(sid, cwd string) string {
	m.t.Helper()
	l := fixture.NewLiveClaude(m.claude, sid, cwd, "cli")
	for _, s := range []string{"one", "two", "three"} {
		if l.User("ask "+s) != nil || l.Reply("answer "+s) != nil {
			m.t.Fatal("transcript")
		}
	}
	side := filepath.Join(filepath.Dir(l.Path()), sid, "subagents", "agent-1.jsonl")
	files := map[string]string{
		side: `{"type":"user","cwd":` + testkit.JSONString(cwd) + `,"sessionId":"` + sid + `"}` + "\n",
		filepath.Join(m.claude, "file-history", sid, "abc@v1"):      "before the edit\n",
		filepath.Join(m.claude, "todos", sid+"-agent-"+sid+".json"): "[]",
		filepath.Join(m.claude, "session-env", sid, "env.sh"):       "export TOKEN=fake\n",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			m.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			m.t.Fatal(err)
		}
	}
	return l.Path()
}

// trip is a migration of one session from src to dst driven by hand, step by step, as remote.Migrate drives it.
type trip struct {
	t        *testing.T
	src, dst *machine
	sid, cwd string
	dir      string
	m        string
	man      Manifest
	carried  *Sum // what the commit carried of the source's main transcript
}

func newTrip(t *testing.T, src, dst *machine, sid, cwd, dir string) *trip {
	return &trip{t: t, src: src, dst: dst, sid: sid, cwd: cwd, dir: dir, m: NewID(time.Now())}
}

func (x *trip) rec() *tend.Rec {
	return &tend.Rec{Provider: tend.ProviderClaude, SessionID: x.sid, Cwd: x.cwd, Title: "t"}
}

func (x *trip) plan() (man Manifest, err error) {
	x.src.on(func() { man, err = Plan(nil, x.rec()) })
	return man, err
}

func (x *trip) intend() {
	x.t.Helper()
	man, err := x.plan()
	if err != nil {
		x.t.Fatal(err)
	}
	x.man = man
	x.src.on(func() { err = Intend(x.m, x.rec(), x.dst.peer(), man) })
	if err != nil {
		x.t.Fatal(err)
	}
}

func (x *trip) begin() (b Began, err error) {
	x.dst.on(func() {
		b, err = Begin(Incoming{Migration: x.m, From: x.src.peer(), Provider: tend.ProviderClaude, SessionID: x.sid, Cwd: x.cwd,
			Dir: x.dir, Pairs: []Pair{{x.cwd, x.dir}}, Manifest: x.man})
	})
	return b, err
}

// send moves every byte begin says is missing, n bytes a chunk.
func (x *trip) send(b Began, n int) error {
	for _, f := range x.man.Files {
		for off := b.Staged[f.Path]; off < f.Size; {
			var data []byte
			var err error
			x.src.on(func() { data, _, err = Read(nil, x.rec(), f.Path, off, n, f.ID) })
			if err != nil {
				return err
			}
			x.dst.on(func() { off, err = Chunk(x.m, f.Path, off, data) })
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (x *trip) commit() (c Committed, err error) {
	x.dst.on(func() {
		s, serr := tend.Open()
		if serr != nil {
			err = serr
			return
		}
		end := x.dst.end()
		c, err = Commit(s, x.m, "# note\n", end.Map(), func(force map[string]bool) error {
			idx, err := index.Open()
			if err == nil {
				_, err = idx.RescanSave(force)
			}
			return err
		})
	})
	if err == nil {
		x.carried = c.Record.Source
	}
	return c, err
}

func (x *trip) finish() {
	x.t.Helper()
	var err error
	x.src.on(func() { _, err = Finish(x.m, tend.ProviderClaude, x.sid, StateDone, x.carried) })
	if err != nil {
		x.t.Fatal(err)
	}
}

// run is the whole migration; it answers the commit.
func (x *trip) run() Committed {
	x.t.Helper()
	x.intend()
	b, err := x.begin()
	if err != nil || b.Clash == ClashExists || b.Clash == ClashDiverged {
		x.t.Fatalf("begin: %+v %v", b, err)
	}
	if err := x.send(b, ChunkSize); err != nil {
		x.t.Fatal(err)
	}
	c, err := x.commit()
	if err != nil {
		x.t.Fatal(err)
	}
	x.finish()
	return c
}

const sid1 = "5e551011-0c1a-4de0-8000-000000000001"

const sid2 = "5e551011-0c1a-4de0-8000-000000000002"

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A migration copies the transcript, its sub-agent transcript, file history and todos into the target's project
// directory of the new cwd, every cwd value carried over and nothing else changed; both ends record it, the target
// keeps the note, the staging goes, and the source still has everything.
func TestMigrationCopiesSessionAndRewritesCwd(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("site", "webapp")
	var orig string
	src.on(func() { orig = src.session(sid1, cwd) })
	before := read(t, orig)
	x := newTrip(t, src, dst, sid1, cwd, dir)
	c := x.run()

	if !slices.Equal(x.man.Left, []string{"session-env/" + sid1}) {
		t.Errorf("left behind: %v", x.man.Left)
	}
	var got []string
	for _, f := range x.man.Files {
		got = append(got, f.Kind+" "+f.Path)
	}
	enc := index.ClaudeProjectName(cwd)
	want := []string{"copy file-history/" + sid1 + "/abc@v1", "transcript projects/" + enc + "/" + sid1 + ".jsonl",
		"side projects/" + enc + "/" + sid1 + "/subagents/agent-1.jsonl", "copy todos/" + sid1 + "-agent-" + sid1 + ".json"}
	if !slices.Equal(got, want) {
		t.Errorf("manifest %v, want %v", got, want)
	}
	there := filepath.Join(dst.claude, "projects", index.ClaudeProjectName(dir), sid1+".jsonl")
	text := read(t, there)
	if strings.Contains(text, testkit.JSONString(cwd)) || strings.Count(text, testkit.JSONString(dir)) != strings.Count(before, testkit.JSONString(cwd)) ||
		strings.Count(text, "\n") != strings.Count(before, "\n") {
		t.Errorf("cwd values carried over, lines kept:\n%s", text)
	}
	if strings.ReplaceAll(text, testkit.JSONString(dir), testkit.JSONString(cwd)) != before {
		t.Errorf("only cwd changed:\n%s\nfrom\n%s", text, before)
	}
	side := read(t, filepath.Join(dst.claude, "projects", index.ClaudeProjectName(dir), sid1, "subagents", "agent-1.jsonl"))
	if !strings.Contains(side, testkit.JSONString(dir)) {
		t.Errorf("sub-agent transcript keeps the old cwd: %s", side)
	}
	if read(t, filepath.Join(dst.claude, "file-history", sid1, "abc@v1")) != "before the edit\n" {
		t.Error("file history copied as it is")
	}
	if _, err := os.Stat(filepath.Join(dst.claude, "session-env", sid1)); err == nil {
		t.Error("session-env is never copied")
	}
	if main, _ := x.man.Main(sid1); !modTime(there).Equal(main.ModTime) {
		t.Errorf("the copy keeps the original's time: %v, not %v", modTime(there), main.ModTime)
	}
	if read(t, orig) != before {
		t.Error("the original changed")
	}
	if c.Files != 4 || c.Record.Role != RoleFrom || c.Record.Dir != dir || c.Record.Note == "" || read(t, c.Record.Note) != "# note\n" {
		t.Errorf("commit: %+v", c)
	}
	if _, err := os.Stat(filepath.Join(dst.data, "import", x.m)); err == nil {
		t.Error("the staging stays after the commit")
	}
	main, _ := x.man.Main(sid1)
	src.on(func() {
		recs, _ := Of(tend.ProviderClaude, sid1)
		if len(recs) != 1 || recs[0].Role != RoleTo || recs[0].State != StateDone || recs[0].SHA != main.SHA || recs[0].Peer.Endpoint != "ep-dst" {
			t.Errorf("source record: %+v", recs)
		}
		if Reminder(tend.ProviderClaude, sid1) == "" {
			t.Error("the source reminds before a resume")
		}
	})
	dst.on(func() {
		recs, _ := Of(tend.ProviderClaude, sid1)
		if len(recs) != 1 || recs[0].SHA == main.SHA || recs[0].Size != int64(len(text)) || recs[0].Peer.Endpoint != "ep-src" {
			t.Errorf("target record: %+v", recs)
		}
		prompt, done := FirstResume(tend.ProviderClaude, sid1)
		if !strings.Contains(prompt, c.Record.Note) {
			t.Errorf("first resume: %q", prompt)
		}
		done()
		if again, _ := FirstResume(tend.ProviderClaude, sid1); again != "" {
			t.Errorf("the note is sent once: %q", again)
		}
		if Reminder(tend.ProviderClaude, sid1) != "" {
			t.Error("the target has nothing to remind of")
		}
		copies, _ := Copies(tend.ProviderClaude, sid1)
		if len(copies) != 1 || copies[0].Changed == nil || *copies[0].Changed {
			t.Errorf("copies: %+v", copies)
		}
		marks := Marks()
		if cs := marks(&tend.Rec{Provider: tend.ProviderClaude, SessionID: sid1}); len(cs) != 1 || cs[0].Role != RoleFrom || cs[0].Peer != "src" {
			t.Errorf("marks: %+v", cs)
		}
	})
}

// A cwd value neither a pair nor the homes map stays as it was and the note names it.
// An empty file arrives empty: nothing is sent for it.
func TestAnEmptyFileArrives(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	src.on(func() { src.session(sid1, cwd) })
	if err := os.WriteFile(filepath.Join(src.claude, "file-history", sid1, "empty@v1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if c := newTrip(t, src, dst, sid1, cwd, dir).run(); c.Files != 5 {
		t.Fatalf("files %d", c.Files)
	}
	if st, err := os.Stat(filepath.Join(dst.claude, "file-history", sid1, "empty@v1")); err != nil || st.Size() != 0 {
		t.Fatalf("%v %v", st, err)
	}
}

func TestUnmappedCwdIsNamedInTheNote(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	var p string
	src.on(func() { p = src.session(sid1, cwd) })
	outside := filepath.Join(filepath.Dir(src.home), "elsewhere")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"user","cwd":` + testkit.JSONString(outside) + `}` + "\n")
	f.Close()
	c := newTrip(t, src, dst, sid1, cwd, dir).run()
	if !slices.Equal(c.Unmapped, []string{outside}) || !strings.Contains(read(t, c.Record.Note), outside) {
		t.Errorf("unmapped %v, note %q", c.Unmapped, read(t, c.Record.Note))
	}
}

// A file staged whole is not sent again under a new plan with the same sha; a file whose sha changed starts over, and
// one sent in part goes on from where it stopped.
func TestStagingReusesFilesBySHA(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	var p string
	src.on(func() { p = src.session(sid1, cwd) })
	x := newTrip(t, src, dst, sid1, cwd, dir)
	x.intend()
	b, err := x.begin()
	if err != nil || len(b.Staged) != 0 {
		t.Fatalf("fresh: %+v %v", b, err)
	}
	if err := x.send(b, 64); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"user","cwd":` + testkit.JSONString(cwd) + `}` + "\n")
	f.Close()
	x.intend()
	if b, err = x.begin(); err != nil {
		t.Fatal(err)
	}
	main, _ := x.man.Main(sid1)
	for _, f := range x.man.Files {
		want := f.Size
		if f.Path == main.Path {
			want = 0
		}
		if b.Staged[f.Path] != want {
			t.Errorf("%s: staged %d, want %d", f.Path, b.Staged[f.Path], want)
		}
	}
	var data []byte
	src.on(func() { data, _, err = Read(nil, x.rec(), main.Path, 0, 100, main.ID) })
	dst.on(func() { _, err = Chunk(x.m, main.Path, 0, data) })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ = x.begin(); b.Staged[main.Path] != 100 {
		t.Errorf("a file sent in part goes on from %d", b.Staged[main.Path])
	}
	if err := x.send(b, ChunkSize); err != nil {
		t.Fatal(err)
	}
	if _, err := x.commit(); err != nil {
		t.Fatal(err)
	}
}

// A chunk is taken only at the bytes staged so far; one sent again is answered without writing, and a whole file whose
// sha is not the plan's starts over as stale.
func TestChunkTakesOnlyTheNextBytes(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	src.on(func() { src.session(sid1, cwd) })
	x := newTrip(t, src, dst, sid1, cwd, dir)
	x.intend()
	if _, err := x.begin(); err != nil {
		t.Fatal(err)
	}
	f := x.man.Files[0] // file-history, 16 bytes
	dst.on(func() {
		if off, err := Chunk(x.m, f.Path, 0, []byte("before")); err != nil || off != 6 {
			t.Fatalf("first: %d %v", off, err)
		}
		if off, err := Chunk(x.m, f.Path, 0, []byte("before")); err != nil || off != 6 {
			t.Errorf("sent again: %d %v", off, err)
		}
		if _, err := Chunk(x.m, f.Path, 9, []byte("x")); wire.Code(err) != wire.CodeConflict {
			t.Errorf("a gap: %v", err)
		}
		if _, err := Chunk(x.m, f.Path, 6, bytes.Repeat([]byte("x"), 100)); wire.Code(err) != wire.CodeStale {
			t.Errorf("past the size, the source grew: %v", err)
		}
		if _, err := Chunk(x.m, f.Path, 6, []byte(" the edxt\n")); wire.Code(err) != wire.CodeStale {
			t.Errorf("a wrong sha: %v", err)
		}
		if b, _ := Begin(Incoming{Migration: x.m, From: src.peer(), Provider: tend.ProviderClaude, SessionID: sid1, Cwd: cwd, Dir: dir,
			Manifest: x.man}); b.Staged[f.Path] != 0 {
			t.Errorf("the wrong file starts over: %d", b.Staged[f.Path])
		}
		if _, err := Chunk("other", f.Path, 0, []byte("b")); wire.Code(err) != wire.CodeNotFound {
			t.Errorf("an unknown migration: %v", err)
		}
	})
}

// A commit that finds a file in its way takes back what it put in place and can run again once the way is clear; a
// committed migration answers its record again.
func TestCommitTakesBackWhatItPutAndRunsAgain(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	src.on(func() { src.session(sid1, cwd) })
	x := newTrip(t, src, dst, sid1, cwd, dir)
	x.intend()
	b, _ := x.begin()
	if err := x.send(b, ChunkSize); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(dst.claude, "file-history", sid1, "abc@v1")
	blocker := filepath.Join(dst.claude, "todos", sid1+"-agent-"+sid1+".json")
	os.MkdirAll(filepath.Dir(blocker), 0o755)
	os.WriteFile(blocker, []byte("someone else's"), 0o600)
	if _, err := x.commit(); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("a file in the way: %v", err)
	}
	if _, err := os.Stat(first); err == nil {
		t.Error("the file put before the failure stays")
	}
	os.Remove(blocker)
	c, err := x.commit()
	if err != nil || c.Files != 4 {
		t.Fatalf("again: %+v %v", c, err)
	}
	if again, err := x.commit(); err != nil || again.Record.Migration != x.m || again.Files != 4 {
		t.Errorf("committed already: %+v %v", again, err)
	}
	x.finish()
}

// The same session already on the target: unrelated is refused, an unchanged copy from the source is replaced (the old
// one restorable from the trash), a copy that went on is refused as diverged; and a session copied back to where it
// came from replaces the unchanged original there.
func TestSameSessionOnTheTarget(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	var orig string
	src.on(func() { orig = src.session(sid1, cwd) })
	dst.on(func() { dst.session(sid1, dir) })
	x := newTrip(t, src, dst, sid1, cwd, dir)
	x.intend()
	if b, err := x.begin(); err != nil || b.Clash != ClashExists {
		t.Fatalf("unrelated: %+v %v", b, err)
	}
	dst.on(func() {
		for _, p := range index.SessionFiles(tend.ProviderClaude, sid1) {
			os.RemoveAll(p)
		}
	})
	x.src.on(func() { Finish(x.m, tend.ProviderClaude, sid1, StateAborted, nil) })

	newTrip(t, src, dst, sid1, cwd, dir).run()
	appendLine(t, src, orig, "went on at the source")
	fwd := newTrip(t, src, dst, sid1, cwd, dir)
	fwd.intend()
	if b, err := fwd.begin(); err != nil || b.Clash != ClashForward {
		t.Fatalf("an unchanged copy: %+v %v", b, err)
	}
	b, _ := fwd.begin()
	if err := fwd.send(b, ChunkSize); err != nil {
		t.Fatal(err)
	}
	if _, err := fwd.commit(); err != nil {
		t.Fatal(err)
	}
	fwd.finish()
	there := filepath.Join(dst.claude, "projects", index.ClaudeProjectName(dir), sid1+".jsonl")
	if !strings.Contains(read(t, there), "went on at the source") {
		t.Error("the forward copy is the source's newer one")
	}
	dst.on(func() {
		entries, _ := tend.LoadTrash()
		if len(entries) != 1 || entries[0].SessionID != sid1 || slices.ContainsFunc(entries[0].Files, func(f tend.Moved) bool { return f.Replaced == "" }) {
			t.Errorf("the replaced copy is in the trash, each file restorable: %+v", entries)
		}
	})

	appendLine(t, dst, there, "went on at the target")
	appendLine(t, src, orig, "and at the source")
	div := newTrip(t, src, dst, sid1, cwd, dir)
	div.intend()
	if b, err := div.begin(); err != nil || b.Clash != ClashDiverged {
		t.Fatalf("both went on: %+v %v", b, err)
	}
	src.on(func() { Finish(div.m, tend.ProviderClaude, sid1, StateAborted, nil) })

	back := newTrip(t, dst, src, sid1, dir, cwd)
	back.intend()
	if b, err := back.begin(); err != nil || b.Clash != ClashDiverged {
		t.Fatalf("back while the source went on too: %+v %v", b, err)
	}
	dst.on(func() { Finish(back.m, tend.ProviderClaude, sid1, StateAborted, nil) })
}

// A forward commit into another directory than the copy it replaces: each file of that copy names the one now in its
// place, so restoring it takes the new one out (one copy of the session), and refuses once the new one was used.
func TestAForwardIntoAnotherDirectoryRestoresAsOneCopy(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir1, dir2 := src.dir("webapp"), dst.dir("a", "webapp"), dst.dir("b", "webapp")
	var orig string
	src.on(func() { orig = src.session(sid1, cwd) })
	newTrip(t, src, dst, sid1, cwd, dir1).run()
	appendLine(t, src, orig, "went on at the source")
	if c := newTrip(t, src, dst, sid1, cwd, dir2).run(); c.Files != 4 {
		t.Fatalf("forward: %+v", c)
	}
	p1 := filepath.Join(dst.claude, "projects", index.ClaudeProjectName(dir1), sid1+".jsonl")
	p2 := filepath.Join(dst.claude, "projects", index.ClaudeProjectName(dir2), sid1+".jsonl")
	if exists(p1) || !exists(p2) {
		t.Fatalf("the copy moved to %s", p2)
	}
	dst.on(func() {
		entries, _ := tend.LoadTrash()
		if len(entries) != 1 || slices.ContainsFunc(entries[0].Files, func(f tend.Moved) bool { return f.Replaced == "" }) {
			t.Fatalf("each replaced file names its successor: %+v", entries)
		}
		appendLine(t, dst, p2, "went on at the target")
		if _, err := tend.RestoreTrash(tend.ProviderClaude, sid1); err == nil {
			t.Error("the new copy was used: no restore over it")
		}
	})
	dst.on(func() {
		entries, _ := tend.LoadTrash()
		for _, f := range entries[0].Files {
			if f.Replaced != "" {
				os.RemoveAll(f.Replaced) // as if the user deleted the new copy
			}
		}
		if _, err := tend.RestoreTrash(tend.ProviderClaude, sid1); err != nil {
			t.Fatalf("restore: %v", err)
		}
	})
	if !exists(p1) || exists(p2) {
		t.Errorf("one copy, back where it was: %v %v", exists(p1), exists(p2))
	}
}

// A manifest's aliases are other ids of the same session: a pattern, an empty name, a dot, the session's own id or one
// twice is refused before anything here is touched.
func TestBeginRefusesAliasesThatAreNotSessionIDs(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	src.on(func() { src.session(sid1, cwd) })
	dst.on(func() { dst.session(sid2, dst.dir("unrelated")) })
	x := newTrip(t, src, dst, sid1, cwd, dir)
	x.intend()
	planned := x.man
	for _, aliases := range [][]string{{"*"}, {""}, {".."}, {"a/b"}, {sid1}, {sid2, sid2}} {
		x.man = planned
		x.man.Aliases = aliases
		if _, err := x.begin(); wire.Code(err) != wire.CodeBadRequest {
			t.Errorf("aliases %q: %v", aliases, err)
		}
	}
	if !exists(dst.claude, "projects", index.ClaudeProjectName(dst.dir("unrelated")), sid2+".jsonl") {
		t.Error("the target's other session stays")
	}
}

// The target crashed while placing, the source went on, the driver planned again: the target finishes what it had
// staged, and the source records that content, so its copy reads as gone on since.
func TestACrashWhilePlacingThenTheSourceGoesOn(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	var orig string
	src.on(func() { orig = src.session(sid1, cwd) })
	x := newTrip(t, src, dst, sid1, cwd, dir)
	x.intend()
	x.crashAt("placing")
	appendLine(t, src, orig, "went on after the crash")
	x.intend()
	b, err := x.begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := x.send(b, ChunkSize); err != nil {
		t.Fatal(err)
	}
	if _, err := x.commit(); err != nil {
		t.Fatal(err)
	}
	x.finish()
	there := filepath.Join(dst.claude, "projects", index.ClaudeProjectName(dir), sid1+".jsonl")
	if strings.Contains(read(t, there), "went on after the crash") {
		t.Fatal("the target finishes the commit it had begun")
	}
	src.on(func() {
		rec, _, err := find(x.m)
		if changed := Changed(rec); err != nil || changed == nil || !*changed {
			t.Errorf("the source went on since what the target holds: %+v %v", rec, err)
		}
	})
}

func appendLine(t *testing.T, m *machine, p, text string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"user","message":{"content":"` + text + `"}}` + "\n")
	f.Close()
}

// Copied there, gone on with there, copied back: the unchanged original here is replaced.
func TestMigratingBackReplacesTheUnchangedOriginal(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	var orig string
	src.on(func() { orig = src.session(sid1, cwd) })
	newTrip(t, src, dst, sid1, cwd, dir).run()
	there := filepath.Join(dst.claude, "projects", index.ClaudeProjectName(dir), sid1+".jsonl")
	appendLine(t, dst, there, "went on at the target")
	back := newTrip(t, dst, src, sid1, dir, cwd)
	back.intend()
	b, err := back.begin()
	if err != nil || b.Clash != ClashForward {
		t.Fatalf("back: %+v %v", b, err)
	}
	if err := back.send(b, ChunkSize); err != nil {
		t.Fatal(err)
	}
	if _, err := back.commit(); err != nil {
		t.Fatal(err)
	}
	back.finish()
	text := read(t, orig)
	if !strings.Contains(text, "went on at the target") || !strings.Contains(text, testkit.JSONString(cwd)) || strings.Contains(text, testkit.JSONString(dir)) {
		t.Errorf("back home, its cwd this machine's again:\n%s", text)
	}
	src.on(func() {
		copies, _ := Copies(tend.ProviderClaude, sid1)
		if len(copies) != 2 || copies[0].Role != RoleFrom || copies[1].Role != RoleTo {
			t.Errorf("both migrations recorded here: %+v", copies)
		}
	})
}

// envTrip hands a trip to a child process of this test binary (TestATripInAChildProcess), which runs its begin, send
// and commit until proc.EnvCrashAt stops it.
const envTrip = "MIGRATE_TEST_TRIP"

type tripState struct {
	Src, Dst string // machine roots
	Sid, Cwd string
	Dir, M   string
	Man      Manifest
}

func machineAt(t *testing.T, root, name string) *machine {
	return &machine{t: t, name: name, claude: filepath.Join(root, "claude"), data: filepath.Join(root, "tend"), home: filepath.Join(root, "home")}
}

func (m *machine) root() string { return filepath.Dir(m.claude) }

// crashAt runs begin, send and commit in a child process that exits at migrate.<point>; it fails the test unless the
// child stopped there.
func (x *trip) crashAt(point string) {
	x.t.Helper()
	b, err := json.Marshal(tripState{Src: x.src.root(), Dst: x.dst.root(), Sid: x.sid, Cwd: x.cwd, Dir: x.dir, M: x.m, Man: x.man})
	if err != nil {
		x.t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestATripInAChildProcess$")
	cmd.Env = append(os.Environ(), envTrip+"="+string(b), proc.EnvCrashAt+"=migrate."+point)
	out, err := cmd.CombinedOutput()
	if ee := (*exec.ExitError)(nil); !errors.As(err, &ee) || ee.ExitCode() != 86 {
		x.t.Fatalf("the child did not stop at %s: %v\n%s", point, err, out)
	}
}

func TestATripInAChildProcess(t *testing.T) {
	raw := os.Getenv(envTrip)
	if raw == "" {
		t.Skip("run by crashAt")
	}
	var st tripState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	x := &trip{t: t, src: machineAt(t, st.Src, "src"), dst: machineAt(t, st.Dst, "dst"), sid: st.Sid, cwd: st.Cwd, dir: st.Dir, m: st.M, man: st.Man}
	b, err := x.begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := x.send(b, 64); err != nil {
		t.Fatal(err)
	}
	if _, err := x.commit(); err != nil {
		t.Fatal(err)
	}
}

// A crash at each cut point of the target leaves what the next run finishes: the same files and records as a run that
// never stopped. An abandoned one leaves nothing of it in the Claude home.
func TestACrashAtEveryCutPointResumes(t *testing.T) {
	for _, point := range []string{"begun", "half", "prepared", "placing", "placed"} {
		t.Run(point, func(t *testing.T) {
			src, dst := newMachine(t, "src"), newMachine(t, "dst")
			cwd, dir := src.dir("webapp"), dst.dir("webapp")
			src.on(func() { src.session(sid1, cwd) })
			x := newTrip(t, src, dst, sid1, cwd, dir)
			x.intend()
			x.crashAt(point)
			src.on(func() {
				if err := Pending(x.m, tend.ProviderClaude, sid1); err != nil {
					t.Errorf("the source still waits: %v", err)
				}
			})
			b, err := x.begin()
			if err != nil || b.Committed || b.Clash != ClashNone {
				t.Fatalf("resumed: %+v %v", b, err)
			}
			if err := x.send(b, ChunkSize); err != nil {
				t.Fatal(err)
			}
			c, err := x.commit()
			if err != nil || c.Files != 4 {
				t.Fatalf("commit: %+v %v", c, err)
			}
			x.finish()
			there := filepath.Join(dst.claude, "projects", index.ClaudeProjectName(dir), sid1+".jsonl")
			if !strings.Contains(read(t, there), testkit.JSONString(dir)) {
				t.Error("the resumed copy is rewritten")
			}
		})
	}
	t.Run("abandoned", func(t *testing.T) {
		src, dst := newMachine(t, "src"), newMachine(t, "dst")
		cwd, dir := src.dir("webapp"), dst.dir("webapp")
		src.on(func() { src.session(sid1, cwd) })
		x := newTrip(t, src, dst, sid1, cwd, dir)
		x.intend()
		x.crashAt("placing")
		dst.on(func() {
			if err := Abort(x.m); err != nil {
				t.Fatal(err)
			}
			var left []string
			filepath.WalkDir(dst.claude, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					left = append(left, p)
				}
				return nil
			})
			if len(left) != 0 || exists(dst.data, "import", x.m) {
				t.Errorf("abandoned, left: %v", left)
			}
		})
	})
}

func modTime(p string) time.Time {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func exists(parts ...string) bool { _, err := os.Stat(filepath.Join(parts...)); return err == nil }

// The source refuses a file replaced since the plan, and a plan after an append differs from the one before.
func TestTheSourceRefusesAFileThatChanged(t *testing.T) {
	src := newMachine(t, "src")
	cwd := src.dir("webapp")
	var p string
	src.on(func() { p = src.session(sid1, cwd) })
	x := newTrip(t, src, newMachine(t, "dst"), sid1, cwd, "")
	before, _ := x.plan()
	main, _ := before.Main(sid1)
	src.on(func() {
		if _, _, err := Read(nil, x.rec(), main.Path, 0, 10, main.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Read(nil, x.rec(), "projects/x/other.jsonl", 0, 10, main.ID); wire.Code(err) != wire.CodeBadRequest {
			t.Errorf("another session's file: %v", err)
		}
		b, _ := os.ReadFile(p)
		os.WriteFile(p+".new", b, 0o600)
		os.Rename(p+".new", p)
		if _, _, err := Read(nil, x.rec(), main.Path, 0, 10, main.ID); wire.Code(err) != wire.CodeStale {
			t.Errorf("replaced since the plan: %v", err)
		}
	})
	appendLine(t, src, p, "more")
	after, _ := x.plan()
	if m2, _ := after.Main(sid1); m2.Size == main.Size || m2.SHA == main.SHA {
		t.Error("the plan sees the file grow")
	}
}

// A plan refuses what it cannot copy safely: a symlink among the session's files, a Codex session.
func TestPlanRefusesWhatItCannotCopy(t *testing.T) {
	src := newMachine(t, "src")
	cwd := src.dir("webapp")
	src.on(func() {
		src.session(sid1, cwd)
		if _, err := Plan(nil, &tend.Rec{Provider: tend.ProviderCodex, SessionID: sid1}); wire.Code(err) != wire.CodeUnsupported {
			t.Errorf("codex: %v", err)
		}
		link := filepath.Join(src.claude, "file-history", sid1, "link")
		if err := os.Symlink(filepath.Join(src.home, "secret"), link); err != nil {
			t.Skip("no symlinks here")
		}
		if _, err := Plan(nil, &tend.Rec{Provider: tend.ProviderClaude, SessionID: sid1}); wire.Code(err) != wire.CodeBadRequest {
			t.Errorf("a symlink: %v", err)
		}
	})
}

// A staging nobody committed for seven days goes at the next begin; a fresh one stays.
func TestBeginSweepsStaleStagings(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	src.on(func() { src.session(sid1, cwd) })
	old := newTrip(t, src, dst, sid1, cwd, dir)
	old.intend()
	old.begin()
	fresh := newTrip(t, src, dst, sid1, cwd, dir)
	fresh.m = old.m + "x"
	fresh.man = old.man
	dst.on(func() {
		s := stagingOf(old.m)
		var st stagedState
		fileio.ReadJSON(s.state(), &st)
		st.At = time.Now().Add(-StaleAfter - time.Hour)
		fileio.WriteJSON(s.state(), st)
	})
	fresh.begin()
	if exists(dst.data, "import", old.m) || !exists(dst.data, "import", fresh.m) {
		t.Error("the stale staging goes, the fresh one stays")
	}
}

// Moving away puts the original in the source's trash under a title naming where it went, once.
func TestMoveAwayTrashesTheOriginalOnce(t *testing.T) {
	src, dst := newMachine(t, "src"), newMachine(t, "dst")
	cwd, dir := src.dir("webapp"), dst.dir("webapp")
	var orig string
	src.on(func() { orig = src.session(sid1, cwd) })
	x := newTrip(t, src, dst, sid1, cwd, dir)
	x.run()
	src.on(func() {
		s, _ := tend.Open()
		rec, _, _ := find(x.m)
		n, _, err := MoveAway(s, nil, x.rec(), rec)
		if err != nil || n == 0 || exists(orig) {
			t.Fatalf("moved %d: %v", n, err)
		}
		entries, _ := tend.LoadTrash()
		if len(entries) != 1 || !strings.Contains(entries[0].Title, "dst") {
			t.Errorf("trash: %+v", entries)
		}
		rec, _, _ = find(x.m)
		if n, _, err := MoveAway(s, nil, x.rec(), rec); err != nil || n != 0 || !rec.Moved {
			t.Errorf("again: %d %v %+v", n, err, rec)
		}
		copies, _ := Copies(tend.ProviderClaude, sid1)
		if len(copies) != 1 || copies[0].Changed != nil {
			t.Errorf("a moved original cannot tell: %+v", copies)
		}
	})
}
