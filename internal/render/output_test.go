package render

import (
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/output"
)

func TestRunOutputLinesReadEvents(t *testing.T) {
	zero, one := 0, 1
	evs := []output.Event{
		{Kind: output.KindSys, Name: "init"},
		{Kind: output.KindUser, Text: "fix it"},
		{Kind: output.KindThink, Text: "hmm"},
		{Kind: output.KindSay, Text: "two\n\nlines"},
		{Kind: output.KindTool, Tool: "Bash", Family: output.FamilyShell, Title: "go test ./...", More: 2, Call: "t1"},
		{Kind: output.KindToolResult, Ref: "t1", Output: "ok\n"},
		{Kind: output.KindTool, Tool: "Read", Family: output.FamilyRead, Title: "a.go", Call: "t2"},
		{Kind: output.KindToolResult, Ref: "t2", Output: "x\nno such file\n", Error: true},
		{Kind: output.KindTool, Tool: "Edit", Family: output.FamilyEdit, Title: "a.go +1 −2"},
		{Kind: output.KindTool, Tool: "Bash", Family: output.FamilyAsk, Request: "r1", Title: "rm -rf build"},
		{Kind: output.KindTool, Tool: "AskUserQuestion", Family: output.FamilyAsk, Title: "Which db?", More: 1},
		{Kind: output.KindCmd, Family: output.FamilyShell, Title: "go vet ./...", Exit: &one, Output: "bad\nworse\n", Error: true},
		{Kind: output.KindCmd, Family: output.FamilyShell, Title: "ls", Exit: &zero, Output: "a\n"},
		{Kind: output.KindEdit, Family: output.FamilyEdit, Title: "a.go +2 −1", Files: []string{"a.go", "b.go"}, More: 1},
		{Kind: output.KindMCP, Family: output.FamilyMCP, Title: "gitea.issue_read 6"},
		{Kind: output.KindSys, Name: "warning", Level: "warning", Text: "descriptions shortened"},
		{Kind: output.KindSys, Name: "usage", Usage: &output.Usage{Input: 1}},
		{Kind: output.KindError, Text: "quota"},
		{Kind: output.KindResult, Text: "Done."},
		{Kind: output.KindResult, Text: "usage limit", Error: true},
		{Kind: output.KindResult},
		{Kind: output.KindRaw, Text: `{"hello":"world"}`},
		{Kind: output.KindSay, Temp: true, Text: "half"},
		{Kind: output.KindThink, Temp: true, Text: "pondering"},
		{Kind: output.KindCmd, Temp: true, Family: output.FamilyShell, Title: "go test ./..."},
		{Kind: output.KindCmd, Temp: true, Family: output.FamilyShell, Title: "make", Output: "cc a.c\ncc b.c\nld x\n"},
	}
	want := []string{
		"> fix it", "two", "lines",
		"$ go test ./... " + i18n.F("tasks.output_more_lines", 2),
		"+ Read a.go", "! no such file",
		"~ a.go +1 −2",
		"? rm -rf build", "? Which db? " + i18n.F("tasks.output_more_questions", 1),
		"$ go vet ./... (exit 1)", "! worse",
		"$ ls (exit 0)",
		"~ a.go +2 −1 " + i18n.F("tasks.output_more_files", 1),
		"+ gitea.issue_read 6",
		"- descriptions shortened",
		"! quota", "= Done.", "! usage limit",
		`{"hello":"world"}`, "half",
		"- " + i18n.T("tasks.output_thinking"),
		"$ go test ./... " + i18n.T("tasks.output_running"),
		"$ make " + i18n.T("tasks.output_running"), "  cc a.c", "  cc b.c", "  ld x",
	}
	if got := RunOutputLines(evs, 80); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// An edit step is a row per file it changed, from the node's counts: the path on the left (its start cut when it does
// not fit), +a −d and the hunks on the right, the row exactly width wide; a claude edit's counts are on its result.
func TestRunOutputLinesPutEachEditedFileOnARow(t *testing.T) {
	evs := []output.Event{
		{Kind: output.KindTool, Tool: "Edit", Family: output.FamilyEdit, Title: "internal/a.go +2 −1", Call: "t9"},
		{Kind: output.KindToolResult, Ref: "t9", Output: "ok", Edits: []output.Edit{{Path: "internal/a.go", Op: "modify", Add: 2, Del: 1, Hunks: 3}}},
		{Kind: output.KindEdit, Family: output.FamilyEdit, Title: "docs/new.md +12 −0", More: 2, Edits: []output.Edit{
			{Path: "docs/new.md", Op: "add", Add: 12, Hunks: 1},
			{Path: "cmd/tend/main.go", From: "cmd/tend/old.go", Op: "rename"},
			{Path: "gone.txt", Op: "delete", Del: 4, Hunks: 1},
		}},
		{Kind: output.KindTool, Tool: "Edit", Family: output.FamilyEdit, Title: "b.go +1 −1", Call: "t10"},
	}
	rows := []struct{ head, tail string }{
		{"~ internal/a.go ", "+2 −1 · " + i18n.F("tasks.output_hunks", 3)},
		{"+ docs/new.md ", i18n.F("tasks.output_new_file", 12)},
		{"→ cmd/tend/old.go → cmd/tend/main.go ", "+0 −0"},
		{"− gone.txt ", "+0 −4"},
	}
	const w = 50
	got := RunOutputLines(evs, w)
	if len(got) != len(rows)+1 || got[len(rows)] != "~ b.go +1 −1" {
		t.Fatalf("a row per file, and the call line while its result has not come:\n%s", strings.Join(got, "\n"))
	}
	for i, r := range rows {
		if l := got[i]; Width(l) != w || !strings.HasPrefix(l, r.head) || !strings.HasSuffix(l, "  "+r.tail) {
			t.Errorf("row %d is %q (%d wide), want %q … %q", i, l, Width(l), r.head, r.tail)
		}
	}
	long := []output.Event{{Kind: output.KindEdit, Family: output.FamilyEdit, Edits: []output.Edit{
		{Path: "internal/server/web/pages/deep/file.go", Op: "modify", Add: 1, Del: 1, Hunks: 1}}}}
	if got := RunOutputLines(long, 30); len(got) != 1 || got[0] != "~ …b/pages/deep/file.go  +1 −1" {
		t.Fatalf("a long path keeps its end: %q", got)
	}
}
