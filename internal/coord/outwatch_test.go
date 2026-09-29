package coord

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// outWatch is a run.output.watch its test reads push by push.
type outWatch struct {
	t      *testing.T
	w      *wire.Watch
	pushes chan wire.Push
	open   wire.Open
	events []output.Event
	cursor *node.Cursor
}

func watchOutput(t *testing.T, cli *wire.Conn, p OutputWatchParams) *outWatch {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	o := &outWatch{t: t, w: cli.Watch(ctx, MRunOutputWatch, p), pushes: make(chan wire.Push, 4096)}
	go func() {
		defer close(o.pushes)
		for {
			p, err := o.w.Next(context.Background())
			if err != nil {
				return
			}
			o.pushes <- p
		}
	}()
	first, ok := o.next()
	if !ok || first.Method != wire.PushOpen || first.Decode(&o.open) != nil {
		t.Fatalf("the first push opens: %s %v", first.Method, o.w.Err())
	}
	return o
}

func (o *outWatch) next() (wire.Push, bool) {
	select {
	case p, ok := <-o.pushes:
		return p, ok
	case <-time.After(20 * time.Second):
		o.t.Fatal("no push")
	}
	return wire.Push{}, false
}

// until applies pushes, a key replacing what it keyed before, until ok holds; false when the stream ended first.
func (o *outWatch) until(ok func([]output.Event) bool) bool {
	o.t.Helper()
	for !ok(o.events) {
		p, more := o.next()
		if !more {
			return false
		}
		var op OutputPush
		if p.Method != PushOutput || p.Decode(&op) != nil {
			o.t.Fatalf("push %s %s", p.Method, p.Params)
		}
		for _, e := range op.Events {
			if i := keyed(o.events, e.Key); e.Key != "" && i >= 0 {
				o.events[i] = e
			} else {
				o.events = append(o.events, e)
			}
		}
		if op.Cursor != nil {
			o.cursor = op.Cursor
		}
	}
	return true
}

func keyed(evs []output.Event, key string) int {
	for i, e := range evs {
		if key != "" && e.Key == key {
			return i
		}
	}
	return -1
}

func has(kind string, also func(output.Event) bool) func([]output.Event) bool {
	return func(evs []output.Event) bool {
		for _, e := range evs {
			if e.Kind == kind && (also == nil || also(e)) {
				return true
			}
		}
		return false
	}
}

func gated(t *testing.T) *env {
	return newEnv(t, tend.Config{Agents: []tend.AgentProfile{{Name: "gated", Provider: agent.ProviderFake,
		Args: []string{"--steps", "2", "--every", "20ms", "--permission", "Bash:make deploy"}}}})
}

// Two watchers of a run share one follow of its node; a message the agent gave back shows as the sender's, the
// answer as who gave it, and both streams end once the run did.
func TestTwoWatchersShareOneFollowAndSeeTheJournalsInputs(t *testing.T) {
	e := gated(t)
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "gated").ID})
	e.wait(r.ID, func(r *task.Run) bool { return len(r.Requests) == 1 })
	a := watchOutput(t, e.cli, OutputWatchParams{Run: r.ID})
	b := watchOutput(t, e.cli, OutputWatchParams{Run: r.ID})
	if a.open.Mode != wire.ModeResume || b.open.Mode != wire.ModeResume {
		t.Fatalf("%+v %+v", a.open, b.open)
	}
	e.c.mu.Lock()
	hubs := len(e.c.outs)
	e.c.mu.Unlock()
	if hubs != 1 {
		t.Fatalf("one follow for the run, %d hubs", hubs)
	}
	ask := has(output.KindTool, func(e output.Event) bool { return e.Family == output.FamilyAsk && e.Request != "" })
	if !a.until(ask) || !b.until(ask) {
		t.Fatal("the permission asked is in the output")
	}
	var sent task.Run
	e.must(MRunSend, SendMessage{Run: r.ID, Text: "keep it small"}, &sent)
	id := sent.Sends[len(sent.Sends)-1].ID
	you := has(output.KindYou, func(e output.Event) bool { return e.InputID() == id })
	if !a.until(you) || !b.until(you) {
		t.Fatalf("the message given back: %+v", a.events)
	}
	for _, ev := range a.events {
		if ev.Kind == output.KindYou && (ev.Text != "keep it small" || ev.Mode != "" && ev.Mode != agent.SendSteer) {
			t.Fatalf("%+v", ev)
		}
	}
	req := e.c.State().Runs[r.ID].Requests[0].ID
	e.must(MRunAnswer, Answer{Run: r.ID, Answer: agent.Answer{Request: req, Decision: agent.DecisionAllow}}, nil)
	resolved := has(output.KindResolved, func(e output.Event) bool { return e.Request == req && e.Decision == agent.DecisionAllow })
	if !a.until(resolved) || !b.until(resolved) {
		t.Fatalf("the answer: %+v", a.events)
	}
	for _, o := range []*outWatch{a, b} {
		if o.until(func([]output.Event) bool { return false }) {
			t.Fatal("unreachable")
		}
		if err := o.w.Err(); err != nil {
			t.Fatalf("the stream ends with the run: %v", err)
		}
	}
	if len(a.events) != len(b.events) {
		t.Fatalf("both saw the same: %d and %d events", len(a.events), len(b.events))
	}
}

