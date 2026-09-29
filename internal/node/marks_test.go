package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/wire"
)

func marksOf(t *testing.T, n *Node, run string) []Mark {
	t.Helper()
	ms, _ := linesFrom(filepath.Join(n.runDir(run), marksFile), 0, func(Mark) bool { return true })
	return ms
}

// A page read from any line on starts at the turn the whole log gives that line.
func TestMarksPlaceARunsTurnsAndHooks(t *testing.T) {
	n := New(t.TempDir())
	n.Limits.AllowHooks = true
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "3", "--every", "300ms", "--stderr", "a warning"), Brief: "b",
		Check: []string{os.Args[0], "_say", "checked", "and said so"}})
	wait(t, n, s.Run, func(s Snapshot) bool { return s.State.State == StateRunning })
	if _, err := n.Send(SendParams{Run: s.Run, Send: agent.Send{ID: "m1", Text: "one"}}); err != nil {
		t.Fatal(err)
	}
	wait(t, n, s.Run, func(s Snapshot) bool { return s.Usage != nil })
	if _, err := n.Send(SendParams{Run: s.Run, Send: agent.Send{ID: "m2", Text: "two"}}); err != nil {
		t.Fatal(err)
	}
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateExited {
		t.Fatalf("%+v", end)
	}
	log := logOf(t, n, s.Run)
	id := fileio.ID(filepath.Join(n.runDir(s.Run), "output.log"))
	whole, _, _ := output.Parse(id, 0, log, output.State{})
	if last := whole[len(whole)-1]; last.Turn < 3 {
		t.Fatalf("two turns, and the check's lines open a third: %+v", last)
	}
	var events []string
	for _, m := range marksOf(t, n, s.Run) {
		if m.Type != "tend" || m.File != id || m.At.IsZero() {
			t.Fatalf("%+v", m)
		}
		events = append(events, m.Event+"/"+m.Phase)
	}
	for _, want := range []string{"start/", "turn/", "turn/end", "exit/", "hook/begin", "hook/end"} {
		if !slices.Contains(events, want) {
			t.Fatalf("%s missing: %v", want, events)
		}
	}
	for off := int64(0); off < int64(len(log)); off += int64(strings.IndexByte(log[off:], '\n')) + 1 {
		page, err := n.Tail(TailParams{Run: s.Run, Before: -1, Max: len(log) - int(off) + 1})
		if err != nil || page.From != off || page.Turn == nil {
			t.Fatalf("a page from %d: %+v %v", off, page, err)
		}
		evs, _, _ := output.Parse(id, page.From, page.Text, *page.Turn)
		for i, e := range evs {
			j := slices.IndexFunc(whole, func(w output.Event) bool { return w.ID == e.ID })
			if j < 0 || whole[j].Turn != e.Turn {
				t.Fatalf("a page from %d: event %d %+v is in turn %d, the whole log says %+v", off, i, e, e.Turn, whole[j])
			}
		}
	}
}

func TestTailReadsTheRolledLogByItsID(t *testing.T) {
	n := New(t.TempDir())
	dir := n.runDir("r_000000000001")
	os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, "output.log")
	os.WriteFile(path, []byte("old one\nold two\n"), 0o600)
	os.Rename(path, path+".1")
	os.WriteFile(path, []byte("new one\n"), 0o600)
	older, newer := fileio.ID(path+".1"), fileio.ID(path)
	cur, err := n.Tail(TailParams{Run: "r_000000000001", Before: -1})
	if err != nil || cur.Text != "new one\n" || cur.File != newer || !cur.Done || cur.Prev != older {
		t.Fatalf("%+v %v", cur, err)
	}
	old, err := n.Tail(TailParams{Run: "r_000000000001", Before: -1, File: cur.Prev})
	if err != nil || old.Text != "old one\nold two\n" || old.File != older || !old.Done || old.Prev != "" {
		t.Fatalf("%+v %v", old, err)
	}
	if _, err := n.Tail(TailParams{Run: "r_000000000001", Before: -1, File: "1:2"}); wire.Code(err) != wire.CodeStale {
		t.Fatalf("a log no longer kept: %v", err)
	}
}

