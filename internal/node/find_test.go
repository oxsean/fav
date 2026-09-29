package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/wire"
)

const findRun = "r_000000000001"

func logWith(t *testing.T, n *Node, lines ...string) string {
	t.Helper()
	dir := n.runDir(findRun)
	os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, "output.log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fileio.ID(path)
}

func say(text string) string {
	b, _ := json.Marshal(text)
	return `{"type":"assistant","message":{"content":[{"type":"text","text":` + string(b) + `}]}}`
}

func find(t *testing.T, n *Node, p FindParams) Found {
	t.Helper()
	p.Run = findRun
	if p.Before == 0 && p.File == "" {
		p.Before = -1
	}
	f, err := n.Find(context.Background(), p)
	if err != nil {
		t.Fatalf("%q: %v", p.Q, err)
	}
	return f
}

func offOf(lines []string, i int) int64 {
	return int64(len(strings.Join(lines[:i], "\n")) + min(i, 1)*1)
}

// Only the text a viewer sees is searched: not the JSON around it, not what a page cuts.
func TestFindMatchesOnlyWhatIsShown(t *testing.T) {
	n := New(t.TempDir())
	var out strings.Builder
	for i := range 200 {
		fmt.Fprintf(&out, "out %04d\n", i)
		if i == 100 {
			out.WriteString("MIDDLE\n")
		}
	}
	result, _ := json.Marshal(out.String())
	lines := []string{
		say("He said \"run it\"\nthen Left"),
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go test ./pay/..."}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"--- FAIL: TestPay\n"}]}}`,
		"中文 输出 <div>",
		`{"type":"assistant","message":{"content":[{"type":"text","text":"a <b> tag"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_2","content":` + string(result) + `}]}}`,
		say(strings.Repeat("x", 20<<10) + " TAILWORD"),
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_3","name":"AskUserQuestion","input":{"questions":[{"question":"Which port?"}]}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_4","name":"TodoWrite","input":{"todos":[{"content":"wire the handler","status":"pending"}]}}]}}`,
	}
	file := logWith(t, n, lines...)
	for _, c := range []struct {
		q, want, kind, ref string
	}{
		{"tool", "", "", ""},
		{"type", "", "", ""},
		{"\"run it\"\nthen", "0", "say", ""},
		{"  left ", "0", "say", ""},
		{"GO TEST", "1", "tool", ""},
		{"fail: testpay", "2", "tool_result", "toolu_1"},
		{"输出 <div>", "3", "say", ""},
		{"<B>", "4", "say", ""},
		{"out 0001", "5", "tool_result", "toolu_2"},
		{"MIDDLE", "", "", ""},
		{"TAILWORD", "", "", ""},
		{"which port", "7", "tool", ""},
		{"wire the", "8", "tool", ""},
	} {
		f := find(t, n, FindParams{Q: c.q})
		if f.File != file || f.Next != nil {
			t.Fatalf("%q: %+v", c.q, f)
		}
		if c.want == "" {
			if len(f.Hits) != 0 {
				t.Fatalf("%q is not shown, found %+v", c.q, f.Hits)
			}
			continue
		}
		var i int
		fmt.Sscan(c.want, &i)
		want := fmt.Sprintf("%s:%d:0", file, offOf(lines, i))
		if len(f.Hits) != 1 {
			t.Fatalf("%q: %+v", c.q, f.Hits)
		}
		h := f.Hits[0]
		if h.ID != want || h.Kind != c.kind || h.Ref != c.ref || h.Turn != 1 ||
			!strings.Contains(strings.ToLower(h.Excerpt), strings.ToLower(strings.TrimSpace(c.q))) {
			t.Fatalf("%q: %+v, want %s", c.q, h, want)
		}
	}
}

func TestFindExcerptsAroundTheHit(t *testing.T) {
	n := New(t.TempDir())
	logWith(t, n, say(strings.Repeat("前", 300)+"needle"+strings.Repeat("后", 300)))
	h := find(t, n, FindParams{Q: "needle"}).Hits[0]
	if n := len([]rune(h.Excerpt)); n > excerptRunes+2 || !strings.HasPrefix(h.Excerpt, "…") || !strings.HasSuffix(h.Excerpt, "…") ||
		!strings.Contains(h.Excerpt, "前needle后") {
		t.Fatalf("%d runes: %q", n, h.Excerpt)
	}
}

// Pages of hits go back from the end through the log before this one, the nearest first; a line's hits stay together.
func TestFindPagesBackAcrossTheLogs(t *testing.T) {
	n := New(t.TempDir())
	oldLines := []string{"hit old 1", "miss", "hit old 2", "hit old 3"}
	logWith(t, n, oldLines...)
	path := filepath.Join(n.runDir(findRun), "output.log")
	os.Rename(path, path+".1")
	older := fileio.ID(path + ".1")
	both := `{"type":"assistant","message":{"content":[{"type":"text","text":"hit new 2a"},{"type":"text","text":"hit new 2b"}]}}`
	newLines := []string{"hit new 1", both, "hit new 3"}
	newer := logWith(t, n, newLines...)
	id := func(file string, lines []string, i, k int) string {
		return fmt.Sprintf("%s:%d:%d", file, offOf(lines, i), k)
	}
	ids := func(f Found) []string {
		var out []string
		for _, h := range f.Hits {
			out = append(out, h.ID)
		}
		return out
	}
	check := func(f Found, want []string, next *FindAt) {
		t.Helper()
		if strings.Join(ids(f), " ") != strings.Join(want, " ") || (f.Next == nil) != (next == nil) || next != nil && *f.Next != *next {
			t.Fatalf("%v next %+v, want %v next %+v", ids(f), f.Next, want, next)
		}
	}

	f := find(t, n, FindParams{Q: "hit", Limit: 2})
	check(f, []string{id(newer, newLines, 1, 0), id(newer, newLines, 1, 1), id(newer, newLines, 2, 0)}, &FindAt{File: newer, Before: offOf(newLines, 1)})
	f = find(t, n, FindParams{Q: "hit", Limit: 2, File: f.Next.File, Before: f.Next.Before})
	check(f, []string{id(older, oldLines, 3, 0), id(newer, newLines, 0, 0)}, &FindAt{File: older, Before: offOf(oldLines, 3)})
	f = find(t, n, FindParams{Q: "hit", Limit: 2, File: f.Next.File, Before: f.Next.Before})
	check(f, []string{id(older, oldLines, 0, 0), id(older, oldLines, 2, 0)}, nil)

	check(find(t, n, FindParams{Q: "hit", Limit: 3}), []string{id(newer, newLines, 0, 0), id(newer, newLines, 1, 0), id(newer, newLines, 1, 1),
		id(newer, newLines, 2, 0)}[1:], &FindAt{File: newer, Before: offOf(newLines, 1)})
	check(find(t, n, FindParams{Q: "hit", Limit: 4}), []string{id(newer, newLines, 0, 0), id(newer, newLines, 1, 0), id(newer, newLines, 1, 1),
		id(newer, newLines, 2, 0)}, &FindAt{File: newer, Before: 0})
	check(find(t, n, FindParams{Q: "hit", File: newer, Before: 0}), []string{id(older, oldLines, 0, 0), id(older, oldLines, 2, 0),
		id(older, oldLines, 3, 0)}, nil)
	check(find(t, n, FindParams{Q: "hit", File: older, Before: -1}), []string{id(older, oldLines, 0, 0), id(older, oldLines, 2, 0),
		id(older, oldLines, 3, 0)}, nil)
}

func TestFindTellsTheTurnFromTheMarks(t *testing.T) {
	n := New(t.TempDir())
	lines := []string{say("first"), `{"type":"result","result":"done"}`, say("second hit")}
	file := logWith(t, n, lines...)
	var marks []byte
	for _, m := range []Mark{{Event: markTurn, N: 1}, {Event: markTurn, N: 1, Phase: phaseEnd, Off: offOf(lines, 2)}, {Event: markTurn, N: 2, Off: offOf(lines, 2)}} {
		m.Type, m.File, m.At = "tend", file, time.Now()
		b, _ := json.Marshal(m)
		marks = append(append(marks, b...), '\n')
	}
	os.WriteFile(filepath.Join(n.runDir(findRun), marksFile), marks, 0o600)
	if h := find(t, n, FindParams{Q: "hit"}).Hits; len(h) != 1 || h[0].Turn != 2 {
		t.Fatalf("%+v", h)
	}
}

func TestFindRefuses(t *testing.T) {
	n := New(t.TempDir())
	logWith(t, n, "a line")
	for _, p := range []FindParams{
		{Run: findRun, Q: "  ", Before: -1},
		{Run: findRun, Q: strings.Repeat("q", maxFindQ+1), Before: -1},
		{Run: "../x", Q: "a", Before: -1},
	} {
		if _, err := n.Find(context.Background(), p); wire.Code(err) != wire.CodeBadRequest {
			t.Fatalf("%.20q: %v", p.Q, err)
		}
	}
	if _, err := n.Find(context.Background(), FindParams{Run: findRun, Q: "a", File: "1:2", Before: -1}); wire.Code(err) != wire.CodeStale {
		t.Fatal(err)
	}
	if f, err := n.Find(context.Background(), FindParams{Run: "r_000000000002", Q: "a", Before: -1}); err != nil || len(f.Hits) != 0 {
		t.Fatalf("a run without output: %+v %v", f, err)
	}
}

// Two finds run at once; a third waits for one of them, until its caller gives up.
func TestFindWaitsForOneOfTwoSlots(t *testing.T) {
	n := New(t.TempDir())
	logWith(t, n, "a line")
	slots := n.findSlots()
	slots <- struct{}{}
	slots <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := n.Find(ctx, FindParams{Run: findRun, Q: "a", Before: -1}); err == nil {
		t.Fatal("a third find ran")
	}
	<-slots
	if f := find(t, n, FindParams{Q: "line"}); len(f.Hits) != 1 {
		t.Fatalf("%+v", f)
	}
	gone, stop := context.WithCancel(context.Background())
	stop()
	<-slots
	if _, err := n.Find(gone, FindParams{Run: findRun, Q: "a", Before: -1}); err == nil {
		t.Fatal("a cancelled find ran")
	}
}
