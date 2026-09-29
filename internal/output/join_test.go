package output

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
)

func TestJoinMakesTheMessagesTheAgentGaveBackYoursAndPlacesTheMarks(t *testing.T) {
	log := `{"type":"user","message":{"role":"user","content":"the brief"},"uuid":"u-brief","isReplay":true}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"on it"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"first\nsecond"},"uuid":"u-2","isReplay":true}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"first\nsecond"},"uuid":"u-2","isReplay":true}` + "\n" +
		`{"method":"item/completed","params":{"item":{"type":"userMessage","id":"i1","clientId":"s_3","content":[{"type":"text","text":"as the agent read it"}]}}}` + "\n" +
		`{"type":"result","result":"done"}` + "\n"
	evs, _, end := Parse("a1:9", 0, log, State{})
	offs := map[string]int64{}
	for _, e := range evs {
		offs[e.Text] = e.Off
	}
	at := time.Date(2026, 9, 30, 14, 20, 0, 0, time.UTC)
	marks := []Mark{
		{Event: "input", ID: "s_2", File: "a1:9", Off: offs["first\nsecond"], Pos: 40},
		{Event: "hook", Name: "check", Phase: "begin", File: "a1:9", Off: offs["on it"], Pos: 80, At: at},
		{Event: "resolved", ID: "p3", File: "a1:9", Off: end, Pos: 120, At: at},
		{Event: "interrupt", ID: "int_1", N: 1, File: "a1:9", Off: end, Pos: 160},
		{Event: "turn", N: 2, File: "a1:9", Off: end, Pos: 200},
	}
	sends := map[string]agent.Send{
		"u-2": {ID: "s_2", Text: "first", By: "u_b", Mode: agent.SendSteer, At: at},
		"s_3": {ID: "s_3", Text: "the text sent", By: "u_c"},
	}
	said := Said{
		Send: func(echo string) (agent.Send, bool) { s, ok := sends[echo]; return s, ok },
		Answer: func(request string) agent.Answer {
			return agent.Answer{Request: request, Decision: agent.DecisionAllowRun, By: "u_b"}
		},
		Interrupt: func(id string) string { return map[string]string{"int_1": "u_a"}[id] },
	}
	got := Join(evs, marks, 1, said, map[string]bool{})
	var kinds []string
	for _, e := range got {
		kinds = append(kinds, e.Kind)
	}
	want := []string{KindUser, KindMark, KindSay, KindYou, KindYou, KindResult, KindResolved, KindInterrupt}
	if b, _ := json.Marshal(kinds); string(b) != mustJSON(want) {
		t.Fatalf("kinds %s, want %v", b, want)
	}
	you := got[3]
	if you.InputID() != "s_2" || you.Text != "first" || you.By != "u_b" || you.Mode != agent.SendSteer || you.At != at.Format(time.RFC3339) {
		t.Errorf("you from claude's replay: %+v", you)
	}
	if got[4].InputID() != "s_3" || got[4].Text != "the text sent" || got[4].By != "u_c" {
		t.Errorf("you from codex's clientId: %+v", got[4])
	}
	if got[0].Text != "the brief" || got[0].Echo != "u-brief" {
		t.Errorf("the brief matches no message and stays the user's: %+v", got[0])
	}
	hook := got[1]
	if hook.ID != "a1:9:"+itoa(offs["on it"])+":m80" || hook.Mark != "hook" || hook.Name != "check" || hook.Phase != "begin" || hook.Turn != 1 {
		t.Errorf("hook mark: %+v", hook)
	}
	if r := got[6]; r.Request != "p3" || r.By != "u_b" || r.Decision != agent.DecisionAllowRun || r.At == "" {
		t.Errorf("resolved: %+v", r)
	}
	if i := got[7]; i.N != 1 || i.By != "u_a" {
		t.Errorf("interrupt: %+v", i)
	}
	if b, _ := json.Marshal(you); !json.Valid(b) || string(mustField(b, "input")) != `"s_2"` {
		t.Errorf("you's input goes out as the message id: %s", b)
	}
}

func TestJoinShowsAMessageAlreadySeenOnce(t *testing.T) {
	evs := parsed(`{"type":"user","message":{"role":"user","content":"again"},"uuid":"u-2","isReplay":true}` + "\n")
	said := Said{Send: func(string) (agent.Send, bool) { return agent.Send{ID: "s_2", Text: "again"}, true }}
	if got := Join(evs, nil, 1, said, map[string]bool{"s_2": true}); len(got) != 0 {
		t.Errorf("a message shown before is dropped, got %+v", got)
	}
}

func TestAReplayWithoutItsFlagIsAPlainUserEvent(t *testing.T) {
	evs := parsed(`{"type":"user","message":{"role":"user","content":"typed"},"uuid":"u-9"}` + "\n")
	if len(evs) != 1 || evs[0].Echo != "" {
		t.Errorf("only a replay gives a message back: %+v", evs)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func mustField(b []byte, k string) json.RawMessage {
	var m map[string]json.RawMessage
	json.Unmarshal(b, &m)
	return m[k]
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
