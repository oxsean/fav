package node

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/wire"
)

// actScript is what the _act agent does: files it writes and removes, then what it says on stdout.
type actScript struct {
	Write  map[string]string `json:"write"`
	Remove []string          `json:"remove"`
	Say    string            `json:"say"`
}

// act runs an _act agent in dir to its end as run id of n, answering its run directory.
func act(t *testing.T, n *Node, dir string, work *Workspace, a actScript) (string, string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "act.json")
	b, _ := json.Marshal(a)
	os.WriteFile(script, b, 0o600)
	id := NewRunID()
	runDir := n.runDir(id)
	os.MkdirAll(runDir, 0o700)
	fileio.WriteJSON(filepath.Join(runDir, "spec.json"), Spec{Run: id, Argv: []string{os.Args[0], "_act", script}, Dir: dir, Runner: RunnerBackground,
		Created: time.Now(), Work: work})
	os.WriteFile(filepath.Join(runDir, "prompt.md"), nil, 0o600)
	if err := Supervise(runDir); err != nil {
		t.Fatal(err)
	}
	return id, runDir
}

func editLine(path, original string) string {
	return `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"ok"}]},"tool_use_result":{"filePath":` +
		jsonStr(path) + `,"originalFile":` + jsonStr(original) + `,"structuredPatch":[]}}` + "\n"
}

func bashLine(command string) string {
	return `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b","name":"Bash","input":{"command":` + jsonStr(command) + `}}]}}` + "\n"
}

func codeOf(err error) string {
	var we *wire.Error
	if errors.As(err, &we) {
		return we.Code
	}
	return ""
}

func fileOps(fs []FileChange) string {
	var ps []string
	for _, f := range fs {
		ps = append(ps, f.Path+":"+f.Op)
	}
	return strings.Join(ps, " ")
}

