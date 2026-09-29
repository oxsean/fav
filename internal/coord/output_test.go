package coord

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
