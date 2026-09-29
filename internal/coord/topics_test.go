package coord

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// topicWatch is a machines.watch or inbox.watch its test reads push by push.
type topicWatch struct {
	t      *testing.T
	w      *wire.Watch
	method string
	pushes chan wire.Push
}

func watchTopic(t *testing.T, cli *wire.Conn, method string) *topicWatch {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tw := &topicWatch{t: t, w: cli.Watch(ctx, method, TopicParams{}), method: method, pushes: make(chan wire.Push, 64)}
	go func() {
		defer close(tw.pushes)
		for {
			p, err := tw.w.Next(context.Background())
			if err != nil {
				return
			}
			tw.pushes <- p
		}
	}()
	var open wire.Open
	if p := tw.next(); p.Method != wire.PushOpen || p.Decode(&open) != nil || open.Mode != wire.ModeSnapshot {
		t.Fatalf("the first push opens with a snapshot: %s %s", p.Method, p.Params)
	}
	return tw
}

func (tw *topicWatch) next() wire.Push {
	tw.t.Helper()
	select {
	case p, ok := <-tw.pushes:
		if !ok {
			tw.t.Fatalf("the stream ended: %v", tw.w.Err())
		}
		return p
	case <-time.After(20 * time.Second):
		tw.t.Fatal("no push")
	}
	return wire.Push{}
}

// until decodes pushes into v until ok holds.
func (tw *topicWatch) until(v any, ok func() bool) {
	tw.t.Helper()
	for {
		p := tw.next()
		if err := p.Decode(v); err != nil || p.Method != map[string]string{MMachinesWatch: PushMachines, MInboxWatch: PushInbox}[tw.method] {
			tw.t.Fatalf("push %s %s: %v", p.Method, p.Params, err)
		}
		if ok() {
			return
		}
	}
}

// quiet: nothing more is pushed for a while.
func (tw *topicWatch) quiet(d time.Duration) {
	tw.t.Helper()
	select {
	case p := <-tw.pushes:
		tw.t.Fatalf("pushed although nothing changed: %s %s", p.Method, p.Params)
	case <-time.After(d):
	}
}

func names(ms MachineList) []string {
	var out []string
	for _, m := range ms.Items {
		out = append(out, m.Name)
	}
	return out
}

// machines.watch pushes whole lists of what the viewer may see: at once, when a share opens a machine to them or
// closes it again, and when its runs change; nothing when nothing changed.
func TestMachinesWatchPushesWhatTheViewerMaySeeAsItChanges(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	w := watchTopic(t, e.as(cy), MMachinesWatch)
	var ms MachineList
	w.until(&ms, func() bool { return true })
	if ms.Items == nil || len(ms.Items) != 0 {
		t.Fatalf("no machine is his: %s", names(ms))
	}
	if err := callAs(e.as(ann), MMachineShare, "s1", task.Share{Machine: Local, Users: []string{cy.User}}, nil); err != nil {
		t.Fatal(err)
	}
	w.until(&ms, func() bool { return len(ms.Items) == 1 })
	if m := ms.Items[0]; m.Name != Local || m.Owner != ann.User || m.Slots == 0 {
		t.Fatalf("%+v", m)
	}
	w.quiet(machinesWait * 3)
	var tk task.Task
	if err := callAs(e.as(ann), MTaskCreate, "t1", TaskCreate{Title: "x", Dir: t.TempDir(), Agent: "quick"}, &tk); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(ann), MRunDispatch, "d1", Dispatch{Task: tk.ID, Runner: node.RunnerBackground}, nil); err != nil {
		t.Fatal(err)
	}
	w.until(&ms, func() bool { return len(ms.Items) == 1 && ms.Items[0].Active+ms.Items[0].Queued > 0 })
	if err := callAs(e.as(ann), MMachineShare, "s2", task.Share{Machine: Local}, nil); err != nil {
		t.Fatal(err)
	}
	w.until(&ms, func() bool { return len(ms.Items) == 0 })
}

// inbox.watch pushes what waits for the viewer: a permission their run asks for, and nothing once it was answered.
// Someone the task does not concern is not woken.
func TestInboxWatchPushesWhatWaitsForTheViewer(t *testing.T) {
	e := gated(t)
	e.start()
	w := watchTopic(t, e.cli, MInboxWatch)
	other := watchTopic(t, e.as(Principal{User: "u_other"}), MInboxWatch)
	var in, theirs Inbox
	w.until(&in, func() bool { return true })
	other.until(&theirs, func() bool { return true })
	if len(in.Items) != 0 || len(theirs.Items) != 0 {
		t.Fatalf("%+v %+v", in, theirs)
	}
	r := e.dispatch(Dispatch{Task: e.task("x", "gated").ID})
	w.until(&in, func() bool { return len(in.Items) == 1 && in.Items[0].Reason == task.AttentionPermission })
	if it := in.Items[0]; it.Run != r.ID || len(it.Pending) != 1 || it.Pending[0].Request == "" {
		t.Fatalf("%+v", it)
	}
	req := e.c.State().Runs[r.ID].Requests[0].ID
	e.must(MRunAnswer, Answer{Run: r.ID, Answer: agent.Answer{Request: req, Decision: agent.DecisionAllow}}, nil)
	w.until(&in, func() bool { return len(in.Items) == 0 || in.Items[0].Reason != task.AttentionPermission })
	other.quiet(inboxWait * 3)
}

// TestTheWebClientsTopicFramesAreTheCoordinators: every machines.watch and inbox.watch frame in the web client's frame
// files decodes into what the coordinator sends, field for field.
func TestTheWebClientsTopicFramesAreTheCoordinators(t *testing.T) {
	n := 0
	for _, name := range []string{"home-state", "output-state"} {
		watches := map[int64]string{}
		for _, l := range frames(t, name) {
			if c := l.C; c != nil && c.Type == wire.TypeReq && (c.Method == MMachinesWatch || c.Method == MInboxWatch) {
				watches[c.ID] = c.Method
				var p TopicParams
				if err := strict(c.Params, &p); err != nil {
					t.Fatalf("%s: %s: %v", name, c.Params, err)
				}
			}
			s := l.S
			if s == nil || s.Type != wire.TypePush || watches[s.ID] == "" {
				continue
			}
			n++
			var v any
			switch {
			case s.Method == wire.PushOpen:
				v = &wire.Open{}
			case s.Method == PushMachines && watches[s.ID] == MMachinesWatch:
				v = &MachineList{}
			case s.Method == PushInbox && watches[s.ID] == MInboxWatch:
				v = &Inbox{}
			default:
				t.Fatalf("%s: %s pushes %s", name, watches[s.ID], s.Method)
			}
			if err := strict(s.Params, v); err != nil {
				t.Fatalf("%s: %s: %v", name, s.Params, err)
			}
			if b, _ := json.Marshal(v); s.Method != wire.PushOpen && len(b) < 12 {
				t.Fatalf("%s: %s", name, b)
			}
		}
	}
	if n < 8 {
		t.Fatalf("%d pushes", n)
	}
}
