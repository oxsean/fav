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
	if got := RunOutputLines(evs); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