// Lines past what is sent whole are cut: a JSON line keeps its shape, anything else only its start.
func TestTailSendsLongLinesCut(t *testing.T) {
	n := New(t.TempDir())
	dir := n.runDir("r_000000000001")
	os.MkdirAll(dir, 0o700)
	short := `{"type":"system","subtype":"init"}` + "\n"
	field := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + strings.Repeat("a", 100<<10) + `"}]},"note":"<&>"}` + "\n"
	huge := `{"type":"user","big":"` + strings.Repeat("b", 2<<20) + `"}` + "\n"
	plain := strings.Repeat("c", 20<<10) + "\n"
	half := strings.Repeat("d", 20<<10)
	os.WriteFile(filepath.Join(dir, "output.log"), []byte(short+field+huge+plain+half), 0o600)

	last, err := n.Tail(TailParams{Run: "r_000000000001", Before: -1, Max: 64 << 10, Clip: true})
	hugeAt := int64(len(short + field))
	if err != nil || last.From != hugeAt || len(last.Lines) != 2 || last.Text != "" {
		t.Fatalf("%d lines from %d: %v", len(last.Lines), last.From, err)
	}
	if h := last.Lines[0]; !h.Head || h.Off != hugeAt || h.Size != int64(len(huge)) || h.Text != huge[:maxSentField] {
		t.Fatalf("a line too long to send is only its start: %+v", h.Off)
	}
	if p := last.Lines[1]; !p.Head || p.Off != hugeAt+int64(len(huge)) || p.Size != int64(len(plain)) || len(p.Text) != maxSentField {
		t.Fatalf("a long line that is not JSON is only its start: %d %d", p.Off, len(p.Text))
	}
	first, err := n.Tail(TailParams{Run: "r_000000000001", Before: last.From, Max: 1 << 20, Clip: true})
	if err != nil || first.From != 0 || !first.Done || len(first.Lines) != 2 || first.Lines[0].Text != short || first.Lines[0].Size != 0 {
		t.Fatalf("%+v %v", first.Lines, err)
	}
	cut := first.Lines[1]
	var m struct {
		Message struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
		Note string `json:"note"`
	}
	if cut.Head || cut.Off != int64(len(short)) || cut.Size != int64(len(field)) || !strings.HasSuffix(cut.Text, "\n") ||
		json.Unmarshal([]byte(cut.Text), &m) != nil || len(m.Message.Content[0].Text) > maxSentField+len("…") || !strings.Contains(cut.Text, `"<&>"`) {
		t.Fatalf("a long JSON line keeps its shape with its strings cut: %d bytes %.200q", len(cut.Text), cut.Text)
	}
}

func TestALineIsReadWholeFromWhereItStarts(t *testing.T) {
	n := New(t.TempDir())
	dir := n.runDir("r_000000000001")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "output.log"), []byte("one\n"+strings.Repeat("x", 100)+"\nthree"), 0o600)
	id := fileio.ID(filepath.Join(dir, "output.log"))
	l, err := n.Line(LineParams{Run: "r_000000000001", File: id, Off: 4})
	if err != nil || l.Text != strings.Repeat("x", 100)+"\n" || l.Size != 101 || l.File != id {
		t.Fatalf("%+v %v", l, err)
	}
	if l, err = n.Line(LineParams{Run: "r_000000000001", Off: 4, Max: 10}); err != nil || l.Text != strings.Repeat("x", 10) || l.Size != 101 {
		t.Fatalf("a line longer than max: %+v %v", l, err)
	}
	if l, err = n.Line(LineParams{Run: "r_000000000001", Off: 105}); err != nil || l.Text != "three" || l.Size != 5 {
		t.Fatalf("a line still being written: %+v %v", l, err)
	}
	if _, err = n.Line(LineParams{Run: "r_000000000001", File: "1:2", Off: 0}); wire.Code(err) != wire.CodeStale {
		t.Fatalf("%v", err)
	}
}
