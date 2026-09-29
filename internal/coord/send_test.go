package coord

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func detailOf(err error) string {
	var we *wire.Error
	if errors.As(err, &we) {
		return we.Detail
	}
	return ""
}

// idleConn is a node connection that answers nothing: what the coordinator knows of the node is its hello.
type idleConn struct{}

func (idleConn) Call(context.Context, string, any, any) error {
	return &wire.Error{Code: wire.CodeOffline, Detail: "idle"}
}
func (idleConn) Done() <-chan struct{} { return nil }
func (idleConn) Err() *wire.Error      { return nil }
func (idleConn) Close() error          { return nil }

// asConnected is what do answers while machine's node is connected and said hello with features.
func (e *env) asConnected(machine string, features []string, do func() error) error {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	m := e.c.ms[machine]
	conn, hello := m.conn, m.hello
	m.conn, m.hello = idleConn{}, remote.Hello{Features: features}
	defer func() { m.conn, m.hello = conn, hello }()
	return do()
}

func request(method string, params any) *wire.Request {
	b, _ := json.Marshal(params)
	return &wire.Request{Method: method, Params: b}
}

func TestAMessageGoesWhereItsSenderSawOrSaysWhereItGoesNow(t *testing.T) {
	e := stage(t, false)
	tid, rid := e.scene("running", "", Owner.User, 1)
	var mr MessageRoute
	e.must(MTaskMessagePreview, MessagePreview{ID: tid}, &mr)
	if mr.Route.To != task.RouteRun || mr.Route.Run != rid || mr.Route.Version == 0 {
		t.Fatalf("%+v", mr)
	}
	err := e.call(MTaskMessage, TaskMessage{ID: tid, Text: "x", Expect: &task.Route{To: task.RouteReply, Run: rid, Version: mr.Route.Version}}, nil)
	if wire.Code(err) != wire.CodeRouteChanged || detailOf(err) != task.RouteRun {
		t.Fatalf("a route its sender did not see: %v", err)
	}
	var res MessageResult
	e.must(MTaskMessage, TaskMessage{ID: tid, Text: "into the turn", Expect: &mr.Route}, &res)
	e.must(MTaskMessage, TaskMessage{ID: tid, Text: "once it ends", Mode: agent.SendAfter}, &res)
	if res.To != task.RouteRun || res.Run != rid {
		t.Fatalf("%+v", res)
	}
	sends := e.c.State().Runs[rid].Sends
	if len(sends) != 2 || sends[0].Mode != "" || sends[1].Mode != agent.SendAfter || sends[1].By != Owner.User {
		t.Fatalf("%+v", sends)
	}
	if err := e.call(MTaskMessage, TaskMessage{ID: tid, Text: "x", Mode: "sideways"}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("an unknown mode: %v", err)
	}

	bt, _ := e.scene("backlog", "", Owner.User, 1)
	if err := e.call(MTaskMessage, TaskMessage{ID: bt, Text: "x"}, nil); wire.Code(err) != wire.CodeCannotSend || detailOf(err) != task.WhyNoSession {
		t.Fatalf("nothing takes it: %v", err)
	}
}

