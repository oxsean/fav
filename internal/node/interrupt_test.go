package node

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// streamRig is a stream supervisor whose stdin the test reads, logging to its run directory.
func streamRig(t *testing.T, provider string) *codexRig {
	t.Helper()
	g := newCodexRig(t, "")
	g.s.spec.Agent, g.s.spec.Provider, g.s.spec.Run = provider, provider, "r_0123456789ab"
	g.s.log = &rolling{path: filepath.Join(g.s.dir, "output.log")}
	g.s.log.turns.doing = g.s.doing
	t.Cleanup(func() {
		go io.Copy(io.Discard, g.out)
		g.s.in.finish(2 * time.Second) // what it was told is written before the directory goes
		g.s.log.Close()
	})
	g.s.proto = newProto(g.s)
	return g
}

func (g *codexRig) sends(ids ...string) {
	for _, id := range ids {
		g.s.st.Sends = append(g.s.st.Sends, agent.Send{ID: id, Text: id, State: agent.SendSent})
	}
}

// logLine writes line to the log and reads it, as copyOut does.
func (g *codexRig) logLine(line string) logPos {
	at := g.s.log.put([]byte(line + "\n"))
	g.s.lineAt([]byte(line+"\n"), at)
	return at
}

func (g *codexRig) marks(t *testing.T, event string) []Mark {
	t.Helper()
	ms, _ := linesFrom(filepath.Join(g.s.dir, marksFile), 0, func(m Mark) bool { return m.Event == event })
	return ms
}

func stateOf(g *codexRig, id string) string {
	for _, m := range g.s.st.Sends {
		if m.ID == id {
			return m.State
		}
	}
	return ""
}

func TestClaudeGivingMessagesBackMarksEachSeenOnce(t *testing.T) {
	g := streamRig(t, tend.ProviderClaude)
	g.s.proto.begin("b")
	g.sent(t)
	if brief := g.sent(t); brief["uuid"] != UUIDFor(g.s.spec.Run, briefInput) {
		t.Fatalf("the brief goes with its uuid: %v", brief)
	}
	g.sends("m1", "m2", "m3")
	for _, id := range []string{"m1", "m2", "m3"} {
		g.s.proto.message(id, "P.S. "+id, nil)
		if m := g.sent(t); m["uuid"] != UUIDFor(g.s.spec.Run, id) || m["uuid"] == UUIDFor(g.s.spec.Run+"x", id) {
			t.Fatalf("%v", m)
		}
	}
	replay := func(id, text string) string {
		return `{"type":"user","message":{"role":"user","content":"` + text + `"},"session_id":"s","parent_tool_use_id":null,"uuid":"` +
			UUIDFor(g.s.spec.Run, id) + `","timestamp":"2026-09-29T13:57:24.465Z","isReplay":true}`
	}
	g.logLine(replay(briefInput, "b"))
	g.logLine(`{"type":"result","subtype":"success","is_error":false,"result":"story","num_turns":1}`)
	at1 := g.logLine(replay("m1", "P.S. m1"))
	g.logLine(replay("m2", "P.S. m2"))
	at3 := g.logLine(replay("m3", `P.S. m1\nP.S. m2\nP.S. m3`)) // the last of those sent together holds all their text
	g.logLine(replay("m3", "P.S. m3"))
	g.logLine(replay("m9", "not ours"))
	if !g.s.out.turnDone.IsZero() {
		t.Fatal("a message given back goes on after the result")
	}
	for _, id := range []string{"m1", "m2", "m3"} {
		if stateOf(g, id) != agent.SendSeen {
			t.Fatalf("%s: %+v", id, g.s.st.Sends)
		}
	}
	in := g.marks(t, markInput)
	if len(in) != 3 || in[0].ID != "m1" || in[0].Off != at1.off || in[0].File != at1.file || in[2].ID != "m3" || in[2].Off != at3.off {
		t.Fatalf("%+v", in)
	}
	g.s.delivered("m1", nil)
	if stateOf(g, "m1") != agent.SendSeen {
		t.Fatal("a message seen stays seen when its write is told late")
	}
}

func TestAClaudeAnswerMarksTheRequestResolvedOnceWritten(t *testing.T) {
	g := streamRig(t, tend.ProviderClaude)
	g.logLine(`{"type":"control_request","request_id":"rq-1","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"a"}}}`)
	if len(g.s.st.Requests) != 1 {
		t.Fatalf("%+v", g.s.st)
	}
	appendLine(filepath.Join(g.s.dir, answersFile), agent.Answer{Request: "rq-1", Allow: true})
	g.s.takeAnswers()
	if m := g.sent(t); m["type"] != "control_response" {
		t.Fatalf("%v", m)
	}
	wait := func() []Mark {
		for range 100 {
			if ms := g.marks(t, markResolved); len(ms) > 0 {
				return ms
			}
			sleep()
		}
		return nil
	}
	if ms := wait(); len(ms) != 1 || ms[0].ID != "rq-1" {
		t.Fatalf("%+v", ms)
	}
}

