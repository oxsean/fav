package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/output"
)

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// field reads the JSON value at path in line.
func field(t *testing.T, line []byte, path ...string) json.RawMessage {
	t.Helper()
	var v json.RawMessage = line
	for _, p := range path {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(v, &m); err != nil {
			var a []json.RawMessage
			i, err := strconv.Atoi(p)
			if json.Unmarshal(v, &a) != nil || err != nil || i >= len(a) {
				t.Fatalf("no %s in %s", p, v)
			}
			v = a[i]
			continue
		}
		v = m[p]
	}
	return v
}

func refAt(t *testing.T, line []byte, path ...string) output.Ref {
	t.Helper()
	var r output.Ref
	raw := field(t, line, path...)
	if err := json.Unmarshal(raw, &r); err != nil || r.Blob == "" && r.Omit == "" {
		t.Fatalf("%v is not a ref: %s", path, raw)
	}
	return r
}

func blobsIn(t *testing.T, dir string) []string {
	t.Helper()
	ents, _ := os.ReadDir(filepath.Join(dir, blobsDir))
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func big(prefix string, lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		b.WriteString(prefix + " line of a file that is long enough\n")
	}
	return b.String()
}

func editResult(original, patch string) []byte {
	return []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]},` +
		`"tool_use_result":{"filePath":"/w/a.txt","oldString":"x","newString":"y","originalFile":` + jsonStr(original) +
		`,"structuredPatch":` + patch + `,"userModified":false,"replaceAll":false}}` + "\n")
}

const smallPatch = `[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-x","+y"]}]`

// In git the file before an edit comes from the base tree: originalFile is left out whatever its size, nothing is
// kept for it.
func TestSlimInGitOmitsTheOriginal(t *testing.T) {
	dir := t.TempDir()
	sl := newSlimmer(dir, true)
	for _, original := range []string{"x", big("x", 200)} {
		out, keep, _ := sl.line(editResult(original, smallPatch))
		if !keep {
			t.Fatal("the line stays")
		}
		r := refAt(t, out, "tool_use_result", "originalFile")
		if r.Omit != "originalFile" || r.Bytes != len(original) || r.Blob != "" {
			t.Fatalf("ref %+v", r)
		}
		if string(field(t, out, "tool_use_result", "structuredPatch")) != smallPatch {
			t.Fatalf("the patch stays: %s", out)
		}
	}
	if b := blobsIn(t, dir); len(b) != 0 {
		t.Fatalf("blobs %v", b)
	}
}

// Outside git the original moves into a blob; the blob is whole when the line naming it comes back.
func TestSlimOutsideGitKeepsBlobs(t *testing.T) {
	dir := t.TempDir()
	sl := newSlimmer(dir, false)
	original := big("x", 200)
	out, _, _ := sl.line(editResult(original, smallPatch))
	r := refAt(t, out, "tool_use_result", "originalFile")
	if r.Blob != sha(original) || r.Bytes != len(original) || r.Lines != 200 {
		t.Fatalf("ref %+v", r)
	}
	got, err := os.ReadFile(filepath.Join(dir, blobsDir, r.Blob))
	if err != nil || string(got) != original {
		t.Fatalf("blob %q %v", got, err)
	}
	// the same content again is kept once and counted once
	used := sl.used
	sl.line(editResult(original, smallPatch))
	if b := blobsIn(t, dir); len(b) != 1 || sl.used != used {
		t.Fatalf("blobs %v, used %d then %d", b, used, sl.used)
	}
	// a small string stays in the line
	out, _, _ = sl.line(editResult("tiny", smallPatch))
	if string(field(t, out, "tool_use_result", "originalFile")) != `"tiny"` {
		t.Fatalf("small original: %s", out)
	}
}

func writeResult(kind, content, original string) []byte {
	orig := "null"
	if original != "" {
		orig = jsonStr(original)
	}
	return []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"}]},` +
		`"tool_use_result":{"type":` + jsonStr(kind) + `,"filePath":"/w/b.txt","content":` + jsonStr(content) +
		`,"structuredPatch":[],"originalFile":` + orig + `}}` + "\n")
}

