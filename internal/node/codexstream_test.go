package node

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/tend"
)

// codexRig is a stream supervisor of a codex run whose stdin the test reads.
type codexRig struct {
	s   *sup
	out *bufio.Reader
}

func newCodexRig(t *testing.T, session string) *codexRig {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	s := &sup{dir: t.TempDir(), spec: Spec{Agent: tend.ProviderCodex, Stream: true, Thread: true, Dir: "/work", Session: session},
		st: State{State: StateRunning, Stream: true}, inputs: map[string]pending{}, userDenied: map[string]bool{}}
	s.in = newStreamIn(w)
	s.proto = newProto(s)
	return &codexRig{s: s, out: bufio.NewReader(r)}
}

// sent is the next message the supervisor wrote to codex.
func (g *codexRig) sent(t *testing.T) map[string]any {
	t.Helper()
	done := make(chan map[string]any, 1)
	go func() {
		line, _ := g.out.ReadBytes('\n')
		var m map[string]any
		json.Unmarshal(line, &m)
		done <- m
	}()
	select {
	case m := <-done:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("nothing sent")
	}
	return nil
}

func (g *codexRig) feed(line string) { g.s.line([]byte(line)) }

// gone waits until request id no longer waits: its answer reached the agent.
func (g *codexRig) gone(t *testing.T, id string) {
	t.Helper()
	for range 200 {
		g.s.mu.Lock()
		waits := slices.ContainsFunc(g.s.st.Requests, func(r agent.Request) bool { return r.ID == id })
		g.s.mu.Unlock()
		if !waits {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s still waits", id)
}

func params(m map[string]any) map[string]any { p, _ := m["params"].(map[string]any); return p }

func TestACodexRunStartsAThreadAndATurnWithItsBrief(t *testing.T) {
	g := newCodexRig(t, "")
	g.s.proto.begin("do it")
	if m := g.sent(t); m["method"] != "initialize" || m["id"] != float64(1) {
		t.Fatalf("%v", m)
	}
	g.feed(`{"id":1,"result":{}}`)
	if m := g.sent(t); m["method"] != "initialized" {
		t.Fatalf("%v", m)
	}
	if m := g.sent(t); m["method"] != "thread/start" || params(m)["cwd"] != "/work" || params(m)["approvalPolicy"] != "on-request" {
		t.Fatalf("%v", m)
	}
	g.feed(`{"id":2,"result":{"thread":{"id":"th-1"}}}`)
	m := g.sent(t)
	if m["method"] != "turn/start" || params(m)["threadId"] != "th-1" || !strings.Contains(mustJSON(params(m)["input"]), "do it") {
		t.Fatalf("%v", m)
	}
	if g.s.st.Session != "th-1" {
		t.Fatalf("the thread is the run's session: %q", g.s.st.Session)
	}
}

func TestACodexRunResumesItsSession(t *testing.T) {
	g := newCodexRig(t, "th-9")
	g.s.proto.begin("more")
	g.sent(t)
	g.feed(`{"id":1,"result":{}}`)
	g.sent(t)
	if m := g.sent(t); m["method"] != "thread/resume" || params(m)["threadId"] != "th-9" {
		t.Fatalf("%v", m)
	}
}

func TestCodexApprovalsQuestionsAndSteering(t *testing.T) {
	g := newCodexRig(t, "")
	g.s.proto.begin("b")
	g.sent(t)
	g.feed(`{"id":1,"result":{}}`)
	g.sent(t)
	g.sent(t)
	g.feed(`{"id":2,"result":{"thread":{"id":"th-1"}}}`)
	g.sent(t)
	g.feed(`{"method":"turn/started","params":{"turn":{"id":"tu-1","status":"inProgress"}}}`)
	g.feed(`{"id":7,"method":"item/commandExecution/requestApproval","params":{"command":"make deploy","itemId":"x"}}`)
	if len(g.s.st.Requests) != 1 || g.s.st.Requests[0].Summary != "make deploy" || g.s.st.Attention != AttentionPermission {
		t.Fatalf("%+v", g.s.st)
	}
	req := g.s.st.Requests[0]
	appendLine(g.s.dir+"/"+answersFile, agent.Answer{Request: req.ID, Allow: true})
	g.s.takeAnswers()
	if m := g.sent(t); m["id"] != float64(7) || mustJSON(m["result"]) != `{"decision":"accept"}` {
		t.Fatalf("%v", m)
	}
	g.gone(t, req.ID)
	g.feed(`{"id":8,"method":"item/tool/requestUserInput","params":{"questions":[{"id":"q1","question":"Which DB?","options":[{"label":"pg","description":"Postgres"}]}]}}`)
	q := g.s.st.Requests[0]
	if q.Kind != agent.RequestQuestion || g.s.st.Ask != "Which DB?" || !reflect.DeepEqual(q.Questions[0].Descriptions, []string{"Postgres"}) {
		t.Fatalf("%+v", g.s.st)
	}
	appendLine(g.s.dir+"/"+answersFile, agent.Answer{Request: q.ID, Allow: true, Answers: map[string]string{"Which DB?": "pg"}})
	g.s.takeAnswers()
	if m := g.sent(t); mustJSON(m["result"]) != `{"answers":{"q1":{"answers":["pg"]}}}` {
		t.Fatalf("%v", m)
	}
	if g.s.proto.message("m1", "use tabs", nil) != nil {
		t.Fatal("message")
	}
	m := g.sent(t)
	if m["method"] != "turn/steer" || params(m)["expectedTurnId"] != "tu-1" {
		t.Fatalf("a message while a turn runs steers it: %v", m)
	}
	g.feed(`{"id":` + mustJSON(m["id"]) + `,"error":{"code":1,"message":"turn ended"}}`)
	if m := g.sent(t); m["method"] != "turn/start" || !strings.Contains(mustJSON(params(m)["input"]), "use tabs") {
		t.Fatalf("a steer too late starts a turn: %v", m)
	}
	g.feed(`{"method":"item/completed","params":{"item":{"type":"agentMessage","text":"ASK: merge now?","phase":"final_answer"}}}`)
	g.feed(`{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"total":{"inputTokens":100,"cachedInputTokens":40,"outputTokens":9}}}}`)
	g.feed(`{"method":"turn/completed","params":{"turn":{"id":"tu-1","status":"completed"}}}`)
	if g.s.out.turnDone.IsZero() || g.s.out.final != "ASK: merge now?" || g.s.out.last != "ASK: merge now?" {
		t.Fatalf("%+v", g.s.out)
	}
	if u := g.s.out.usage; u == nil || u.Input != 60 || u.CacheRead != 40 || u.Output != 9 || u.Turns != 1 {
		t.Fatalf("%+v", u)
	}
}

func TestACodexRequestTendDoesNotKnowIsRefused(t *testing.T) {
	g := newCodexRig(t, "")
	g.feed(`{"id":3,"method":"account/chatgptAuthTokens/refresh","params":{}}`)
	if m := g.sent(t); m["id"] != float64(3) || m["error"] == nil {
		t.Fatalf("%v", m)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
