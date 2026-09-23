package capture

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractProseToolInputsAndResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"type":"user","timestamp":"2026-09-22T10:00:00Z","message":{"role":"user","content":"分页游标漂移怎么查"}}`,
		`{"type":"assistant","timestamp":"2026-09-22T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"先看排序键"},{"type":"tool_use","name":"Bash","input":{"command":"rg -n cursor api/\nsecond line"}}]}}`,
		`{"type":"user","timestamp":"2026-09-22T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","content":"huge output that must not be indexed"}]}}`,
		`{"type":"assistant","timestamp":"2026-09-22T10:00:03Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/src/page.go"}}]}}`,
	}
	partial := `{"type":"assistant","message":{"content":"half writt`
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"+partial), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []Entry
	upto, owner, err := Extract(context.Background(), path, 0, -1, 0, func(e Entry) { got = append(got, e) })
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range got {
		texts = append(texts, string(e.Role)+":"+e.Text)
	}
	want := []string{"u:分页游标漂移怎么查", "a:先看排序键", "t:Bash rg -n cursor api/", "t:Read /src/page.go"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("entries:\n got %q\nwant %q", texts, want)
	}
	if got[2].Off != got[1].Off || got[3].Off != got[1].Off {
		t.Fatal("a tool call belongs to the message before it")
	}
	full := int64(len(strings.Join(lines, "\n")) + 1)
	if upto != full {
		t.Fatalf("upto %d, want the end of the last complete line %d", upto, full)
	}

	// the writer finishes the line and adds a tool call: the next call resumes and keeps the owner
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`en"}}` + "\n" + `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test"}}]}}` + "\n")
	f.Close()
	got = nil
	if _, _, err := Extract(context.Background(), path, upto, owner, 0, func(e Entry) { got = append(got, e) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "half written" || got[1].Text != "Bash go test" || got[1].Off != got[0].Off {
		t.Fatalf("resumed entries: %+v", got)
	}
}

func TestExtractSkipsOverlongLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	big := `{"type":"user","message":{"content":"` + strings.Repeat("x", extractLineCap+10) + `"}}`
	small := `{"type":"user","message":{"content":"after the blob"}}`
	os.WriteFile(path, []byte(big+"\n"+small+"\n"), 0o644)
	var got []string
	upto, _, err := Extract(context.Background(), path, 0, -1, 0, func(e Entry) { got = append(got, e.Text) })
	if err != nil || len(got) != 1 || got[0] != "after the blob" || upto != int64(len(big)+len(small)+2) {
		t.Fatalf("got %q upto %d err %v", got, upto, err)
	}
}

func TestExtractCodexApplyPatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	lines := []string{
		`{"timestamp":"2026-09-22T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"修一下分页"}]}}`,
		`{"timestamp":"2026-09-22T10:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"c1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: /src/page.go\n@@\n-a\n+b\n*** Add File: /src/cursor.go\n+package src\n*** End Patch\n"}}`,
		`{"timestamp":"2026-09-22T10:00:02Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"Exit code: 0\nSuccess. Updated the following files:\nM /src/page.go"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []Entry
	if _, _, err := Extract(context.Background(), path, 0, -1, 2, func(e Entry) { got = append(got, e) }); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range got {
		texts = append(texts, string(e.Role)+":"+e.Text)
	}
	want := []string{"u:修一下分页", "t:apply_patch /src/page.go /src/cursor.go", "o:Exit code: 0 | Success. Updated the following files:"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("an apply_patch is indexed by the files it touches:\n got %q\nwant %q", texts, want)
	}

	msgs := recentMessages(path, 10)
	if len(msgs) != 1 || len(msgs[0].Steps) != 2 || msgs[0].Steps[0].Tool != "apply_patch" || !strings.HasPrefix(msgs[0].Steps[0].Text, "/src/page.go /src/cursor.go\n") {
		t.Fatalf("the right pane shows the patch step: %+v", msgs)
	}
}