func TestTheFirstAnswerCountsUnlessItDidNotReachTheAgent(t *testing.T) {
	e := stage(t, true)
	_, rid := e.scene("running", "p1", bob.User, 1)
	pick := func(p Principal, db string) error {
		return callAs(e.as(p), MRunAnswer, "a-"+p.User+"-"+db, Answer{Run: rid, Answer: agent.Answer{Request: "q2", Answers: map[string]string{"which?": db}}}, nil)
	}
	if err := pick(ann, "a"); err != nil {
		t.Fatal(err)
	}
	if err := pick(bob, "b"); wire.Code(err) != wire.CodeRequestGone || detailOf(err) != ann.User {
		t.Fatalf("the second answer: %v", err)
	}
	if err := callAs(e.as(bob), MRunAnswer, "a-nope", Answer{Run: rid, Answer: agent.Answer{Request: "nope", Allow: true}}, nil); wire.Code(err) != wire.CodeRequestGone || detailOf(err) != "" {
		t.Fatalf("a request never asked: %v", err)
	}

	e.c.mu.Lock()
	e.c.commit(journal.System, nil, journal.NewEvent(task.ERunObserved, task.Observation{ID: rid, State: task.Running, NodeRev: 2, Stream: true, Turn: 1,
		Caps: e.c.st.Runs[rid].Caps, Requests: []agent.Request{{ID: "q1", Kind: agent.RequestPermission, Tool: "Bash", AllowRun: true},
			{ID: "q2", Kind: agent.RequestQuestion, Failed: true, Questions: []agent.Question{{Question: "which?", Options: []string{"a", "b"}}}}}}))
	e.c.mu.Unlock()
	if err := pick(bob, "b"); err != nil {
		t.Fatalf("an answer that did not reach the agent is replaced: %v", err)
	}
	if as := e.c.State().Runs[rid].Answers; len(as) != 1 || as[0].By != bob.User || as[0].Answers["which?"] != "b" {
		t.Fatalf("%+v", as)
	}

	allowRun := request(MRunAnswer, Answer{Run: rid, Answer: agent.Answer{Request: "q1", Decision: agent.DecisionAllowRun}})
	err := e.asConnected("far", nil, func() error { _, _, err := e.c.runAnswer(ann, allowRun); return err })
	if wire.Code(err) != wire.CodeProto || detailOf(err) != ReasonNodeOutdated {
		t.Fatalf("a node that reads allow_run as allow once: %v", err)
	}
	if err := callAs(e.as(ann), MRunAnswer, "a-scope", Answer{Run: rid, Answer: agent.Answer{Request: "q2", Decision: agent.DecisionAllowRun,
		Answers: map[string]string{"which?": "a"}}}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("a question has no run-wide answer: %v", err)
	}
	if err := callAs(e.as(ann), MRunAnswer, "a-run", Answer{Run: rid, Answer: agent.Answer{Request: "q1", Decision: agent.DecisionAllowRun}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAnInterruptIsAskedOncePerTurn(t *testing.T) {
	e := stage(t, false)
	_, rid := e.scene("running", "", Owner.User, 1)
	ask := request(MRunInterrupt, Interrupt{Run: rid})
	if err := e.asConnected("far", nil, func() error { _, _, err := e.c.runInterrupt(Owner, ask); return err }); wire.Code(err) != wire.CodeProto {
		t.Fatalf("a node without run.interrupt: %v", err)
	}
	send := request(MRunSend, SendMessage{Run: rid, Text: "x", Mode: agent.SendInterrupt})
	if err := e.asConnected("far", nil, func() error { _, _, err := e.c.runSend(Owner, send); return err }); wire.Code(err) != wire.CodeProto {
		t.Fatalf("a node without run.interrupt: %v", err)
	}
	e.must(MRunInterrupt, Interrupt{Run: rid}, nil)
	seq := e.c.State().Seq
	e.must(MRunInterrupt, Interrupt{Run: rid, Turn: 1}, nil)
	if r := e.c.State(); r.Seq != seq || *r.Runs[rid].Interrupt != (task.RunInterrupt{ID: rid, Turn: 1, Ask: "int_1", By: Owner.User}) {
		t.Fatalf("asked again: %d %d %+v", r.Seq, seq, r.Runs[rid].Interrupt)
	}
	e.c.mu.Lock()
	e.c.commit(journal.System, nil, journal.NewEvent(task.ERunObserved, task.Observation{ID: rid, State: task.Running, NodeRev: 2, Stream: true, Turn: 2,
		Caps: e.c.st.Runs[rid].Caps}))
	e.c.mu.Unlock()
	if err := e.call(MRunInterrupt, Interrupt{Run: rid, Turn: 1}, nil); wire.Code(err) != wire.CodeConflict || detailOf(err) != "turn_over" {
		t.Fatalf("a turn that is over: %v", err)
	}
}

// continuedFrom waits for the run that goes on from run id.
func (e *env) continuedFrom(id string) *task.Run {
	e.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		for _, r := range e.c.State().Runs {
			if r.Parent == id {
				return r
			}
		}
		e.c.poke()
	}
	e.t.Fatalf("nothing went on from %s: %+v", id, e.c.State().Runs[id])
	return nil
}

func TestAMessageForAfterTheTurnGoesOnInItsSession(t *testing.T) {
	e := newEnv(t, tend.Config{Agents: []tend.AgentProfile{
		{Name: "brief", Provider: agent.ProviderFake, Args: []string{"--steps", "20", "--every", "100ms"}},
		{Name: "long", Provider: agent.ProviderFake, Args: []string{"--steps", "300", "--every", "100ms"}}}})
	e.start()
	for _, c := range []struct{ agent, mode string }{{"brief", agent.SendAfter}, {"long", agent.SendInterrupt}} {
		tk := e.task(c.mode, c.agent)
		r := e.dispatch(Dispatch{Task: tk.ID})
		e.wait(r.ID, func(r *task.Run) bool { return r.State == task.Running && r.Turn > 0 && r.Caps != nil })
		var got task.Run
		e.must(MRunSend, SendMessage{Run: r.ID, Text: "then this", Mode: c.mode}, &got)
		e.must(MRunSend, SendMessage{Run: r.ID, Text: "and that", Mode: agent.SendAfter}, nil)
		end := e.wait(r.ID, ended)
		if c.mode == agent.SendInterrupt && (end.State != task.Stopped || end.Interrupt == nil) {
			t.Fatalf("interrupted: %+v", end)
		}
		next := e.continuedFrom(r.ID)
		if next.Resume != end.Session || next.Brief != "then this\n\nand that" || !slices.Equal(next.Takes, []string{got.Sends[0].ID, e.c.State().Runs[r.ID].Sends[1].ID}) {
			t.Fatalf("%s: %+v", c.mode, next)
		}
		for _, m := range e.c.State().Runs[r.ID].Sends {
			if m.State != agent.SendSent {
				t.Fatalf("%s: what it carries went out: %+v", c.mode, m)
			}
		}
		e.must(MRunStop, task.RunRef{ID: next.ID}, nil)
		e.wait(next.ID, ended)
	}
}