func TestCodexGivesMessagesAndResolvedRequestsBack(t *testing.T) {
	g := streamRig(t, tend.ProviderCodex)
	g.s.proto.begin("b")
	g.sent(t)
	g.feed(`{"id":1,"result":{}}`)
	g.sent(t)
	g.sent(t)
	g.feed(`{"id":2,"result":{"thread":{"id":"th-1"}}}`)
	if m := g.sent(t); params(m)["clientUserMessageId"] != briefInput {
		t.Fatalf("%v", m)
	}
	g.feed(`{"method":"turn/started","params":{"turn":{"id":"tu-1","status":"inProgress"}}}`)
	g.sends("m1")
	g.s.proto.message("m1", "remember BANANA", nil)
	steer := g.sent(t)
	if steer["method"] != "turn/steer" || params(steer)["clientUserMessageId"] != "m1" {
		t.Fatalf("%v", steer)
	}
	g.feed(`{"id":` + mustJSON(steer["id"]) + `,"error":{"code":1,"message":"turn ended"}}`)
	if m := g.sent(t); m["method"] != "turn/start" || params(m)["clientUserMessageId"] != "m1" {
		t.Fatalf("a steer too late starts a turn with the same id: %v", m)
	}
	g.logLine(`{"method":"item/started","params":{"item":{"type":"userMessage","id":"u-1","clientId":"brief","content":[]},"threadId":"th-1"}}`)
	at := g.logLine(`{"method":"item/started","params":{"item":{"type":"userMessage","id":"u-2","clientId":"m1","content":[]},"threadId":"th-1"}}`)
	g.logLine(`{"method":"item/completed","params":{"item":{"type":"userMessage","id":"u-2","clientId":"m1","content":[]},"threadId":"th-1"}}`)
	if in := g.marks(t, markInput); stateOf(g, "m1") != agent.SendSeen || len(in) != 1 || in[0].ID != "m1" || in[0].Off != at.off {
		t.Fatalf("%+v %+v", g.s.st.Sends, in)
	}
	g.logLine(`{"id":0,"method":"item/commandExecution/requestApproval","params":{"command":"touch x","itemId":"x"}}`)
	g.logLine(`{"id":1,"method":"item/fileChange/requestApproval","params":{"itemId":"y"}}`)
	appendLine(filepath.Join(g.s.dir, answersFile), agent.Answer{Request: "rpc-0", Allow: true})
	g.s.takeAnswers()
	g.sent(t)
	g.gone(t, "rpc-0")
	g.logLine(`{"method":"serverRequest/resolved","params":{"threadId":"th-1","requestId":0}}`)
	rat := g.logLine(`{"method":"serverRequest/resolved","params":{"threadId":"th-1","requestId":1}}`)
	res := g.marks(t, markResolved)
	if len(res) != 2 || res[0].ID != "rpc-0" || res[1].ID != "rpc-1" || res[1].Off != rat.off {
		t.Fatalf("%+v", res)
	}
	if len(g.s.st.Requests) != 0 || g.s.st.Attention != "" {
		t.Fatalf("a request resolved by itself no longer waits: %+v", g.s.st)
	}
}

func TestAnInterruptEndsOnlyTheTurnItNames(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "100", "--every", "100ms"), Brief: "b"})
	wait(t, n, s.Run, func(s Snapshot) bool { return s.Last != "" })
	if got, err := n.Interrupt(InterruptParams{Run: s.Run, Turn: 2, ID: "int_later"}); err != nil || got.ID != "int_later" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := n.Interrupt(InterruptParams{Run: s.Run, Turn: 1, ID: "bad id"}); wire.Code(err) != wire.CodeBadRequest {
		t.Fatal(err)
	}
	got, err := n.Interrupt(InterruptParams{Run: s.Run, Turn: 1})
	if err != nil || !strings.HasPrefix(got.ID, "int_") {
		t.Fatalf("%+v %v", got, err)
	}
	if again, err := n.Interrupt(InterruptParams{Run: s.Run, Turn: 1, ID: got.ID}); err != nil || again.ID != got.ID {
		t.Fatalf("the same interrupt again is a no-op: %v", err)
	}
	end := wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
	if end.State.State != StateStopped || end.Reason != "interrupted" || end.ExitCode == nil {
		t.Fatalf("the agent exits and the run is stopped: %+v", end)
	}
	ms := marksOf(t, n, s.Run)
	var ints []Mark
	for _, m := range ms {
		if m.Event == markInterrupt {
			ints = append(ints, m)
		}
	}
	if len(ints) != 1 || ints[0].ID != got.ID || ints[0].N != 1 {
		t.Fatalf("one interrupt, of turn 1: %+v", ints)
	}
	if log := logOf(t, n, s.Run); !strings.Contains(log, `"request_id":"`+got.ID+`"`) || !strings.Contains(log, "error_during_execution") {
		t.Fatalf("claude is asked with the interrupt's own id:\n%s", log)
	}
	if again, err := n.Interrupt(InterruptParams{Run: s.Run, Turn: 1, ID: "int_after"}); err != nil || again.State.State != StateStopped {
		t.Fatalf("an ended run's turns are over: %v", err)
	}
}

func TestOnlyAStreamRunIsInterrupted(t *testing.T) {
	n := New(t.TempDir())
	run := NewRunID()
	dir := n.runDir(run)
	os.MkdirAll(dir, 0o700)
	writeJSON(filepath.Join(dir, "spec.json"), Spec{Run: run, Created: time.Now()})
	writeJSON(filepath.Join(dir, "state.json"), State{Rev: 1, State: StateRunning, Sup: os.Getpid()})
	if _, err := n.Interrupt(InterruptParams{Run: run, Turn: 1}); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("%v", err)
	}
}

func sleep() { time.Sleep(20 * time.Millisecond) }
