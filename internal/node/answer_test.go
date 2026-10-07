package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/tend"
)

// claude suggests its rule to the repository's settings and a mode for the whole session; an answer for the run
// passes only the rule, and only to the session.
const claudeBashAsk = `{"type":"control_request","request_id":"rq-1","request":{"subtype":"can_use_tool","tool_name":"Bash",` +
	`"input":{"command":"touch marker1 *"},"description":"Touch","permission_suggestions":[` +
	`{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"touch marker1 *"}],"behavior":"allow","destination":"localSettings"},` +
	`{"type":"addDirectories","directories":["/work"],"destination":"session"},` +
	`{"type":"setMode","mode":"acceptEdits","destination":"session"}]}}`

func answered(t *testing.T, g *codexRig, a agent.Answer) map[string]any {
	t.Helper()
	appendLine(filepath.Join(g.s.dir, answersFile), a)
	g.s.takeAnswers()
	m := g.sent(t)
	resp, _ := m["response"].(map[string]any)
	if resp == nil {
		return m
	}
	r, _ := resp["response"].(map[string]any)
	return r
}

func TestAClaudeAllowForTheRunAddsOnlyItsRuleToTheSession(t *testing.T) {
	g := streamRig(t, tend.ProviderClaude)
	g.logLine(claudeBashAsk)
	if len(g.s.st.Requests) != 1 || !g.s.st.Requests[0].AllowRun {
		t.Fatalf("a rule to add lets it be allowed for the run: %+v", g.s.st.Requests)
	}
	r := answered(t, g, agent.Answer{Request: "rq-1", Allow: true, Decision: agent.DecisionAllowRun})
	perms, _ := r["updatedPermissions"].([]any)
	want := `[{"behavior":"allow","destination":"session","rules":[{"ruleContent":"touch marker1 *","toolName":"Bash"}],"type":"addRules"}]`
	if r["behavior"] != "allow" || len(perms) != 1 || mustJSON(perms) != want {
		t.Fatalf("%s", mustJSON(r))
	}
	if s := mustJSON(r); strings.Contains(s, "localSettings") || strings.Contains(s, "setMode") || strings.Contains(s, "addDirectories") {
		t.Fatalf("nothing but the rule, never to the repository's settings: %s", s)
	}

	g.logLine(strings.Replace(claudeBashAsk, "rq-1", "rq-2", 1))
	if r := answered(t, g, agent.Answer{Request: "rq-2", Allow: true}); r["updatedPermissions"] != nil {
		t.Fatalf("an allow is this once: %s", mustJSON(r))
	}
	g.logLine(`{"type":"control_request","request_id":"rq-3","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"a"},` +
		`"permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}]}}`)
	if q := g.s.st.Requests[len(g.s.st.Requests)-1]; q.ID != "rq-3" || q.AllowRun {
		t.Fatalf("no rule, no allowing for the run: %+v", q)
	}
	if r := answered(t, g, agent.Answer{Request: "rq-3", Allow: true, Decision: agent.DecisionAllowRun}); r["behavior"] != "allow" || r["updatedPermissions"] != nil {
		t.Fatalf("it is allowed once: %s", mustJSON(r))
	}
	g.logLine(strings.Replace(claudeBashAsk, "rq-1", "rq-4", 1))
	if r := answered(t, g, agent.Answer{Request: "rq-4", Allow: true, Decision: agent.DecisionDeny}); r["behavior"] != "deny" {
		t.Fatalf("a deny decision denies: %s", mustJSON(r))
	}
}

func TestACodexAllowForTheRunAcceptsForTheSession(t *testing.T) {
	g := streamRig(t, tend.ProviderCodex)
	g.logLine(`{"id":0,"method":"item/commandExecution/requestApproval","params":{"command":"touch marker1","proposedExecpolicyAmendment":["touch","marker1"]}}`)
	g.logLine(`{"id":1,"method":"item/fileChange/requestApproval","params":{}}`)
	g.logLine(`{"id":2,"method":"item/permissions/requestApproval","params":{"permissions":{"network":true}}}`)
	for i, want := range []bool{true, true, false} {
		if g.s.st.Requests[i].AllowRun != want {
			t.Fatalf("%+v", g.s.st.Requests)
		}
	}
	for _, id := range []string{"rpc-0", "rpc-1"} {
		if m := answered(t, g, agent.Answer{Request: id, Allow: true, Decision: agent.DecisionAllowRun}); mustJSON(m["result"]) != `{"decision":"acceptForSession"}` {
			t.Fatalf("%v", m)
		}
	}
	if m := answered(t, g, agent.Answer{Request: "rpc-2", Allow: true, Decision: agent.DecisionAllowRun}); mustJSON(m["result"]) != `{"permissions":{"network":true},"scope":"turn"}` {
		t.Fatalf("%v", m)
	}
}