// A directory run in git lists the files the agent's tools edited and its commands named; the owner sees the rest.
// Its trees are kept by refs until the run directory goes.
func TestChangesOfADirectoryRun(t *testing.T) {
	needGit(t)
	dir := repo(t)
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		os.WriteFile(filepath.Join(dir, f), []byte(f+"\n"), 0o644)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "--quiet", "-m", "files")
	n := New(t.TempDir())
	id, runDir := act(t, n, dir, nil, actScript{
		Write: map[string]string{filepath.Join(dir, "a.txt"): "A\n", filepath.Join(dir, "b.txt"): "B\n",
			filepath.Join(dir, "c.txt"): "C\n", filepath.Join(dir, "d.txt"): "d\n"},
		Say: editLine(filepath.Join(dir, "a.txt"), "a.txt\n") + bashLine("gofmt -w b.txt"),
	})
	for _, f := range []string{changesFile, treesFile} {
		if !fileExists(filepath.Join(runDir, f)) {
			t.Fatalf("no %s", f)
		}
	}
	res, err := n.Changes(ChangesParams{Run: id})
	if err != nil {
		t.Fatal(err)
	}
	if fileOps(res.Files) != "a.txt:modify b.txt:modify" || res.Hidden != 2 || !res.Git || res.Total != (ChangeTotal{2, 2, 2}) || res.Next != "" {
		t.Fatalf("%+v", res)
	}
	if !res.Files[0].Agent || !res.Files[1].Agent {
		t.Fatalf("the agent's: %+v", res.Files)
	}
	all, _ := n.Changes(ChangesParams{Run: id, All: true})
	if fileOps(all.Files) != "a.txt:modify b.txt:modify c.txt:modify d.txt:add" || all.Hidden != 0 || all.Files[2].Agent {
		t.Fatalf("all %+v", all)
	}
	if all.Snapshot != res.Snapshot || res.Snapshot == "" {
		t.Fatal("the end tree is the snapshot")
	}

	d, err := n.Diff(DiffParams{Run: id, Path: "a.txt", Snapshot: res.Snapshot})
	if err != nil || d.Of != 1 || d.Hunks[0].At != "@@ -1 +1 @@" || strings.Join(d.Hunks[0].Lines, "|") != "-a.txt|+A" || d.Next != nil {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := n.Diff(DiffParams{Run: id, Path: "c.txt"}); codeOf(err) != wire.CodeNotFound {
		t.Fatalf("only the owner reads c.txt: %v", err)
	}
	if _, err := n.Diff(DiffParams{Run: id, Path: "c.txt", All: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Diff(DiffParams{Run: id, Path: "a.txt", Snapshot: "other"}); codeOf(err) != wire.CodeSnapshotChanged {
		t.Fatalf("another snapshot: %v", err)
	}

	for _, which := range []string{"base", "end"} {
		git(t, dir, "rev-parse", "--verify", refPrefix+id+"/"+which)
	}
	n.forget(id)
	if out, _ := (gitIn{dir: dir}).run("for-each-ref", refPrefix); out != "" {
		t.Fatalf("refs left: %s", out)
	}
	if _, err := n.Changes(ChangesParams{Run: id}); codeOf(err) != wire.CodeNotFound {
		t.Fatalf("a forgotten run: %v", err)
	}
}

// A worktree run lists every change, what it committed included.
func TestChangesOfAWorktreeRun(t *testing.T) {
	needGit(t)
	checkout := repo(t)
	wt := filepath.Join(checkout+"-wt", "t_1")
	n := New(t.TempDir())
	id, _ := act(t, n, wt, &Workspace{Checkout: checkout, Branch: "tend/t_1", Base: "main"}, actScript{
		Write: map[string]string{filepath.Join(wt, "gen", "x.pb.go"): "package gen\n"}, Remove: []string{filepath.Join(wt, "README")}})
	res, err := n.Changes(ChangesParams{Run: id})
	if err != nil || fileOps(res.Files) != "README:delete gen/x.pb.go:add" || res.Hidden != 0 || !res.Files[1].Generated {
		t.Fatalf("%+v %v", res, err)
	}
	if git(t, checkout, "log", "--oneline", "main..tend/t_1") == "" {
		t.Fatal("what the agent left is committed, and counted")
	}
}

// running makes run id of n look running in dir, its base tree taken now.
func running(t *testing.T, n *Node, dir string) string {
	t.Helper()
	id := NewRunID()
	runDir := n.runDir(id)
	os.MkdirAll(runDir, 0o700)
	fileio.WriteJSON(filepath.Join(runDir, "spec.json"), Spec{Run: id, Dir: dir, Created: time.Now(), Work: &Workspace{Checkout: dir}})
	fileio.WriteJSON(filepath.Join(runDir, "state.json"), State{State: StateRunning})
	unlock, err := filelock.Lock(filepath.Join(runDir, "lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
	s := &sup{dir: runDir, spec: Spec{Run: id, Dir: dir}, slim: newSlimmer(runDir, true), log: &rolling{path: filepath.Join(runDir, "output.log")}}
	s.startChanges()
	return id
}

// While a run runs its changes are its workspace now: pages carry the snapshot, and a workspace that moved answers
// snapshot_changed.
func TestChangesWhileRunning(t *testing.T) {
	needGit(t)
	defer func(d time.Duration) { liveReuse = d }(liveReuse)
	liveReuse = 0
	dir := repo(t)
	n := New(t.TempDir())
	id := running(t, n, dir)
	for i := 0; i < changesPage+20; i++ {
		os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(1000+i)), []byte("x\n"), 0o644)
	}
	first, err := n.Changes(ChangesParams{Run: id})
	if err != nil || len(first.Files) != changesPage || first.Next != "f1499" || first.Total.Files != changesPage+20 {
		t.Fatalf("%d files, next %q, %v", len(first.Files), first.Next, err)
	}
	second, err := n.Changes(ChangesParams{Run: id, After: first.Next, Snapshot: first.Snapshot})
	if err != nil || len(second.Files) != 20 || second.Next != "" || second.Files[0].Path != "f1500" {
		t.Fatalf("%+v %v", second, err)
	}
	os.WriteFile(filepath.Join(dir, "README"), []byte("moved on\n"), 0o644)
	if _, err := n.Changes(ChangesParams{Run: id, After: first.Next, Snapshot: first.Snapshot}); codeOf(err) != wire.CodeSnapshotChanged {
		t.Fatalf("the workspace moved: %v", err)
	}
	if _, err := n.Diff(DiffParams{Run: id, Path: "f1000", Snapshot: first.Snapshot}); codeOf(err) != wire.CodeSnapshotChanged {
		t.Fatalf("diff: %v", err)
	}
	if _, err := n.Diff(DiffParams{Run: id, Path: "f1000", Snapshot: first.Snapshot, IgnoreSpace: true}); codeOf(err) != wire.CodeSnapshotChanged {
		t.Fatalf("diff ignoring whitespace: %v", err)
	}
	again, _ := n.Changes(ChangesParams{Run: id})
	if again.Snapshot == first.Snapshot || again.Total.Files != changesPage+21 {
		t.Fatalf("%+v", again.Total)
	}
}

// Outside git the changes are the agent's edits, from each file before its first edit to the file at the end; a
// cleared blob leaves the counts and answers gone for the hunks.
func TestChangesOutsideGit(t *testing.T) {
	dir := t.TempDir()
	x := filepath.Join(dir, "x.txt")
	os.WriteFile(x, []byte("one\ntwo\n"), 0o644)
	n := New(t.TempDir())
	write := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"w","content":"ok"}]},"tool_use_result":{"type":"create","filePath":` +
		jsonStr(filepath.Join(dir, "new.txt")) + `,"content":"n\n","structuredPatch":[],"originalFile":null}}` + "\n"
	id, _ := act(t, n, dir, nil, actScript{
		Write: map[string]string{x: "ONE\nTWO\nthree\n", filepath.Join(dir, "new.txt"): "n\n", filepath.Join(dir, "other.txt"): "not the agent's\n"},
		Say:   editLine(x, "one\ntwo\n") + editLine(x, "one\nTWO\n") + write,
	})
	res, err := n.Changes(ChangesParams{Run: id, All: true})
	if err != nil || res.Git || fileOps(res.Files) != "new.txt:add x.txt:modify" || res.Files[1].Add != 3 || res.Files[1].Del != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	d, err := n.Diff(DiffParams{Run: id, Path: "x.txt"})
	if err != nil || strings.Join(d.Hunks[0].Lines, "|") != "-one|-two|+ONE|+TWO|+three" {
		t.Fatalf("%+v %v", d, err)
	}
	os.WriteFile(x, []byte("changed after the run\n"), 0o644)
	if d2, _ := n.Diff(DiffParams{Run: id, Path: "x.txt"}); strings.Join(d2.Hunks[0].Lines, "|") != "-one|-two|+ONE|+TWO|+three" {
		t.Fatal("an ended run's diff is its end")
	}

	defer func(c int64) { maxNodeBlobs = c }(maxNodeBlobs)
	maxNodeBlobs = 0
	n.TrimBlobs()
	if again, err := n.Changes(ChangesParams{Run: id}); err != nil || len(again.Files) != 2 {
		t.Fatalf("the counts stay: %+v %v", again, err)
	}
	if _, err := n.Diff(DiffParams{Run: id, Path: "x.txt"}); codeOf(err) != wire.CodeGone {
		t.Fatalf("cleared: %v", err)
	}
}

// Ignoring whitespace, a file changed only in its whitespace keeps its place and counts in the list and has no hunks;
// the others' hunks are git diff -w's between the run's trees, paged by the same next, and another snapshot still
// answers snapshot_changed.
func TestDiffIgnoringSpace(t *testing.T) {
	needGit(t)
	dir := repo(t)
	ws, mixed := filepath.Join(dir, "ws.txt"), filepath.Join(dir, "mixed.txt")
	os.WriteFile(ws, []byte("one\ntwo\n"), 0o644)
	os.WriteFile(mixed, []byte(numbered(60)), 0o644)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "--quiet", "-m", "files")
	// every tenth line changes: lines 5, 25 and 45 in their whitespace only, 15, 35 and 55 in their text
	changed := strings.NewReplacer("l5\n", "  l5\n", "l15\n", "L15\n", "l25\n", "l25 \n", "l35\n", "L35\n", "l45\n", "\tl45\n",
		"l55\n", "L55\n").Replace(numbered(60))
	n := New(t.TempDir())
	id, _ := act(t, n, dir, nil, actScript{
		Write: map[string]string{ws: "one  \n\ttwo\n", mixed: changed},
		Say:   editLine(ws, "one\ntwo\n") + editLine(mixed, numbered(60)),
	})
	res, err := n.Changes(ChangesParams{Run: id})
	if err != nil || fileOps(res.Files) != "mixed.txt:modify ws.txt:modify" || res.Files[1].Add != 2 || res.Files[1].Del != 2 ||
		res.Total != (ChangeTotal{2, 8, 8}) {
		t.Fatalf("the list does not ignore whitespace: %+v %v", res, err)
	}
	if d, err := n.Diff(DiffParams{Run: id, Path: "ws.txt", Snapshot: res.Snapshot, IgnoreSpace: true}); err != nil || d.Of != 0 ||
		len(d.Hunks) != 0 || d.Next != nil {
		t.Fatalf("only whitespace changed: %+v %v", d, err)
	}
	if d, _ := n.Diff(DiffParams{Run: id, Path: "ws.txt", Snapshot: res.Snapshot}); d.Of != 1 {
		t.Fatalf("not ignoring: %+v", d)
	}
	want := parseHunks(git(t, dir, "diff", "--no-color", "-w", "-U3", refPrefix+id+"/base", refPrefix+id+"/end", "--", "mixed.txt"))
	if len(want) != 3 {
		t.Fatalf("git -w: %+v", want)
	}
	var got []hunk
	for from := (DiffNext{}); ; {
		d, err := n.Diff(DiffParams{Run: id, Path: "mixed.txt", Snapshot: res.Snapshot, IgnoreSpace: true, Hunk: from.Hunk, Line: from.Line, N: 2})
		if err != nil || d.Of != len(want) {
			t.Fatalf("%+v %v", d, err)
		}
		got = append(got, d.Hunks...)
		if d.Next == nil {
			break
		}
		from = *d.Next
	}
	if joinHunks(got) != joinHunks(want) {
		t.Fatalf("paged:\n%s\ngit -w:\n%s", joinHunks(got), joinHunks(want))
	}
	if d, _ := n.Diff(DiffParams{Run: id, Path: "mixed.txt", Snapshot: res.Snapshot}); d.Of != 6 {
		t.Fatalf("not ignoring, every change: %d", d.Of)
	}
	if _, err := n.Diff(DiffParams{Run: id, Path: "mixed.txt", Snapshot: "other", IgnoreSpace: true}); codeOf(err) != wire.CodeSnapshotChanged {
		t.Fatalf("another snapshot: %v", err)
	}
}

// Outside git, ignoring whitespace is the same comparison: a file changed only in its whitespace has no hunks, and the
// context lines are the new side's.
func TestDiffIgnoringSpaceOutsideGit(t *testing.T) {
	dir := t.TempDir()
	ws, text := filepath.Join(dir, "ws.txt"), filepath.Join(dir, "text.txt")
	os.WriteFile(ws, []byte("a\nb\n"), 0o644)
	os.WriteFile(text, []byte("p\nq\nr\n"), 0o644)
	n := New(t.TempDir())
	id, _ := act(t, n, dir, nil, actScript{
		Write: map[string]string{ws: "a \n\tb\r\n", text: "p \nQ\nr\n"},
		Say:   editLine(ws, "a\nb\n") + editLine(text, "p\nq\nr\n"),
	})
	res, err := n.Changes(ChangesParams{Run: id})
	if err != nil || res.Git || fileOps(res.Files) != "text.txt:modify ws.txt:modify" || res.Files[1].Add != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if d, err := n.Diff(DiffParams{Run: id, Path: "ws.txt", IgnoreSpace: true}); err != nil || d.Of != 0 || len(d.Hunks) != 0 {
		t.Fatalf("only whitespace changed: %+v %v", d, err)
	}
	d, err := n.Diff(DiffParams{Run: id, Path: "text.txt", IgnoreSpace: true})
	if err != nil || d.Of != 1 || d.Hunks[0].At != "@@ -1,3 +1,3 @@" || strings.Join(d.Hunks[0].Lines, "|") != " p |-q|+Q| r" {
		t.Fatalf("%+v %v", d, err)
	}
}

// A page of hunks stops at n hunks or its bytes; a hunk past the bytes goes on by its lines.
func TestDiffPages(t *testing.T) {
	small := hunk{At: "@@ -1 +1 @@", Lines: []string{"-a", "+b"}}
	var long []string
	for i := 0; i < 3000; i++ {
		long = append(long, "+"+strings.Repeat("x", 199))
	}
	huge := hunk{At: "@@ -0,0 +1,3000 @@", Lines: long}
	hs := []hunk{small, small, huge, small}
	p := page(hs, 0, 0, 2)
	if len(p.Hunks) != 2 || p.Of != 4 || *p.Next != (DiffNext{Hunk: 2}) {
		t.Fatalf("%+v", p.Next)
	}
	p = page(hs, 1, 0, 5)
	if len(p.Hunks) != 1 || *p.Next != (DiffNext{Hunk: 2}) {
		t.Fatalf("a hunk that does not fit waits for the next page: %+v", p.Next)
	}
	p = page(hs, 2, 0, 5)
	if len(p.Hunks) != 1 || p.Next == nil || p.Next.Hunk != 2 || p.Next.Line == 0 || p.Next.Line >= 3000 {
		t.Fatalf("%+v", p.Next)
	}
	got := len(p.Hunks[0].Lines)
	for line := p.Next.Line; ; {
		q := page(hs, 2, line, 5)
		got += len(q.Hunks[0].Lines)
		if q.Next == nil || q.Next.Hunk != 2 {
			if got != 3000 || q.Next != nil && *q.Next != (DiffNext{Hunk: 3}) || q.Next == nil && len(q.Hunks) != 2 {
				t.Fatalf("lines %d, next %+v", got, q.Next)
			}
			break
		}
		line = q.Next.Line
	}
	if p := page(hs, 3, 0, 0); len(p.Hunks) != 1 || p.Next != nil {
		t.Fatalf("the last: %+v", p)
	}
}

// A blob is read a page at a time, each page whole characters.
func TestBlobPages(t *testing.T) {
	n := New(t.TempDir())
	id := NewRunID()
	sl := newSlimmer(n.runDir(id), false)
	text := strings.Repeat("é", 10)
	sum, ok := sl.keep([]byte(text))
	if !ok {
		t.Fatal("kept")
	}
	var got strings.Builder
	var off int64
	for {
		b, err := n.BlobOf(BlobParams{Run: id, Sha: sum, Off: off, N: 3})
		if err != nil || b.Size != int64(len(text)) || len(b.Text) != 2 {
			t.Fatalf("%+v %v", b, err)
		}
		got.WriteString(b.Text)
		if b.Next == 0 {
			break
		}
		off = b.Next
	}
	if got.String() != text {
		t.Fatalf("%q", got.String())
	}
	if _, err := n.BlobOf(BlobParams{Run: id, Sha: strings.Repeat("0", 64)}); codeOf(err) != wire.CodeNotFound {
		t.Fatalf("another run's blob: %v", err)
	}
	if _, err := n.BlobOf(BlobParams{Run: id, Sha: "../x"}); codeOf(err) != wire.CodeNotFound {
		t.Fatal("a sha is a sha")
	}
}

// The web client's frames (feat/tend-f1's changes-*.jsonl) decode into these shapes, no field unknown.
func TestChangesFramesDecode(t *testing.T) {
	for _, c := range []struct {
		into any
		raw  string
	}{
		{&Changes{}, `{"files":[{"path":"go.sum","op":"modify","add":14,"del":2,"bytes":9310,"generated":true},{"path":"internal/receipt/pdf.go","op":"modify","add":48,"del":6,"bytes":6120,"agent":true}],"total":{"files":6,"add":3327,"del":1111},"snapshot":"t-9a1","next":"internal/receipt/pdf_test.go","git":true}`},
		{&Changes{}, `{"files":[{"path":"assets/fonts/Inter.ttf","op":"modify","add":0,"del":0,"bytes":14336,"old_bytes":12288,"binary":true},{"path":"internal/receipt/render.go","op":"rename","from":"internal/receipt/draw.go","add":3,"del":3,"bytes":2210,"agent":true},{"path":"g.txt","op":"modify","add":3200,"del":1100,"bytes":402113,"big":true}],"total":{"files":6,"add":3327,"del":1111},"snapshot":"t-9a1","git":true}`},
		{&Changes{}, `{"files":[{"path":"cmd/tend/main.go","op":"modify","add":5,"del":1,"bytes":8800,"agent":true}],"total":{"files":1,"add":5,"del":1},"snapshot":"t-5","git":true,"hidden":4}`},
		{&Diff{}, `{"hunks":[{"at":"@@ -40,7 +40,12 @@ func Render(r Receipt) ([]byte, error) {","lines":[" \tpdf := gofpdf.New(\"P\", \"mm\", \"A4\", \"\")","-\tpdf.SetFont(\"Arial\", \"\", 12)"]}],"of":3,"next":{"hunk":1}}`},
		{&ChangesParams{}, `{"run":"r1","after":"internal/receipt/pdf_test.go"}`},
		{&DiffParams{}, `{"run":"r1","path":"internal/receipt/pdf.go","snapshot":"t-9a1","hunk":0,"n":1}`},
		{&DiffParams{}, `{"run":"r1","path":"internal/receipt/pdf.go","snapshot":"t-9a1","hunk":0,"n":10,"ignore_space":true}`},
	} {
		d := json.NewDecoder(strings.NewReader(c.raw))
		d.DisallowUnknownFields()
		if err := d.Decode(c.into); err != nil {
			t.Errorf("%s: %v", c.raw[:40], err)
		}
	}
}
