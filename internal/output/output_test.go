package output

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// strip leaves what a case states: where an event sits (id, offset, turn) is checked on its own.
func strip(evs []Event) string {
	for i := range evs {
		evs[i].ID, evs[i].Off, evs[i].Turn = "", 0, 0
	}
	b, _ := json.Marshal(evs)
	return string(b)
}

func parsed(text string) []Event {
	evs, _, _ := Parse("f", 0, text, State{})
	return evs
}

// The Go and JS readers differed line by line; each case is one row of that table and states what both read now.
func TestEachShapeReadsAsOneReader(t *testing.T) {
	self := "tend"
	cases := []struct{ name, in, want string }{
		{"1 a fake run's claude lines read as claude, whatever the provider",
			`{"message":{"content":[{"text":"fake step 1 of 1","type":"text"}],"role":"assistant"},"type":"assistant"}`,
			`[{"off":0,"kind":"say","text":"fake step 1 of 1"}]`},
		{"2 plain text is said", "step one", `[{"off":0,"kind":"say","text":"step one"}]`},
		{"3 unknown JSON is raw", `{"hello":"world"}`, `[{"off":0,"kind":"raw","text":"{\"hello\":\"world\"}"}]`},
		{"4 a message's lines stay one event",
			`{"type":"assistant","message":{"content":[{"type":"text","text":"two\nlines"}]}}`,
			`[{"off":0,"kind":"say","text":"two\nlines"}]`},
		{"5 a line's carriage return goes", "step one\r", `[{"off":0,"kind":"say","text":"step one"}]`},
		{"8 a claude tool call",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
			`[{"off":0,"kind":"tool","tool":"Bash","call":"toolu_1","family":"shell","title":"go test ./...","input":{"command":"go test ./..."}}]`},
		{"9 a claude tool result is its own event",
			`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok\n","is_error":true}]}}`,
			`[{"off":0,"kind":"tool_result","ref":"toolu_1","output":"ok\n","lines":1,"bytes":3,"error":true}]`},
		{"9 a result in parts keeps their text",
			`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"a"},{"type":"image","source":{}},{"type":"text","text":"b"}]}]}}`,
			`[{"off":0,"kind":"tool_result","ref":"toolu_1","output":"a\nb","lines":2,"bytes":3}]`},
		{"10 claude thinks; an empty thought is nothing",
			`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"check main.go","signature":"x"},{"type":"thinking","thinking":"","signature":"y"},{"type":"redacted_thinking","data":"x"}]}}`,
			`[{"off":0,"kind":"think","text":"check main.go"}]`},
		{"10 a block no one knows is raw",
			`{"type":"user","message":{"content":[{"type":"image","source":{"type":"base64"}}]}}`,
			`[{"off":0,"kind":"raw","text":"{\"type\":\"image\",\"source\":{\"type\":\"base64\"}}"}]`},
		{"11 claude's system line", `{"type":"system","subtype":"init","session_id":"s","model":"claude-x"}`,
			`[{"off":0,"kind":"sys","name":"init","model":"claude-x"}]`},
		{"12 claude's result with its usage",
			`{"type":"result","subtype":"success","is_error":false,"result":"Done.","total_cost_usd":0.62,"duration_ms":720000,"usage":{"input_tokens":48000,"output_tokens":3100,"cache_read_input_tokens":22000}}`,
			`[{"off":0,"kind":"result","text":"Done.","dur_ms":720000,"usage":{"input":48000,"output":3100,"cache_read":22000},"cost":0.62}]`},
		{"12 a failed result says how", `{"type":"result","subtype":"error_max_turns","is_error":true}`,
			`[{"off":0,"kind":"result","text":"error_max_turns","error":true}]`},
		{"13 claude's partial text waits for the whole message", `{"type":"stream_event","event":{"delta":{"text":"fa"}}}`, `null`},
		{"14 a permission prompt is an ask with its request",
			`{"type":"control_request","request_id":"req-1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"rm -rf build"},"description":"clean"}}`,
			`[{"off":0,"kind":"tool","tool":"Bash","request":"req-1","family":"ask","title":"rm -rf build","input":{"command":"rm -rf build"}}]`},
		{"14 a question is an ask",
			`{"type":"control_request","request_id":"req-2","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[{"question":"Which db?"},{"question":"Port?"}]}}}`,
			`[{"off":0,"kind":"tool","tool":"AskUserQuestion","request":"req-2","family":"ask","title":"Which db?","more":1,"input":{"questions":[{"question":"Which db?"},{"question":"Port?"}]}}]`},
		{"15 the rest of claude's control traffic", `{"type":"control_response","response":{"subtype":"success","request_id":"x"}}` + "\n" +
			`{"type":"control_request","request_id":"y","request":{"subtype":"hook_callback"}}` + "\n" + `{"type":"control_cancel_request","request_id":"z"}`, `null`},
		{"16 a subagent's events point at its call",
			`{"type":"assistant","parent_tool_use_id":"toolu_9","message":{"content":[{"type":"text","text":"inside"}]}}`,
			`[{"off":0,"kind":"say","parent":"toolu_9","text":"inside"}]`},
		{"17 codex's user message joins its parts",
			`{"method":"item/completed","params":{"item":{"type":"userMessage","id":"u1","content":[{"type":"text","text":"Plan"},{"type":"text","text":"the task"}]}}}`,
			`[{"off":0,"kind":"user","text":"Plan\nthe task"}]`},
		{"18 codex reasons in its summary; an empty one is nothing",
			`{"method":"item/completed","params":{"item":{"type":"reasoning","id":"rs2","summary":["Checking main.go"],"content":[]}}}` + "\n" +
				`{"method":"item/completed","params":{"item":{"type":"reasoning","id":"rs1","summary":[],"content":[]}}}`,
			`[{"off":0,"kind":"think","text":"Checking main.go"}]`},
		{"19 a codex command carries its output",
			`{"method":"item/completed","params":{"item":{"type":"commandExecution","id":"e1","command":"/bin/zsh -lc 'go vet ./...\ngo test ./...'","aggregatedOutput":"bad\n","exitCode":1,"durationMs":1200,"status":"failed"}}}`,
			`[{"off":0,"kind":"cmd","tool":"commandExecution","call":"e1","family":"shell","title":"go vet ./...","more":1,"input":{"command":"/bin/zsh -lc 'go vet ./...\ngo test ./...'"},"output":"bad\n","exit":1,"dur_ms":1200,"lines":1,"bytes":4,"error":true}]`},
		{"20 a codex file change",
			`{"method":"item/completed","params":{"item":{"type":"fileChange","id":"f1","changes":[{"path":"main.go","kind":{"type":"update"},"diff":"@@ -1 +1,2 @@\n-a\n+b\n+c"},{"path":"b.go","diff":""}],"status":"completed"}}}`,
			`[{"off":0,"kind":"edit","tool":"fileChange","call":"f1","family":"edit","title":"main.go +2 −1","more":1,"files":["main.go","b.go"],"edits":[{"path":"main.go","op":"modify","add":2,"del":1,"hunks":1,"preview":["@@ -1 +1,2 @@","-a","+b","+c"]},{"path":"b.go","op":"modify","add":0,"del":0,"hunks":0}]}]`},
		{"21 a codex MCP call",
			`{"method":"item/completed","params":{"item":{"type":"mcpToolCall","id":"c1","server":"gitea","tool":"issue_read","arguments":{"number":6,"repo":"x/y"},"result":{"content":[{"type":"text","text":"issue 6"}]}}}}`,
			`[{"off":0,"kind":"mcp","tool":"issue_read","server":"gitea","call":"c1","family":"mcp","title":"gitea.issue_read x/y","input":{"number":6,"repo":"x/y"},"output":"issue 6","lines":1,"bytes":7}]`},
		{"22 codex's approvals and questions are asks with their request",
			`{"method":"item/commandExecution/requestApproval","id":0,"params":{"itemId":"e2","command":"/bin/zsh -lc 'go test ./...'","reason":"write the build cache"}}` + "\n" +
				`{"method":"item/fileChange/requestApproval","id":"a","params":{"reason":"edit outside"}}` + "\n" +
				`{"method":"item/tool/requestUserInput","id":4,"params":{"questions":[{"id":"q","question":"Which db?"}]}}`,
			`[{"off":0,"kind":"tool","tool":"shell","request":"rpc-0","family":"ask","title":"go test ./...","input":{"itemId":"e2","command":"/bin/zsh -lc 'go test ./...'","reason":"write the build cache"}},` +
				`{"off":0,"kind":"tool","tool":"apply_patch","request":"rpc-\"a\"","family":"ask","title":"edit outside","input":{"reason":"edit outside"}},` +
				`{"off":0,"kind":"tool","tool":"question","request":"rpc-4","family":"ask","title":"Which db?","input":{"questions":[{"id":"q","question":"Which db?"}]}}]`},
		{"23 codex warns and fails",
			`{"method":"warning","params":{"message":"Skill descriptions were shortened"}}` + "\n" +
				`{"method":"error","params":{"error":{"message":"stream lost"},"willRetry":true}}` + "\n" +
				`{"method":"error","params":{"error":{"message":"quota"},"willRetry":false}}`,
			`[{"off":0,"kind":"sys","text":"Skill descriptions were shortened","name":"warning","level":"warning"},` +
				`{"off":0,"kind":"sys","text":"stream lost","name":"error","level":"warning"},{"off":0,"kind":"error","text":"quota"}]`},
		{"24 codex's thread and turn start",
			`{"method":"thread/started","params":{"thread":{"id":"th1"}}}` + "\n" + `{"method":"turn/started","params":{"turn":{"id":"tu1"}}}`,
			`[{"off":0,"kind":"sys","name":"thread/started"},{"off":0,"kind":"sys","name":"turn/started"}]`},
		{"25 codex's usage is its own event",
			`{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"total":{"inputTokens":186000,"cachedInputTokens":156288,"outputTokens":2052}}}}`,
			`[{"off":0,"kind":"sys","name":"usage","usage":{"input":29712,"output":2052,"cache_read":156288}}]`},
		{"26 codex's turn ends",
			`{"method":"turn/completed","params":{"turn":{"id":"tu1","status":"completed","error":null}}}` + "\n" +
				`{"method":"turn/completed","params":{"turn":{"id":"tu2","status":"failed","error":{"message":"usage limit"}}}}`,
			`[{"off":0,"kind":"result"},{"off":0,"kind":"result","text":"usage limit","error":true}]`},
		{"27 an answer is bookkeeping, a failed one an error",
			`{"id":1,"result":{"userAgent":"x"}}` + "\n" + `{"id":9,"error":{"code":-32600,"message":"bad request"}}`,
			`[{"off":0,"kind":"error","text":"bad request"}]`},
		{"28 codex's plan",
			`{"method":"turn/plan/updated","params":{"explanation":"","plan":[{"step":"read","status":"completed"},{"step":"fix","status":"inProgress"}]}}`,
			`[{"off":0,"kind":"tool","tool":"update_plan","family":"plan","title":"1/2","input":{"explanation":"","plan":[{"step":"read","status":"completed"},{"step":"fix","status":"inProgress"}]}}]`},
		{"29 codex's partial text and bookkeeping",
			`{"method":"item/agentMessage/delta","params":{"delta":"Al"}}` + "\n" + `{"method":"item/commandExecution/outputDelta","params":{"delta":"ok"}}` + "\n" +
				`{"method":"item/started","params":{"item":{"type":"userMessage"}}}` + "\n" + `{"method":"hook/started","params":{}}` + "\n" +
				`{"method":"mcpServer/startupStatus/updated","params":{}}` + "\n" + `{"method":"account/rateLimits/updated","params":{}}` + "\n" +
				`{"method":"serverRequest/resolved","params":{"requestId":0}}` + "\n" + `{"method":"thread/status/changed","params":{}}` + "\n" +
				`{"method":"turn/diff/updated","params":{}}`,
			`null`},
		{"30 a method no one knows is raw", `{"method":"something/new","params":{"x":1}}`,
			`[{"off":0,"kind":"raw","text":"{\"method\":\"something/new\",\"params\":{\"x\":1}}"}]`},
		{"31 codex exec says and thinks",
			`{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"Fixed."}}` + "\n" + `{"type":"item.completed","item":{"id":"i1","type":"reasoning","text":"Look at x"}}`,
			`[{"off":0,"kind":"say","text":"Fixed."},{"off":0,"kind":"think","text":"Look at x"}]`},
		{"32 a codex exec command",
			`{"type":"item.completed","item":{"id":"i2","type":"command_execution","command":"ls","aggregated_output":"a\n","exit_code":0,"status":"completed"}}`,
			`[{"off":0,"kind":"cmd","tool":"command_execution","call":"i2","family":"shell","title":"ls","input":{"command":"ls"},"output":"a\n","exit":0,"lines":1,"bytes":2}]`},
		{"33 a codex exec file change",
			`{"type":"item.completed","item":{"id":"i3","type":"file_change","changes":[{"path":"a.go","kind":"update"}],"status":"completed"}}`,
			`[{"off":0,"kind":"edit","tool":"file_change","call":"i3","family":"edit","title":"a.go","files":["a.go"],"edits":[{"path":"a.go","op":"modify","add":0,"del":0,"hunks":0}]}]`},
		{"34 a codex exec MCP call that failed",
			`{"type":"item.completed","item":{"id":"i4","type":"mcp_tool_call","server":"gitea","tool":"issue_read","arguments":{"number":6},"result":null,"error":{"message":"denied"},"status":"failed"}}`,
			`[{"off":0,"kind":"mcp","tool":"issue_read","server":"gitea","call":"i4","family":"mcp","title":"gitea.issue_read","input":{"number":6},"output":"denied","lines":1,"bytes":6,"error":true}]`},
		{"35 codex exec's turn ends with its usage",
			`{"type":"turn.completed","usage":{"input_tokens":12,"cached_input_tokens":2,"output_tokens":3}}`,
			`[{"off":0,"kind":"result","usage":{"input":10,"output":3,"cache_read":2}}]`},
		{"36 codex exec fails",
			`{"type":"turn.failed","error":{"message":"usage limit"}}` + "\n" + `{"type":"error","message":"reconnecting"}`,
			`[{"off":0,"kind":"result","text":"usage limit","error":true},{"off":0,"kind":"error","text":"reconnecting"}]`},
		{"37 codex exec's thread and turn start",
			`{"type":"thread.started","thread_id":"th1"}` + "\n" + `{"type":"turn.started"}`,
			`[{"off":0,"kind":"sys","name":"thread.started"},{"off":0,"kind":"sys","name":"turn.started"}]`},
		{"38 codex exec's items in progress",
			`{"type":"item.started","item":{"id":"i2","type":"command_execution","command":"ls","status":"in_progress"}}` + "\n" + `{"type":"item.updated","item":{"id":"t","type":"todo_list","items":[]}}`,
			`null`},
		{"39 claude's user message as plain text",
			`{"type":"user","message":{"role":"user","content":"try again"}}`,
			`[{"off":0,"kind":"user","text":"try again"}]`},
		{"40 the run's own reports are not asked",
			`{"type":"control_request","request_id":"r","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"` + self + ` run note half"}}}` + "\n" +
				`{"method":"item/commandExecution/requestApproval","id":1,"params":{"command":"/bin/zsh -lc '` + self + ` run verdict pass ok'"}}`,
			`null`},
	}
	for _, c := range cases {
		if got := strip(parsed(c.in + "\n")); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// 6: a line still being written is not consumed; plain text shows as a temp event, a JSON line waits.
func TestALineWithoutItsNewlineWaits(t *testing.T) {
	evs, _, end := Parse("f", 100, "done\nhalf a li", State{})
	if end != 105 || len(evs) != 2 || evs[0].ID != "f:100:0" || !evs[1].Temp || evs[1].Key != "f:105" || evs[1].Text != "half a li" || evs[1].ID != "" {
		t.Fatalf("end %d, %+v", end, evs)
	}
	evs, _, end = Parse("f", 100, "done\n"+`{"type":"assistant","mess`, State{})
	if end != 105 || len(evs) != 1 {
		t.Fatalf("end %d, %+v", end, evs)
	}
}

// 7: a line that says several things gives each its place in the line; the id says where the line starts.
func TestALineWithSeveralEventsNumbersThem(t *testing.T) {
	text := "\n" + `{"type":"assistant","message":{"content":[{"type":"text","text":"I will run it."},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}` + "\n"
	evs, _, end := Parse("a1:9", 48212, text, State{})
	if len(evs) != 2 || evs[0].ID != "a1:9:48213:0" || evs[1].ID != "a1:9:48213:1" || evs[1].Off != 48213 || end != 48212+int64(len(text)) {
		t.Fatalf("%+v", evs)
	}
}

// A result arriving later never changes the call's event; a page read cold names every event as the whole log does.
func TestAPageReadsAsTheWholeLogDoes(t *testing.T) {
	log := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Reading."},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"a.go"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"package a"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"also run vet"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"}]}}`,
		`{"type":"result","subtype":"success","result":"Done."}`,
		`{"type":"system","subtype":"init","session_id":"s"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Again."}]}}`,
		`{"type":"result","subtype":"success","result":"Done again."}`,
	}, "\n") + "\n"
	whole, _, _ := Parse("f", 0, log, State{})
	call := whole[2]
	if call.Kind != KindTool || call.Call != "t1" || call.Output != "" || whole[3].Ref != "t1" {
		t.Fatalf("%+v", whole)
	}
	turns := []int{1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2}
	for i, e := range whole {
		if e.Turn != turns[i] {
			t.Errorf("event %d (%s) in turn %d, want %d", i, e.Kind, e.Turn, turns[i])
		}
	}
	for start := strings.Index(log, "\n") + 1; start < len(log); start += strings.Index(log[start:], "\n") + 1 {
		_, st, _ := Parse("f", 0, log[:start], State{}) // the state a page starts at, as the log's marks will give it
		page, _, _ := Parse("f", int64(start), log[start:], st)
		if a, b := jsonOf(page), jsonOf(whole[len(whole)-len(page):]); a != b {
			t.Fatalf("page from %d:\n%s\nwhole:\n%s", start, a, b)
		}
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// A turn starts with what the agent is told or does after the last result; a tool's result and a message sent into a
// running turn do not start one.
func TestTurnsStartAfterAResult(t *testing.T) {
	codex := strings.Join([]string{
		`{"method":"thread/started","params":{}}`,
		`{"method":"turn/started","params":{}}`,
		`{"method":"item/completed","params":{"item":{"type":"userMessage","content":[{"text":"brief"}]}}}`,
		`{"method":"item/completed","params":{"item":{"type":"userMessage","content":[{"text":"steer"}]}}}`,
		`{"method":"turn/completed","params":{"turn":{"status":"completed"}}}`,
		`{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"total":{}}}}`,
		`{"method":"turn/started","params":{}}`,
		`{"method":"item/completed","params":{"item":{"type":"agentMessage","text":"reply"}}}`,
	}, "\n") + "\n"
	evs, st, _ := Parse("f", 0, codex, State{})
	var got []int
	for _, e := range evs {
		got = append(got, e.Turn)
	}
	if jsonOf(got) != "[1,1,1,1,1,1,2,2]" || st != (State{Turn: 2}) {
		t.Fatalf("turns %v, state %+v", got, st)
	}
	_, st, _ = Parse("f", 0, `{"type":"result","subtype":"success"}`+"\n"+`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"x","content":"late"}]}}`+"\n", State{Turn: 4})
	if st != (State{Turn: 4, Closed: true}) {
		t.Fatalf("a result after the turn's end starts nothing: %+v", st)
	}
}

// Each family's title is the one line a reader needs; More counts what it leaves out.
func TestEachFamilyHasItsTitle(t *testing.T) {
	cases := []struct {
		tool, input, family, title string
		more                       int
	}{
		{"Bash", `{"command":"go test ./...\n  -run X\n"}`, FamilyShell, "go test ./...", 1},
		{"Bash", `{"command":"bash -lc 'make'"}`, FamilyShell, "make", 0},
		{"BashOutput", `{"bash_id":"b1"}`, FamilyShell, "BashOutput", 0},
		{"Read", `{"file_path":"/w/a.go","offset":10,"limit":50}`, FamilyRead, "/w/a.go:10-59", 0},
		{"Read", `{"file_path":"/w/a.go"}`, FamilyRead, "/w/a.go", 0},
		{"Grep", `{"pattern":"func Parse","path":"internal"}`, FamilySearch, "func Parse · internal", 0},
		{"Glob", `{"pattern":"**/*.go"}`, FamilySearch, "**/*.go", 0},
		{"Edit", `{"file_path":"a.go","old_string":"a\nb","new_string":"c"}`, FamilyEdit, "a.go +1 −2", 0},
		{"MultiEdit", `{"file_path":"a.go","edits":[{"old_string":"a","new_string":"b\nc"},{"old_string":"d","new_string":"e"}]}`, FamilyEdit, "a.go +3 −2", 0},
		{"Write", `{"file_path":"new.go","content":"package x\n\nfunc F() {}\n"}`, FamilyEdit, "new.go +3 −0", 0},
		{"Write", `{"file_path":"big.go","content":{"$blob":"ab12","bytes":9000,"lines":320}}`, FamilyEdit, "big.go +320 −0", 0},
		{"Edit", `{"file_path":"a.go","old_string":{"$blob":"cd34","bytes":5000,"lines":40},"new_string":"c"}`, FamilyEdit, "a.go +1 −40", 0},
		{"MultiEdit", `{"file_path":"a.go","edits":[{"old_string":"a","new_string":{"$omit":"new_string","bytes":5000,"lines":7,"cap":true}}]}`, FamilyEdit, "a.go +7 −1", 0},
		{"WebFetch", `{"url":"https://go.dev/doc/effective_go?x=1","prompt":"p"}`, FamilyWeb, "go.dev/doc/effective_go", 0},
		{"WebSearch", `{"query":"bubbletea v2"}`, FamilyWeb, "bubbletea v2", 0},
		{"Task", `{"description":"Find the parser","prompt":"…","subagent_type":"Explore"}`, FamilyAgent, "Find the parser", 0},
		{"TodoWrite", `{"todos":[{"content":"a","status":"completed"},{"content":"b","status":"in_progress"},{"content":"c","status":"pending"}]}`, FamilyPlan, "1/3", 0},
		{"AskUserQuestion", `{"questions":[{"question":"Which db?"}]}`, FamilyAsk, "Which db?", 0},
		{"mcp__gitea__issue_read", `{"owner":"x","number":6}`, FamilyMCP, "gitea.issue_read x", 0},
		{"SlashCommand", `{"command":"/review"}`, FamilyOther, "/review", 0},
	}
	for _, c := range cases {
		e := toolEvent(c.tool, json.RawMessage(c.input))
		if e.Family != c.family || e.Title != c.title || e.More != c.more {
			t.Errorf("%s %s: family %q title %q more %d", c.tool, c.input, e.Family, e.Title, e.More)
		}
	}
	if long := strings.Repeat("é", 300); len([]rune(toolEvent("Bash", json.RawMessage(`{"command":"`+long+`"}`)).Title)) != maxTitle {
		t.Error("a title keeps at most maxTitle runes")
	}
}

// Parse leaves a long output's middle out of the event; Whole keeps all of it.
func TestWholeKeepsWhatParseLeavesOut(t *testing.T) {
	out, _ := json.Marshal(strings.Repeat("line\n", 200))
	l := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":` + string(out) + `}]}}`
	cut, _, _ := Parse("f", 0, l+"\n", State{})
	all := Whole("f", 0, l)
	if len(cut) != 1 || cut[0].Truncated["output"] == 0 {
		t.Fatalf("Parse: %+v", cut)
	}
	if len(all) != 1 || all[0].Output != strings.Repeat("line\n", 200) || all[0].Truncated != nil || all[0].ID != cut[0].ID {
		t.Fatalf("Whole: %+v", all)
	}
}

// A long output keeps its first and last lines, says how long it was and how much it left out.
func TestALongOutputKeepsItsEnds(t *testing.T) {
	var b strings.Builder
	for i := range 100 {
		b.WriteString(strings.Repeat("x", i%7) + "\n")
	}
	s := b.String()
	out, lines, size, trunc := headTail(s)
	parts := strings.SplitAfter(s, "\n")
	head, tail := strings.Join(parts[:40], ""), strings.Join(parts[60:100], "")
	if out != head+"…\n"+tail || lines != 100 || size != len(s) || trunc["output"] != len(s)-len(head)-len(tail) {
		t.Fatalf("lines %d size %d truncated %v", lines, size, trunc)
	}
	one := strings.Repeat("y", 100<<10)
	out, lines, _, trunc = headTail(one)
	if lines != 1 || len(out) != 2*keepBytes+len("\n…\n") || trunc["output"] != len(one)-2*keepBytes {
		t.Fatalf("one long line: %d bytes out, truncated %v", len(out), trunc)
	}
	if out, _, _, trunc := headTail("short\n"); out != "short\n" || trunc != nil {
		t.Fatal("a short output stays whole")
	}
}

// Reads and searches in a row are one item until anything else comes; a call's result belongs to it; an item keeps
// its identity when an earlier page arrives.
func TestReadsAndSearchesInARowGroup(t *testing.T) {
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":"a"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r2","name":"Grep","input":{"pattern":"x"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"r1","content":"a"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"r2","content":"x"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r3","name":"Read","input":{"file_path":"b"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Now a command."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r4","name":"Read","input":{"file_path":"c"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r5","name":"Read","input":{"file_path":"d"}}]}}`,
		`{"type":"assistant","parent_tool_use_id":"task1","message":{"content":[{"type":"tool_use","id":"r6","name":"Read","input":{"file_path":"e"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r7","name":"Glob","input":{"pattern":"*"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b1","content":"ok"}]}}`,
	}
	log := strings.Join(lines, "\n") + "\n"
	evs := parsed(log)
	shape := func(evs []Event, items []Item) string {
		var s []string
		for _, it := range items {
			var calls []string
			for _, i := range it.Events {
				calls = append(calls, firstOf(evs[i].Call, evs[i].Kind))
			}
			s = append(s, strings.Join(calls, "+"))
		}
		return strings.Join(s, " ")
	}
	items := Items(evs)
	if got := shape(evs, items); got != "r1+r2+r3 say r4 b1 r5 r6 r7" {
		t.Fatalf("items %s", got)
	}
	if !items[0].Group || items[0].ID != evs[0].ID || items[1].Group {
		t.Fatalf("%+v", items[:2])
	}
	if res := Results(evs); evs[res["b1"]].Output != "ok" || evs[res["r2"]].Output != "x" {
		t.Fatalf("results %v", res)
	}
	start := len(lines[0]) + 1 + len(lines[1]) + 1
	pageEvs, _, _ := Parse("f", int64(start), log[start:], State{})
	later := Items(pageEvs)
	if got := shape(pageEvs, later); got != "tool_result tool_result r3 say r4 b1 r5 r6 r7" {
		t.Fatalf("the later page alone: %s", got)
	}
	keep := later[2].ID // r3's group, expanded by hand before the earlier page came
	again := Items(evs)
	if !again[0].Has(keep) || again[0].ID == keep {
		t.Fatal("the group that grew backwards still holds the id its state was kept by")
	}
	for i := 3; i < len(later); i++ {
		if later[i].ID != again[i-2].ID {
			t.Fatalf("item %d moved: %s vs %s", i, later[i].ID, again[i-2].ID)
		}
	}
}

// The real transcripts in testdata read without raw lines: every shape they hold is known.
func TestSampleRunsReadWithoutRawLines(t *testing.T) {
	for _, name := range []string{"testdata/claude.jsonl", "testdata/codex-app-server.jsonl"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range parsed(string(b)) {
			if e.Kind == KindRaw {
				t.Errorf("%s: raw %s", name, e.Text)
			}
		}
	}
}