// A new file's content is kept in a blob in git too (it may be changed again before the run ends); an overwritten
// file's is in git.
func TestSlimWrite(t *testing.T) {
	content := big("w", 150)
	for _, c := range []struct {
		git        bool
		kind, orig string
		blob       bool
	}{{true, "create", "", true}, {true, "update", big("o", 150), false}, {false, "create", "", true}, {false, "update", big("o", 150), true}} {
		dir := t.TempDir()
		out, _, _ := newSlimmer(dir, c.git).line(writeResult(c.kind, content, c.orig))
		r := refAt(t, out, "tool_use_result", "content")
		if c.blob != (r.Blob == sha(content)) || !c.blob && r.Omit != "content" || r.Lines != 150 {
			t.Fatalf("git %v %s: %+v", c.git, c.kind, r)
		}
	}
}

// What Read read, what a tool call writes, and a patch past 64 KiB move into blobs; the step's title still counts
// the lines.
func TestSlimReadCallsAndPatches(t *testing.T) {
	dir := t.TempDir()
	sl := newSlimmer(dir, true)
	text := big("r", 300)
	read := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t3","content":"1→…"}]},` +
		`"tool_use_result":{"type":"text","file":{"filePath":"/w/c.txt","content":` + jsonStr(text) + `,"numLines":300}}}` + "\n")
	out, _, _ := sl.line(read)
	if r := refAt(t, out, "tool_use_result", "file", "content"); r.Blob != sha(text) {
		t.Fatalf("read %+v", r)
	}
	if string(field(t, out, "tool_use_result", "file", "numLines")) != "300" {
		t.Fatal("the other fields stay")
	}

	call := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"writing"},{"type":"tool_use","id":"t4","name":"Write","input":{"file_path":"/w/d.go","content":` +
		jsonStr(text) + `}}]}}` + "\n")
	out, _, _ = sl.line(call)
	if r := refAt(t, out, "message", "content", "1", "input", "content"); r.Blob != sha(text) || r.Lines != 300 {
		t.Fatalf("call %+v in %s", r, out)
	}
	evs, _, _ := output.Parse("f", 0, string(out), output.State{})
	found := false
	for _, e := range evs {
		if e.Kind == output.KindTool || e.Kind == output.KindEdit {
			found = found || e.Title == "/w/d.go +300 −0"
		}
	}
	if !found {
		t.Fatalf("title from the ref: %+v", evs)
	}

	var hunk []string
	for i := 0; i < 3000; i++ {
		hunk = append(hunk, "+ a line added to the file that makes the patch long")
	}
	patch, _ := json.Marshal([]map[string]any{{"oldStart": 1, "oldLines": 0, "newStart": 1, "newLines": len(hunk), "lines": hunk}})
	out, _, _ = sl.line(editResult("x", string(patch)))
	r := refAt(t, out, "tool_use_result", "structuredPatch")
	if got, _ := os.ReadFile(filepath.Join(dir, blobsDir, r.Blob)); string(got) != string(patch) {
		t.Fatalf("patch blob: %d bytes", len(got))
	}
}

// Lines slimming has nothing for pass unchanged, byte for byte.
func TestSlimLeavesOtherLines(t *testing.T) {
	sl := newSlimmer(t.TempDir(), false)
	for _, l := range []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi <b>"}]}}` + "\n",
		`{"type":"user","message":{"content":"say \"tool_use_result\""}}` + "\n",
		"plain text\n",
		`{"method":"item/completed","params":{"item":{"type":"agentMessage","text":"done"}}}` + "\n",
	} {
		out, keep, m := sl.line([]byte(l))
		if !keep || string(out) != l || m != nil {
			t.Fatalf("%s became %s", l, out)
		}
	}
}

// Past the run's cap nothing more is kept: the field is left out with cap, and the line is still logged.
func TestSlimRunCap(t *testing.T) {
	defer func(c int64) { maxRunBlobs = c }(maxRunBlobs)
	dir := t.TempDir()
	first, second := big("a", 200), big("b", 200)
	maxRunBlobs = int64(len(first) + len(second)/2)
	sl := newSlimmer(dir, false)
	sl.line(editResult(first, smallPatch))
	out, keep, _ := sl.line(editResult(second, smallPatch))
	r := refAt(t, out, "tool_use_result", "originalFile")
	if !keep || !r.Cap || r.Omit != "originalFile" || r.Blob != "" || r.Bytes != len(second) {
		t.Fatalf("at the cap: %+v", r)
	}
	if b := blobsIn(t, dir); len(b) != 1 || b[0] != sha(first) {
		t.Fatalf("blobs %v", b)
	}
	// a supervisor starting on the run counts what is there already
	if again := newSlimmer(dir, false); again.used != int64(len(first)) {
		t.Fatalf("used %d", again.used)
	}
}

