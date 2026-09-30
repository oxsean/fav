package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func editsJSON(es []Edit) string {
	b, _ := json.Marshal(es)
	return string(b)
}

func numbered(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(prefix + strings.Repeat("x", 3) + string(rune('0'+i%10)) + "\n")
	}
	return b.String()
}

// claude's structuredPatch gives the hunks: counts from their marks, the first one as the preview under its @@ line.
func TestClaudeEditsCountTheirHunks(t *testing.T) {
	res := `{"filePath":"/w/a.go","oldString":"x","newString":"y","structuredPatch":[` +
		`{"oldStart":1,"oldLines":2,"newStart":1,"newLines":2,"lines":[" keep","-hello","\\ No newline at end of file","+hello world","\\ No newline at end of file"]},` +
		`{"oldStart":9,"oldLines":1,"newStart":9,"newLines":2,"lines":[" nine","+ten"]}]}`
	got := Brief(ClaudeEdits(json.RawMessage(res)))
	want := `[{"path":"/w/a.go","op":"modify","add":2,"del":1,"hunks":2,"preview":["@@ -1,2 +1,2 @@"," keep","-hello","\\ No newline at end of file","+hello world","\\ No newline at end of file"]}]`
	if editsJSON(got) != want {
		t.Fatalf("got  %s\nwant %s", editsJSON(got), want)
	}
	whole := ClaudeEdits(json.RawMessage(res))
	if len(whole) != 1 || len(whole[0].Lines) != 9 || whole[0].Lines[6] != "@@ -9,1 +9,2 @@" {
		t.Fatalf("the whole edit has every hunk: %q", whole[0].Lines)
	}
}

// A new file is all added: its lines are the count, the first PreviewLines of them the preview.
func TestANewFileCountsItsLines(t *testing.T) {
	content := numbered("", 50)
	res, _ := json.Marshal(map[string]any{"type": "create", "filePath": "/w/new.txt", "content": content, "structuredPatch": []any{}, "originalFile": nil})
	got := Brief(ClaudeEdits(res))
	if len(got) != 1 || got[0].Op != "add" || got[0].Add != 50 || got[0].Del != 0 || got[0].Hunks != 1 || !got[0].Cut ||
		len(got[0].Preview) != PreviewLines || got[0].Preview[0] != "@@ -0,0 +1,50 @@" || got[0].Preview[1] != "+xxx1" {
		t.Fatalf("new file: %s", editsJSON(got))
	}
	// a Write over a file is a modification; a result that is no edit is nothing
	upd := `{"type":"update","filePath":"/w/a.txt","content":{"$omit":"content","bytes":3,"lines":1},"structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-a","+b"]}]}`
	if got := Brief(ClaudeEdits(json.RawMessage(upd))); len(got) != 1 || got[0].Op != "modify" || got[0].Add != 1 || got[0].Del != 1 {
		t.Fatalf("write over a file: %s", editsJSON(got))
	}
	for _, res := range []string{`{"type":"text","file":{"filePath":"/w/a","content":"x"}}`, `{"stdout":"ok"}`, `"text"`} {
		if got := ClaudeEdits(json.RawMessage(res)); got != nil {
			t.Errorf("%s: %s", res, editsJSON(got))
		}
	}
}

// A structuredPatch the node moved into a blob comes back as the text of its JSON.
func TestAPatchPutBackAsTextReads(t *testing.T) {
	patch := `[{"oldStart":3,"oldLines":1,"newStart":3,"newLines":1,"lines":["-a","+b"]}]`
	res, _ := json.Marshal(map[string]any{"filePath": "/w/a", "structuredPatch": patch})
	if got := ClaudeEdits(res); len(got) != 1 || got[0].Add != 1 || got[0].Del != 1 || got[0].Hunks != 1 {
		t.Fatalf("got %s", editsJSON(got))
	}
}

// codex's changes: an update is a unified diff, an added file its content as it is, a move a rename.
func TestCodexEditsReadEachKind(t *testing.T) {
	changes := `[{"path":"/w/a.go","kind":{"type":"update","move_path":null},"diff":"@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n@@ -9 +9 @@\n-x\n+y\n"},` +
		`{"path":"/w/notes.txt","kind":{"type":"add"},"diff":"one\ntwo\nthree\n"},` +
		`{"path":"/w/old.go","kind":{"type":"update","move_path":"/w/new.go"},"diff":""},` +
		`{"path":"/w/gone.go","kind":"delete","diff":"x\ny\n"}]`
	got := Brief(CodexEdits(json.RawMessage(changes)))
	want := `[{"path":"/w/a.go","op":"modify","add":2,"del":2,"hunks":2,"preview":["@@ -1,3 +1,3 @@"," a","-b","+B"," c"]},` +
		`{"path":"/w/notes.txt","op":"add","add":3,"del":0,"hunks":1,"preview":["@@ -0,0 +1,3 @@","+one","+two","+three"]},` +
		`{"path":"/w/new.go","op":"rename","from":"/w/old.go","add":0,"del":0,"hunks":0},` +
		`{"path":"/w/gone.go","op":"delete","add":0,"del":2,"hunks":1,"preview":["@@ -1,2 +0,0 @@","-x","-y"]}]`
	if editsJSON(got) != want {
		t.Fatalf("got  %s\nwant %s", editsJSON(got), want)
	}
}

