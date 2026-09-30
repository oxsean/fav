package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/wire"
)

func claudeDelta(parent, event string) string {
	return `{"type":"stream_event","event":` + event + `,"session_id":"s1","parent_tool_use_id":` + parent + `,"uuid":"u"}`
}

// partialsOf is what partial.json holds after a flush; nil when it is gone.
func partialsOf(t *testing.T, p *partials) []Partial {
	t.Helper()
	p.mu.Lock()
	p.flush()
	p.mu.Unlock()
	raw, ok := readPartials(filepath.Dir(p.path))
	if !ok {
		t.Fatal("partial.json unreadable")
	}
	if raw == nil {
		return nil
	}
	var ps Partials
	if err := json.Unmarshal(raw, &ps); err != nil {
		t.Fatal(err)
	}
	return ps.Items
}

func TestClaudeDeltasMakeTheBlockBeingWrittenUntilItsFinalLine(t *testing.T) {
	dir := t.TempDir()
	p := newPartials(dir)
	defer p.close()
	for _, l := range []string{
		claudeDelta("null", `{"type":"message_start","message":{"id":"msg_1","content":[]}}`),
		claudeDelta("null", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`),
		claudeDelta("null", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`),
		claudeDelta("null", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"x"}}`),
	} {
		if !p.take([]byte(l)) {
			t.Fatalf("a delta stays out of the log: %s", l)
		}
	}
	if got := partialsOf(t, p); len(got) != 1 || got[0] != (Partial{Key: "msg_1:0", Kind: output.KindThink, Src: "msg_1", Text: "hmm"}) {
		t.Fatalf("%+v", got)
	}
	p.done([]byte(`{"type":"assistant","message":{"id":"msg_1","content":[{"type":"thinking","thinking":"hmm"}]}}`))
	if got := partialsOf(t, p); got != nil {
		t.Fatalf("the final line takes the block out: %+v", got)
	}
	for _, l := range []string{
		claudeDelta("null", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"Bash","input":{}}}`),
		claudeDelta("null", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"comm"}}`),
		claudeDelta("null", `{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`),
		claudeDelta("null", `{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Hel"}}`),
		claudeDelta("null", `{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"lo 世界"}}`),
	} {
		p.take([]byte(l))
	}
	if got := partialsOf(t, p); len(got) != 1 || got[0].Key != "msg_1:2" || got[0].Kind != output.KindSay || got[0].Text != "Hello 世界" {
		t.Fatalf("a tool's input is not shown as it streams; the text is: %+v", got)
	}
	p.take([]byte(claudeDelta("null", `{"type":"message_stop"}`)))
	if got := partialsOf(t, p); got != nil {
		t.Fatalf("a message that stopped without its final line leaves nothing behind: %+v", got)
	}
	if p.take([]byte(`{"type":"assistant","message":{"id":"msg_2","content":[{"type":"text","text":"a \"type\":\"stream_event\""}]}}`)) {
		t.Fatal("a line that only mentions stream_event is logged")
	}
}