func codexLine(method, params string) []byte {
	return []byte(`{"method":"` + method + `","params":` + params + `}` + "\n")
}

// codex's turn diff is the whole turn's so far each time: only the last is kept, written once the turn completes,
// and marked with its counts; patch updates stay out of the log.
func TestSlimCodexTurnDiff(t *testing.T) {
	dir := t.TempDir()
	sl := newSlimmer(dir, true)
	d1 := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+A\n"
	d2 := d1 + "diff --git a/n.txt b/n.txt\nnew file mode 100644\n--- /dev/null\n+++ b/n.txt\n@@ -0,0 +1,2 @@\n+1\n+2\n"
	for _, d := range []string{d1, d1, d2} {
		if _, keep, _ := sl.line(codexLine("turn/diff/updated", `{"threadId":"th","turnId":"tu1","diff":`+jsonStr(d)+`}`)); keep {
			t.Fatal("a turn diff stays out of the log")
		}
	}
	if _, keep, _ := sl.line(codexLine("item/fileChange/patchUpdated", `{"turnId":"tu1","itemId":"i","changes":[]}`)); keep {
		t.Fatal("a patch update stays out of the log")
	}
	done := codexLine("turn/completed", `{"threadId":"th","turn":{"id":"tu1","status":"completed"}}`)
	out, keep, m := sl.line(done)
	if !keep || string(out) != string(done) {
		t.Fatalf("turn/completed is logged as it is: %s", out)
	}
	if m == nil || m.Event != markDiff || m.ID != "tu1" || m.Files != 2 || m.Add != 3 || m.Del != 1 {
		t.Fatalf("mark %+v", m)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, diffsDir, "turn-tu1.patch")); string(got) != d2 {
		t.Fatalf("patch %q", got)
	}
	// a turn without a diff marks nothing
	if _, _, m := sl.line(codexLine("turn/completed", `{"turn":{"id":"tu2"}}`)); m != nil {
		t.Fatalf("mark %+v", m)
	}
}

// A file change's diff past 64 KiB moves into a blob.
func TestSlimCodexFileChange(t *testing.T) {
	dir := t.TempDir()
	d := strings.Repeat("+a long line added by the change to the file\n", 2000)
	out, _, _ := newSlimmer(dir, true).line(codexLine("item/completed", `{"item":{"type":"fileChange","id":"f1","changes":[{"path":"/w/a.txt","kind":{"type":"add"},"diff":`+jsonStr(d)+`}],"status":"completed"}}`))
	if r := refAt(t, out, "params", "item", "changes", "0", "diff"); r.Blob != sha(d) || r.Lines != 2000 {
		t.Fatalf("ref %+v", r)
	}
}

// The supervisor logs a run's output slimmed: in a directory outside git the original is in a blob, inside git it is
// left out.
func TestSuperviseSlimsTheLog(t *testing.T) {
	needGit(t)
	original := big("s", 200)
	said := filepath.Join(t.TempDir(), "said.jsonl")
	os.WriteFile(said, editResult(original, smallPatch), 0o600)
	checkout := repo(t)
	for _, c := range []struct {
		dir  string
		blob bool
	}{{t.TempDir(), true}, {checkout, false}} {
		n := New(t.TempDir())
		id := NewRunID()
		dir := n.runDir(id)
		os.MkdirAll(dir, 0o700)
		writeJSON(filepath.Join(dir, "spec.json"), Spec{Run: id, Argv: []string{os.Args[0], "_cat", said}, Dir: c.dir, Runner: RunnerBackground, Created: time.Now()})
		os.WriteFile(filepath.Join(dir, "prompt.md"), nil, 0o600)
		if err := Supervise(dir); err != nil {
			t.Fatal(err)
		}
		log, _ := os.ReadFile(filepath.Join(dir, "output.log"))
		r := refAt(t, bytes.TrimSpace(log), "tool_use_result", "originalFile")
		if c.blob != (r.Blob == sha(original)) || !c.blob && r.Omit != "originalFile" {
			t.Fatalf("blob %v: %+v", c.blob, r)
		}
		if c.blob {
			if got, _ := os.ReadFile(filepath.Join(dir, blobsDir, r.Blob)); string(got) != original {
				t.Fatal("the blob holds the original")
			}
		}
	}
}