// A watcher opening again from its cursor gets only what came after it; one from before what the hub keeps gets a
// gap first.
func TestAWatcherResumesFromItsCursor(t *testing.T) {
	e := gated(t)
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "gated").ID})
	e.wait(r.ID, func(r *task.Run) bool { return len(r.Requests) == 1 })
	a := watchOutput(t, e.cli, OutputWatchParams{Run: r.ID})
	a.until(func(evs []output.Event) bool { return a.cursor != nil && len(evs) > 0 })
	b := watchOutput(t, e.cli, OutputWatchParams{Run: r.ID, From: a.cursor})
	if b.open.Mode != wire.ModeResume {
		t.Fatalf("%+v", b.open)
	}
	path := filepath.Join(e.home, "node", "runs", r.ID, "output.log")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"assistant","message":{"content":[{"type":"text","text":"later"}]}}` + "\n")
	f.Close()
	later := has(output.KindSay, func(e output.Event) bool { return e.Text == "later" })
	if !b.until(later) {
		t.Fatal("what came after the cursor")
	}
	if b.events[0].Text != "later" || b.events[0].At == "" {
		t.Fatalf("only what came after, with when it was read: %+v", b.events)
	}
	c := watchOutput(t, e.cli, OutputWatchParams{Run: r.ID, From: &node.Cursor{File: a.cursor.File, Off: 1}})
	if c.open.Mode != wire.ModeGap {
		t.Fatalf("a cursor before what the hub keeps: %+v", c.open)
	}
}

// A watcher whose pushes were dropped gets a gap event before the next batch; the others do not.
func TestADroppedWatcherGetsAGap(t *testing.T) {
	e := gated(t)
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "gated").ID})
	e.wait(r.ID, func(r *task.Run) bool { return len(r.Requests) == 1 })
	a := watchOutput(t, e.cli, OutputWatchParams{Run: r.ID})
	b := watchOutput(t, e.cli, OutputWatchParams{Run: r.ID})
	a.until(func(evs []output.Event) bool { return a.cursor != nil })
	b.until(func(evs []output.Event) bool { return b.cursor != nil })
	e.c.mu.Lock()
	h := e.c.outs[r.ID]
	e.c.mu.Unlock()
	h.mu.Lock()
	var one *outSub
	for sb := range h.subs {
		one = sb
		break
	}
	at := h.at
	one.gap = &at
	h.mu.Unlock()
	path := filepath.Join(e.home, "node", "runs", r.ID, "output.log")
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	f.WriteString("plain words\n")
	f.Close()
	words := has(output.KindSay, func(e output.Event) bool { return e.Text == "plain words" })
	a.until(words)
	b.until(words)
	gaps := 0
	for _, o := range []*outWatch{a, b} {
		for _, ev := range o.events {
			if ev.Kind == output.KindGap {
				gaps++
				if ev.From == nil || ev.To == nil || ev.From.File == "" {
					t.Fatalf("%+v", ev)
				}
			}
		}
	}
	if gaps != 1 {
		t.Fatalf("one of the two had pushes dropped: %d gaps", gaps)
	}
}

// Someone who may no longer read the run has their stream ended as unauthorized.
func TestAWatcherWhoMayNoLongerReadIsEnded(t *testing.T) {
	e := team(t, tend.Config{Agents: []tend.AgentProfile{{Name: "gated", Provider: agent.ProviderFake,
		Args: []string{"--steps", "2", "--every", "20ms", "--permission", "Bash:make deploy"}}}})
	e.start()
	e.project()
	tk := e.taskAs(bob, "b1", "p1", "gated")
	var r task.Run
	if err := callAs(e.as(bob), MRunDispatch, "d1", Dispatch{Task: tk.ID, Runner: node.RunnerBackground}, &r); err != nil {
		t.Fatal(err)
	}
	e.wait(r.ID, func(r *task.Run) bool { return len(r.Requests) == 1 })
	if err := e.as(cy).Call(context.Background(), MRunOutputWatch, OutputWatchParams{Run: r.ID}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("someone outside the project: %v", err)
	}
	w := watchOutput(t, e.as(dee), OutputWatchParams{Run: r.ID})
	if err := callAs(e.as(ann), MProjectMember, "m-dee-out", task.MemberSet{Project: "p1", User: dee.User}, nil); err != nil {
		t.Fatal(err)
	}
	w.until(func([]output.Event) bool { return false })
	if wire.Code(w.w.Err()) != wire.CodeUnauthorized {
		t.Fatalf("%v", w.w.Err())
	}
}

// A run's machine going away ends its watchers' streams as gone.
func TestAWatcherHearsTheMachineGo(t *testing.T) {
	e := gated(t)
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "gated").ID})
	e.wait(r.ID, func(r *task.Run) bool { return len(r.Requests) == 1 })
	w := watchOutput(t, e.cli, OutputWatchParams{Run: r.ID})
	w.until(func([]output.Event) bool { return w.cursor != nil })
	e.c.mu.Lock()
	m := e.c.ms[r.Machine]
	m.conn.Close()
	e.c.mu.Unlock()
	w.until(func([]output.Event) bool { return false })
	if err := w.w.Err(); wire.Code(err) != wire.CodeGone || errors.Is(err, io.EOF) {
		t.Fatalf("%v", err)
	}
}

// TestTheWebClientsOutputFramesAreTheCoordinators: every run.output.watch push in the web client's frame files decodes
// into what the coordinator sends, field for field.
func TestTheWebClientsOutputFramesAreTheCoordinators(t *testing.T) {
	n := 0
	for _, name := range []string{"output", "output-conv"} {
		watches := map[int64]bool{}
		for _, l := range frames(t, name) {
			if l.C != nil && l.C.Type == wire.TypeReq && l.C.Method == MRunOutputWatch {
				watches[l.C.ID] = true
				var p OutputWatchParams
				if err := strict(l.C.Params, &p); err != nil || p.Run == "" {
					t.Fatalf("%s: %s: %v", name, l.C.Params, err)
				}
			}
			s := l.S
			if s == nil || s.Type != wire.TypePush || !watches[s.ID] {
				continue
			}
			n++
			switch s.Method {
			case wire.PushOpen:
				var o struct {
					wire.Open
					Cursor *node.Cursor `json:"cursor"`
					From   *node.Cursor `json:"from"`
					To     *node.Cursor `json:"to"`
				}
				if err := strict(s.Params, &o); err != nil || o.Cursor == nil || o.Mode == wire.ModeGap && (o.From == nil || o.To == nil) {
					t.Fatalf("%s: %s: %v", name, s.Params, err)
				}
			case PushOutput:
				var op OutputPush
				if err := strict(s.Params, &op); err != nil {
					t.Fatalf("%s: %s: %v", name, s.Params, err)
				}
			default:
				t.Fatalf("%s: push %s", name, s.Method)
			}
		}
	}
	if n < 6 {
		t.Fatalf("%d pushes", n)
	}
}