func TestCodexDeltasMakeTheItemsBeingWrittenUntilTheyComplete(t *testing.T) {
	dir := t.TempDir()
	p := newPartials(dir)
	defer p.close()
	delta := func(method, item, d string, part int) string {
		return fmt.Sprintf(`{"method":%q,"params":{"threadId":"th","turnId":"tu","itemId":%q,"delta":%q,"summaryIndex":%d},"emittedAtMs":1}`, method, item, d, part)
	}
	for _, l := range []string{
		delta("item/reasoning/textDelta", "rs_1", "raw", 0),
		delta("item/reasoning/summaryTextDelta", "rs_1", "first", 0),
		delta("item/reasoning/summaryTextDelta", "rs_1", "second", 1),
		delta("item/reasoning/textDelta", "rs_1", " more", 0),
		delta("item/agentMessage/delta", "msg_1", "我将", 0),
		delta("item/agentMessage/delta", "msg_1", "执行", 0),
		delta("item/plan/delta", "plan_1", "step", 0),
	} {
		if !p.take([]byte(l)) {
			t.Fatalf("a delta stays out of the log: %s", l)
		}
	}
	got := partialsOf(t, p)
	want := []Partial{{Key: "rs_1", Kind: output.KindThink, Src: "rs_1", Text: "first\nsecond"}, {Key: "msg_1", Kind: output.KindSay, Src: "msg_1", Text: "我将执行"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("the summary over the raw reasoning, the message; no plan: %+v", got)
	}
	if p.take([]byte(`{"id":7,"method":"x/delta","params":{"itemId":"i"}}`)) {
		t.Fatal("a request is not a delta")
	}
	p.done([]byte(`{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"msg_1","text":"我将执行"}}}`))
	if got := partialsOf(t, p); len(got) != 1 || got[0].Key != "rs_1" {
		t.Fatalf("the completed item leaves: %+v", got)
	}
	p.done([]byte(`{"method":"turn/completed","params":{"turn":{"status":"completed"}}}`))
	if got := partialsOf(t, p); got != nil {
		t.Fatalf("the turn's end ends them all: %+v", got)
	}
}

func TestACodexCommandIsListedFromItsStartWithTheLastLinesOfItsOutput(t *testing.T) {
	dir := t.TempDir()
	p := newPartials(dir)
	defer p.close()
	out := func(item, d string) string {
		return fmt.Sprintf(`{"method":"item/commandExecution/outputDelta","params":{"threadId":"th","turnId":"tu","itemId":%q,"delta":%q}}`, item, d)
	}
	p.done([]byte(`{"method":"item/started","params":{"item":{"type":"commandExecution","id":"exec-1","command":"/bin/zsh -lc 'go test ./...'","status":"inProgress","aggregatedOutput":null}}}`))
	p.done([]byte(`{"method":"item/started","params":{"item":{"type":"agentMessage","id":"msg_1","text":""}}}`))
	want := Partial{Key: "exec-1", Kind: output.KindCmd, Src: "exec-1", Command: "/bin/zsh -lc 'go test ./...'"}
	if got := partialsOf(t, p); len(got) != 1 || got[0] != want {
		t.Fatalf("a command runs before it prints anything; other items wait for their text: %+v", got)
	}
	for _, d := range []string{"ok  a\n", "ok  b\nok  c\n", "ok  d\n--- FA", "IL: e"} {
		if !p.take([]byte(out("exec-1", d))) {
			t.Fatal("command output stays out of the log")
		}
	}
	p.take([]byte(`{"method":"item/agentMessage/delta","params":{"itemId":"msg_1","delta":"跑着"}}`))
	want.Text, want.Bytes = "ok  c\nok  d\n--- FAIL: e", len("ok  a\nok  b\nok  c\nok  d\n--- FAIL: e")
	if got := partialsOf(t, p); len(got) != 2 || got[0] != want || got[1].Key != "msg_1" {
		t.Fatalf("the last %d lines, the one being written among them, and every byte counted: %+v", output.RunningTail, got)
	}
	p.take([]byte(out("exec-1", "\n")))
	if got := partialsOf(t, p); got[0].Text != "ok  c\nok  d\n--- FAIL: e\n" {
		t.Fatalf("a line ended keeps its newline: %q", got[0].Text)
	}
	p.take([]byte(out("exec-2", strings.Repeat("字", maxPartial))))
	if got := partialsOf(t, p); len(got) != 3 || got[2].Command != "" || !strings.HasPrefix(got[2].Text, "…") || len(got[2].Text) > maxPartial+len("…") {
		t.Fatalf("output of a command whose start was not seen; a long line is sent as its tail: %d", len(got))
	}
	p.done([]byte(`{"method":"item/completed","params":{"item":{"type":"commandExecution","id":"exec-1","command":"go test","exitCode":1,"aggregatedOutput":"…"}}}`))
	if got := partialsOf(t, p); len(got) != 2 || got[0].Key != "msg_1" || got[1].Key != "exec-2" {
		t.Fatalf("the completed command leaves: %+v", got)
	}
	p.done([]byte(`{"method":"turn/completed","params":{"turn":{"status":"interrupted"}}}`))
	if got := partialsOf(t, p); got != nil {
		t.Fatalf("the turn's end ends the commands too: %+v", got)
	}
}

func TestTheTailOfACommandsOutput(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"a", "a"},
		{"a\nb\nc\nd", "b\nc\nd"},
		{"a\nb\nc\nd\n", "b\nc\nd\n"},
		{"a\nb\n", "a\nb\n"},
		{"a\n\n\n\n", "\n\n\n"},
	} {
		if got := cmdTail(c.in); got != c.want {
			t.Errorf("cmdTail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestALongPartialIsSentAsItsTail(t *testing.T) {
	long := strings.Repeat("字", maxPartial) // three bytes each
	s := sentPartial(trimHead(long + "end"))
	if !strings.HasPrefix(s, "…") || !strings.HasSuffix(s, "end") || len(s) > maxPartial+len("…") || !strings.HasPrefix(s[len("…"):], "字") {
		t.Fatalf("the last bytes, from a rune's start: %d %q", len(s), s[:12])
	}
}

func TestTheSupervisorLogsNoDeltaAndClearsPartialJSONAtTheEnd(t *testing.T) {
	dir := t.TempDir()
	s := &sup{dir: dir, log: &rolling{path: filepath.Join(dir, "output.log")}}
	defer s.log.Close()
	in := strings.Join([]string{
		claudeDelta("null", `{"type":"message_start","message":{"id":"msg_1"}}`),
		claudeDelta("null", `{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`),
		claudeDelta("null", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`),
		`{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"hi"}]}}`,
	}, "\n") + "\n"
	s.copyOut(strings.NewReader(in), s.log)
	b, err := os.ReadFile(filepath.Join(dir, "output.log"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("stream_event")) || !bytes.Contains(b, []byte(`"type":"assistant"`)) {
		t.Fatalf("%s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, partialFile)); !os.IsNotExist(err) {
		t.Fatalf("partial.json outlives the output: %v", err)
	}
}

// ⚠️ on Windows a reader holding partial.json makes the writer's rename wait; neither side may fail, and the reader
// ends on the last version written.
func TestReadingPartialJSONWhileItIsReplacedNeitherFailsNorMissesTheLast(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, partialFile)
	const versions = 300
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var bad []string
	var mu sync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			raw, ok := readPartials(dir)
			if ok && raw != nil {
				var ps Partials
				if json.Unmarshal(raw, &ps) != nil || len(ps.Items) != 1 {
					mu.Lock()
					bad = append(bad, string(raw))
					mu.Unlock()
				}
			}
		}
	}()
	for i := range versions {
		b, _ := json.Marshal(Partials{Items: []Partial{{Key: "k", Kind: output.KindSay, Src: "k", Text: fmt.Sprint(i)}}})
		if err := fileio.WriteFile(path, b, 0o600); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("version %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
	if len(bad) > 0 {
		t.Fatalf("read a torn file: %q", bad[0])
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, ok := readPartials(dir)
		if ok && bytes.Contains(raw, []byte(fmt.Sprintf(`"text":"%d"`, versions-1))) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the last version is not what is read: %s %v", raw, ok)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFollowPushesPartialsAsTheyChangeAndWhenTheyGo(t *testing.T) {
	r := newFollowRun(t)
	r.write("a\n")
	w := r.follow(Cursor{})
	var open wire.Open
	next(t, w, &open)
	lines(t, w, 1)
	path := filepath.Join(r.dir, partialFile)
	write := func(text string) {
		b, _ := json.Marshal(Partials{Items: []Partial{{Key: "m:0", Kind: output.KindSay, Src: "m", Text: text}}})
		if err := fileio.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	partial := func() *Partials {
		t.Helper()
		for {
			var fl Follow
			next(t, w, &fl)
			if fl.Partial != nil {
				return fl.Partial
			}
		}
	}
	write("He")
	if p := partial(); len(p.Items) != 1 || p.Items[0].Text != "He" {
		t.Fatalf("%+v", p)
	}
	write("Hello")
	if p := partial(); len(p.Items) != 1 || p.Items[0].Text != "Hello" {
		t.Fatalf("%+v", p)
	}
	r.write(`{"type":"assistant"}` + "\n")
	os.Remove(path)
	var fl Follow
	for fl.Partial == nil {
		fl = Follow{}
		next(t, w, &fl)
		if len(fl.Lines) > 0 {
			continue
		}
	}
	if len(fl.Partial.Items) != 0 {
		t.Fatalf("gone is an empty list: %+v", fl.Partial)
	}
}
