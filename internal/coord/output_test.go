package coord

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/tend"
)

// A page of output is the last n events read from the node's log, cut at a line; the page before ends where it
// starts, across as many reads of the node as it takes.
func TestRunOutputComesInPagesOfEvents(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "quick").ID})
	e.wait(r.ID, ended)
	var b strings.Builder
	for i := range 30 {
		b.WriteString(`{"type":"assistant","message":{"content":[{"type":"text","text":"step ` + strconv.Itoa(i) + `"},{"type":"tool_use","id":"t` + strconv.Itoa(i) + `","name":"Bash","input":{"command":"ls"}}]}}` + "\n")
	}
	b.WriteString("half a li")
	log := b.String()
	if err := os.WriteFile(filepath.Join(e.home, "node", "runs", r.ID, "output.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(e.home, "node", "runs", r.ID, "marks.jsonl")) // the turns of another log
	defer func(was int) { outputChunk = was }(outputChunk)
	outputChunk = 300 // a few lines a read

	var last OutputPage
	e.must(MRunOutputPage, OutputPageParams{Run: r.ID, Before: -1, N: 5, Raw: true}, &last)
	at27, end := int64(strings.Index(log, `{"type":"assistant","message":{"content":[{"type":"text","text":"step 27"`)), int64(strings.Index(log, "half"))
	if len(last.Events) != 7 || !last.Events[6].Temp || last.Events[6].Text != "half a li" || last.Events[0].Text != "step 27" ||
		last.From != at27 || last.To != end || last.File == "" || last.Raw != log[at27:] || last.Earliest != 0 {
		t.Fatalf("the last page: from %d to %d, %d events %+v", last.From, last.To, len(last.Events), last.Events)
	}
	if last.Events[0].ID != last.File+":"+strconv.FormatInt(at27, 10)+":0" || last.Events[1].Call != "t27" || last.Events[0].Turn != 0 {
		t.Fatalf("%+v", last.Events[:2])
	}
	var before OutputPage
	e.must(MRunOutputPage, OutputPageParams{Run: r.ID, Before: last.From, N: 60, File: last.File}, &before)
	if len(before.Events) != 54 || before.From != 0 || before.To != last.From || before.Events[0].Text != "step 0" || before.Raw != "" {
		t.Fatalf("the page before: from %d to %d, %d events", before.From, before.To, len(before.Events))
	}
	if err := e.call(MRunOutputPage, OutputPageParams{Run: r.ID, Before: -1, File: "another"}, nil); err == nil {
		t.Fatal("a page of a log that was replaced is stale")
	}
	var none OutputPage
	e.must(MRunOutputPage, OutputPageParams{Run: r.ID, Before: 0}, &none)
	if len(none.Events) != 0 || none.From != 0 {
		t.Fatalf("nothing before the start: %+v", none)
	}
}

// A page starts at the turn the node's marks give where it starts; a line too long to send whole is its head, raw.
func TestAPageKnowsItsTurnsFromTheNodesMarks(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "quick").ID})
	e.wait(r.ID, ended)
	say := func(text string) string {
		return `{"type":"assistant","message":{"content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}
	result := `{"type":"result","subtype":"success","result":"ok"}` + "\n"
	huge := `{"type":"user","big":"` + strings.Repeat("b", 2<<20) + `"}` + "\n"
	lines := []string{`{"type":"system","subtype":"init"}` + "\n", say("one"), result, say("two"), result, huge}
	dir := filepath.Join(e.home, "node", "runs", r.ID)
	path := filepath.Join(dir, "output.log")
	os.Remove(path)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	at := func(i int) int64 { return int64(len(strings.Join(lines[:i], ""))) }
	id := fileio.ID(path)
	var marks strings.Builder
	for _, m := range []string{`"n":1,"off":0`, `"n":1,"phase":"end","off":` + strconv.FormatInt(at(3), 10),
		`"n":2,"off":` + strconv.FormatInt(at(3), 10), `"n":2,"phase":"end","off":` + strconv.FormatInt(at(5), 10)} {
		marks.WriteString(`{"type":"tend","event":"turn",` + m + `,"file":"` + id + `","at":"2026-09-30T00:00:00Z"}` + "\n")
	}
	os.WriteFile(filepath.Join(dir, "marks.jsonl"), []byte(marks.String()), 0o600)

	var last OutputPage
	e.must(MRunOutputPage, OutputPageParams{Run: r.ID, Before: -1, N: 3}, &last)
	if len(last.Events) != 3 || last.Turn != 2 || last.From != at(3) || last.To != at(6) {
		t.Fatalf("from %d to %d, turn %d: %+v", last.From, last.To, last.Turn, last.Events)
	}
	head := last.Events[2]
	if head.Kind != "raw" || head.Turn != 2 || head.Truncated["line"] != len(huge) || len(head.Text) > 16<<10 || last.Events[0].Turn != 2 {
		t.Fatalf("%+v %.100q", head.Truncated, head.Text)
	}
	var before OutputPage
	e.must(MRunOutputPage, OutputPageParams{Run: r.ID, Before: last.From, File: last.File}, &before)
	if len(before.Events) != 3 || before.Turn != 1 || before.Events[2].Turn != 1 || before.Events[2].Kind != "result" {
		t.Fatalf("turn %d: %+v", before.Turn, before.Events)
	}
}