// Outside git, codex's file change about to happen keeps the file as it is as its base; a command's paths under the
// run's directory are recorded, others and flags not.
func TestTouchCodex(t *testing.T) {
	runDir, dir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old\n"), 0o644)
	sl := newSlimmer(runDir, false)
	sl.cwd = dir
	sl.line(codexLine("item/started", `{"item":{"type":"fileChange","id":"f","changes":[{"path":`+jsonStr(filepath.Join(dir, "a.txt"))+`,"kind":{"type":"update"},"diff":"-old\n+new\n"},{"path":"b.txt","kind":{"type":"add"},"diff":"+b\n"}]}}`))
	sl.line(codexLine("item/completed", `{"item":{"type":"fileChange","id":"f","changes":[{"path":`+jsonStr(filepath.Join(dir, "a.txt"))+`,"kind":{"type":"update"},"diff":"-old\n+new\n"}]}}`))
	sl.line(codexLine("item/started", `{"item":{"type":"commandExecution","id":"c","command":"sed -i -e s/a/b/ sub/y.txt /etc/hosts > out.log","cwd":`+jsonStr(dir)+`}}`))
	var got []string
	for _, tc := range readTouched(runDir) {
		rel, _ := filepath.Rel(dir, tc.Path)
		got = append(got, tc.Via+":"+filepath.ToSlash(rel))
		if rel == "a.txt" && tc.Base != sha("old\n") || rel == "b.txt" && !tc.New {
			t.Errorf("%+v", tc)
		}
	}
	if strings.Join(got, " ") != "edit:a.txt edit:b.txt cmd:s/a/b cmd:sub/y.txt cmd:out.log" {
		t.Fatalf("%q", got)
	}
}

// An edit's counts and first hunk are written on its line before slimming takes the hunks away, so the timeline has
// them however big the patch: claude's result, codex's finished fileChange.
func TestSlimCountsEditsOnTheLine(t *testing.T) {
	sl := newSlimmer(t.TempDir(), true)
	var hunk []string
	for i := 0; i < 3000; i++ {
		hunk = append(hunk, "+ a line added to the file that makes the patch long")
	}
	patch, _ := json.Marshal([]map[string]any{{"oldStart": 1, "oldLines": 0, "newStart": 1, "newLines": len(hunk), "lines": hunk},
		{"oldStart": 5000, "oldLines": 1, "newStart": 8000, "newLines": 0, "lines": []string{"-gone"}}})
	out, _, _ := sl.line(editResult(big("x", 200), string(patch)))
	refAt(t, out, "tool_use_result", "structuredPatch")
	var st output.Stat
	if err := json.Unmarshal(field(t, out, "tend"), &st); err != nil || len(st.Edits) != 1 {
		t.Fatalf("tend: %s", field(t, out, "tend"))
	}
	e := st.Edits[0]
	if e.Path != "/w/a.txt" || e.Op != "modify" || e.Add != 3000 || e.Del != 1 || e.Hunks != 2 || len(e.Preview) != output.PreviewLines || !e.Cut || len(e.Lines) != 0 {
		t.Fatalf("claude edit %+v", e)
	}
	evs, _, _ := output.Parse("f", 0, string(out), output.State{})
	if len(evs) != 1 || len(evs[0].Edits) != 1 || evs[0].Edits[0].Add != 3000 {
		t.Fatalf("parsed %+v", evs)
	}

	diff := "@@ -1,2 +1,2 @@\n-" + strings.Repeat("o", 70<<10) + "\n+n\n"
	line := `{"method":"item/completed","params":{"item":{"type":"fileChange","id":"f1","changes":[{"path":"/w/a.go","kind":{"type":"update"},"diff":` + jsonStr(diff) + `}],"status":"completed"}}}` + "\n"
	out, _, _ = sl.line([]byte(line))
	refAt(t, out, "params", "item", "changes", "0", "diff")
	st = output.Stat{}
	json.Unmarshal(field(t, out, "tend"), &st)
	if len(st.Edits) != 1 || st.Edits[0].Add != 1 || st.Edits[0].Del != 1 || st.Edits[0].Hunks != 1 || len(st.Edits[0].Preview) != 2 {
		t.Fatalf("codex edit %s", field(t, out, "tend"))
	}
	// started, the item has not changed anything yet
	started := strings.Replace(line, "item/completed", "item/started", 1)
	if out, _, _ = sl.line([]byte(started)); bytes.Contains(out, []byte(`"tend"`)) {
		t.Fatal("item/started is not counted")
	}
}