func TestAnAnswerThatDidNotReachTheAgentKeepsItsRequest(t *testing.T) {
	g := streamRig(t, tend.ProviderClaude)
	g.logLine(claudeBashAsk)
	g.s.in.close()
	appendLine(filepath.Join(g.s.dir, answersFile), agent.Answer{Request: "rq-1", Allow: true})
	g.s.takeAnswers()
	if len(g.s.st.Requests) != 1 || !g.s.st.Requests[0].Failed || g.s.inputs["rq-1"].tool != "Bash" {
		t.Fatalf("it waits to be answered again: %+v", g.s.st.Requests)
	}
	if ms := g.marks(t, markResolved); len(ms) != 0 {
		t.Fatalf("%+v", ms)
	}

	n := New(t.TempDir())
	run := NewRunID()
	dir := n.runDir(run)
	os.MkdirAll(dir, 0o700)
	fileio.WriteJSON(filepath.Join(dir, "spec.json"), Spec{Run: run, Stream: true, Created: time.Now()})
	fileio.WriteJSON(filepath.Join(dir, "state.json"), State{Rev: 1, State: StateRunning, Sup: os.Getpid(), Requests: g.s.st.Requests})
	unlock, err := filelock.TryLock(filepath.Join(dir, "lock")) // as its supervisor
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	appendLine(filepath.Join(dir, answersFile), agent.Answer{Request: "rq-1", Allow: true})
	if _, err := n.Answer(AnswerParams{Run: run, Answer: agent.Answer{Request: "rq-1", Message: "no"}}); err != nil {
		t.Fatal(err)
	}
	as, _ := linesFrom(filepath.Join(dir, answersFile), 0, func(a agent.Answer) bool { return true })
	if len(as) != 2 || as[1].Allow {
		t.Fatalf("a failed answer is answered again: %+v", as)
	}
}

func TestAWaitingRequestTakesADecisionItKnows(t *testing.T) {
	n := New(t.TempDir())
	if _, err := n.Answer(AnswerParams{Run: NewRunID(), Answer: agent.Answer{Request: "rq-1", Decision: "always"}}); err == nil {
		t.Fatal("a decision it does not know")
	}
}

func TestTheRunSaysWhatItIsDoing(t *testing.T) {
	g := streamRig(t, tend.ProviderClaude)
	g.logLine(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"go test ./...\nmore"}}]}}`)
	g.s.flushOut()
	if !strings.HasPrefix(g.s.st.Doing, "go test ./...") {
		t.Fatalf("%q", g.s.st.Doing)
	}
	g.logLine(`{"type":"result","subtype":"success","is_error":false,"result":"done","num_turns":1}`)
	g.s.flushOut()
	if g.s.st.Doing != "" {
		t.Fatalf("a turn's end ends what it was doing: %q", g.s.st.Doing)
	}
	var b []byte
	b, _ = json.Marshal(State{Doing: "x"})
	if !strings.Contains(string(b), `"doing":"x"`) {
		t.Fatal(string(b))
	}
}

func TestAStreamRunSaysWhatItCanDo(t *testing.T) {
	n := New(t.TempDir())
	s := start(t, n, StartParams{Task: "t_1", Profile: fake("--steps", "2", "--every", "100ms"), Brief: "b"})
	got := wait(t, n, s.Run, func(s Snapshot) bool { return s.Caps != nil })
	if c := *got.Caps; !c.Steer || !c.After || !c.Interrupt || !c.AnswerScope || !c.Questions || !c.Continue || c.Takeover {
		t.Fatalf("%+v", c)
	}
	wait(t, n, s.Run, func(s Snapshot) bool { return Terminal(s.State.State) })
}
