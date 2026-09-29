package coord

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/task"
)

func newTestHub() *hub {
	return &hub{subs: map[*outSub]bool{}, seen: map[string]bool{}, temps: map[string]bool{}, parts: map[string]output.Event{}}
}

func partialPush(items ...node.Partial) node.Follow {
	return node.Follow{File: "f:1", Partial: &node.Partials{Items: items}, Cursor: node.Cursor{File: "f:1"}}
}

func assistantLine(off int64, msg, kind, text string) node.TailLine {
	field := map[string]string{"text": "text", "thinking": "thinking"}[kind]
	b, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"id": msg,
		"content": []map[string]string{{"type": kind, field: text}}}})
	return node.TailLine{Off: off, Text: string(b) + "\n"}
}

func TestAPartialIsATempEventTheFinalEventReplaces(t *testing.T) {
	h := newTestHub()
	h.take(partialPush(node.Partial{Key: "m:0", Kind: output.KindThink, Src: "m", Text: "hm"}), output.Said{})
	h.take(partialPush(node.Partial{Key: "m:0", Kind: output.KindThink, Src: "m", Text: "hmm"}), output.Said{})
	if len(h.pending) != 1 || !h.pending[0].Temp || h.pending[0].Key != "p:m:0" || h.pending[0].Text != "hmm" || h.pending[0].Kind != output.KindThink {
		t.Fatalf("one temp event per key in a batch, the latest: %+v", h.pending)
	}
	h.pending = nil
	l := assistantLine(0, "m", "thinking", "hmm.")
	h.take(node.Follow{File: "f:1", Lines: []node.TailLine{l}, Partial: &node.Partials{}, Cursor: node.Cursor{File: "f:1", Off: int64(len(l.Text))}}, output.Said{})
	if len(h.pending) != 1 || h.pending[0].Temp || h.pending[0].Key != "p:m:0" || h.pending[0].Text != "hmm." || h.pending[0].ID == "" {
		t.Fatalf("the final event takes the key and nothing clears it: %+v", h.pending)
	}
	h.pending = nil
	h.take(partialPush(node.Partial{Key: "m:0", Kind: output.KindThink, Src: "m", Text: "hmm"}), output.Said{})
	if len(h.pending) != 0 {
		t.Fatalf("a message listed after its final event is not shown again: %+v", h.pending)
	}
}

func TestAPartialGoneWithoutItsFinalEventIsTakenAway(t *testing.T) {
	h := newTestHub()
	h.take(partialPush(node.Partial{Key: "i1", Kind: output.KindSay, Src: "i1", Text: "half"},
		node.Partial{Key: "i2", Kind: output.KindSay, Src: "i2", Text: "other"}), output.Said{})
	h.pending = nil
	h.take(partialPush(node.Partial{Key: "i2", Kind: output.KindSay, Src: "i2", Text: "other"}), output.Said{})
	if len(h.pending) != 1 || !h.pending[0].Temp || h.pending[0].Key != "p:i1" || h.pending[0].Text != "" {
		t.Fatalf("an empty temp event for the one gone, nothing for the one unchanged: %+v", h.pending)
	}
	l := assistantLine(0, "i2", "text", "unrelated kind")
	l.Text = `{"method":"item/completed","params":{"item":{"type":"reasoning","id":"i2","summary":["x"]}}}` + "\n"
	h.pending = nil
	h.take(node.Follow{File: "f:1", Lines: []node.TailLine{l}, Cursor: node.Cursor{File: "f:1", Off: 90}}, output.Said{})
	if len(h.pending) != 1 || h.pending[0].Key != "" {
		t.Fatalf("a final event of another kind does not take the key: %+v", h.pending)
	}
	h.pending = nil
	l.Off, l.Text = 90, `{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"i2","text":"other."}}}`+"\n"
	h.take(node.Follow{File: "f:1", Lines: []node.TailLine{l}, Cursor: node.Cursor{File: "f:1", Off: 200}}, output.Said{})
	if len(h.pending) != 1 || h.pending[0].Key != "p:i2" || h.pending[0].Temp {
		t.Fatalf("codex's completed item takes the key: %+v", h.pending)
	}
}

func TestOnlyTheLatestBatchOfTheSameTempEventsIsKept(t *testing.T) {
	h := newTestHub()
	h.cursor = &node.Cursor{File: "f:1"}
	h.pending = []output.Event{{ID: "f:1:0:0", Kind: output.KindSay, Text: "a"}}
	h.cursor = &node.Cursor{File: "f:1", Off: 2}
	h.flush()
	for _, text := range []string{"x", "xy", "xyz"} {
		h.take(partialPush(node.Partial{Key: "m:0", Kind: output.KindSay, Src: "m", Text: text}), output.Said{})
		h.cursor = &node.Cursor{File: "f:1", Off: 2}
		h.flush()
	}
	if len(h.batches) != 2 {
		t.Fatalf("the backlog and the latest partial: %d batches", len(h.batches))
	}
	var op OutputPush
	json.Unmarshal(h.batches[1].raw, &op)
	if len(op.Events) != 1 || op.Events[0].Text != "xyz" {
		t.Fatalf("%+v", op)
	}
}

// A watcher of a live run sees the message being written, then the final event in its place.
func TestAWatcherSeesTheMessageBeingWritten(t *testing.T) {
	e := gated(t)
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "gated").ID})
	e.wait(r.ID, func(r *task.Run) bool { return len(r.Requests) == 1 })
	a := watchOutput(t, e.cli, OutputWatchParams{Run: r.ID})
	dir := filepath.Join(e.home, "node", "runs", r.ID)
	b, _ := json.Marshal(node.Partials{Items: []node.Partial{{Key: "msg_9:1", Kind: output.KindSay, Src: "msg_9", Text: "writing"}}})
	if err := fileio.WriteFile(filepath.Join(dir, "partial.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.until(has(output.KindSay, func(e output.Event) bool { return e.Temp && e.Key == "p:msg_9:1" && e.Text == "writing" })) {
		t.Fatalf("the message being written: %+v", a.events)
	}
	f, err := os.OpenFile(filepath.Join(dir, "output.log"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(assistantLine(0, "msg_9", "text", "written").Text)
	f.Close()
	os.Remove(filepath.Join(dir, "partial.json"))
	if !a.until(has(output.KindSay, func(e output.Event) bool { return !e.Temp && e.Key == "p:msg_9:1" && e.Text == "written" })) {
		t.Fatalf("the final event in its place: %+v", a.events)
	}
	for _, ev := range a.events {
		if ev.Temp && ev.Key == "p:msg_9:1" {
			t.Fatalf("%+v", a.events)
		}
	}
}