// The previews of one step share PreviewBytes: past it a file has only its counts, a line too long is cut.
func TestPreviewsKeepToTheirBudget(t *testing.T) {
	long := strings.Repeat("y", PreviewBytes)
	var cs []map[string]any
	for _, p := range []string{"/w/a", "/w/b"} {
		cs = append(cs, map[string]any{"path": p, "kind": map[string]any{"type": "update"}, "diff": "@@ -1 +1 @@\n-x\n+" + long + "\n"})
	}
	raw, _ := json.Marshal(cs)
	got := Brief(CodexEdits(raw))
	size := 0
	for _, l := range got[0].Preview {
		size += len(l) + 1
	}
	if !got[0].Cut || size > PreviewBytes || len(got[1].Preview) != 0 || !got[1].Cut || got[1].Add != 1 {
		t.Fatalf("got %d bytes, %s", size, editsJSON(got)[:200])
	}
}

// The node's counts ride on the line (its "tend" field): a result or a codex step carries them, and the codex step
// leaves its hunks to Whole, which reads every one from the line itself.
func TestTheLinesEditsReachTheirEvents(t *testing.T) {
	stat := `"tend":{"edits":[{"path":"/w/a.go","op":"modify","add":2,"del":1,"hunks":3,"preview":["@@ -1 +1 @@","-a","+b"]}]}`
	claude := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]},` +
		`"tool_use_result":{"filePath":"/w/a.go","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-a","+b"]}]},` + stat + `}`
	evs := parsed(claude + "\n")
	if len(evs) != 1 || editsJSON(evs[0].Edits) != `[{"path":"/w/a.go","op":"modify","add":2,"del":1,"hunks":3,"preview":["@@ -1 +1 @@","-a","+b"]}]` {
		t.Fatalf("claude: %s", strip(evs))
	}
	if w := Whole("f", 0, claude); len(w) != 1 || len(w[0].Edits) != 1 || strings.Join(w[0].Edits[0].Lines, "\n") != "@@ -1,1 +1,1 @@\n-a\n+b" {
		t.Fatalf("claude whole: %s", strip(w))
	}
	codex := `{"method":"item/completed","params":{"item":{"type":"fileChange","id":"f1","changes":[{"path":"/w/a.go","kind":{"type":"update"},"diff":"@@ -1 +1 @@\n-a\n+b\n"}],"status":"completed"}},` + stat + `}`
	evs = parsed(codex + "\n")
	if len(evs) != 1 || len(evs[0].Edits[0].Lines) != 0 || evs[0].Title != "/w/a.go +2 −1" || len(evs[0].Edits) != 1 || evs[0].Edits[0].Hunks != 3 {
		t.Fatalf("codex: %s", strip(evs))
	}
	if w := Whole("f", 0, codex); len(w[0].Edits) != 1 || strings.Join(w[0].Edits[0].Lines, "\n") != "@@ -1 +1 @@\n-a\n+b" {
		t.Fatalf("codex whole: %s", strip(w))
	}
}

// A codex step logged before the node counted is read from its diff: a new file's lines are added ones.
func TestAnOldCodexStepCountsItsNewFile(t *testing.T) {
	evs := parsed(`{"method":"item/completed","params":{"item":{"type":"fileChange","id":"f1","changes":[{"path":"notes.txt","kind":{"type":"add"},"diff":"a\nb\n"}],"status":"completed"}}}` + "\n")
	if len(evs) != 1 || evs[0].Title != "notes.txt +2 −0" {
		t.Fatalf("got %s", strip(evs))
	}
	big := `{"method":"item/completed","params":{"item":{"type":"fileChange","id":"f1","changes":[{"path":"a.go","kind":{"type":"update"},"diff":{"$blob":"ab","bytes":70000,"lines":900}}],"status":"completed"}}}`
	if evs := parsed(big + "\n"); len(evs) != 1 || evs[0].Title != "a.go" || len(evs[0].Files) != 1 {
		t.Fatalf("a diff moved to a blob: %s", strip(evs))
	}
}
