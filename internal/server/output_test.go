package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A codex run in stream mode writes the app-server's JSON-RPC lines: the timeline shows its messages, commands, file
// changes, approvals and the turn's end, and leaves out the protocol's bookkeeping.
func TestTheTimelineReadsCodexAppServerLines(t *testing.T) {
	lines := `{"id":1,"result":{"userAgent":"tend/0.155.1"}}
{"method":"thread/started","params":{"thread":{"id":"th1"}}}
{"method":"mcpServer/startupStatus/updated","params":{"name":"gitea","status":"ready"}}
{"method":"turn/started","params":{"turn":{"id":"tu1"}}}
{"method":"warning","params":{"message":"Skill descriptions were shortened"}}
{"method":"hook/started","params":{"hook":"SessionStart"}}
{"method":"item/started","params":{"item":{"type":"userMessage","id":"u1","content":[{"type":"text","text":"Plan the task"}]}}}
{"method":"item/completed","params":{"item":{"type":"userMessage","id":"u1","content":[{"type":"text","text":"Plan the task"}]}}}
{"method":"item/agentMessage/delta","params":{"itemId":"m1","delta":"I will"}}
{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"m1","text":"I will read the repository first."}}}
{"method":"item/completed","params":{"item":{"type":"reasoning","id":"rs1","summary":[],"content":[]}}}
{"method":"item/completed","params":{"item":{"type":"reasoning","id":"rs2","summary":["Checking main.go"],"content":[]}}}
{"method":"item/completed","params":{"item":{"type":"commandExecution","id":"e1","command":"/bin/zsh -lc pwd","aggregatedOutput":"/work\n","exitCode":0}}}
{"method":"item/commandExecution/requestApproval","id":0,"params":{"itemId":"e2","command":"/bin/zsh -lc 'go test ./...'","reason":"write the build cache"}}
{"method":"serverRequest/resolved","params":{"requestId":0}}
{"method":"item/commandExecution/outputDelta","params":{"itemId":"e2","delta":"ok"}}
{"method":"item/completed","params":{"item":{"type":"fileChange","id":"f1","changes":[{"path":"main.go","kind":{"type":"update"},"diff":"@@ -1 +1 @@\n-a\n+b"}]}}}
{"method":"item/completed","params":{"item":{"type":"mcpToolCall","id":"c1","server":"gitea","tool":"issue_read","arguments":{"number":6},"result":{"content":[{"type":"text","text":"issue 6"}]}}}}
{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"total":{"inputTokens":186000,"cachedInputTokens":156288,"outputTokens":2052}}}}
{"method":"account/rateLimits/updated","params":{}}
{"method":"thread/status/changed","params":{"status":"idle"}}
{"method":"turn/completed","params":{"turn":{"id":"tu1","status":"completed","error":null}}}
{"method":"something/new","params":{"x":1}}
{"id":9,"error":{"code":-32600,"message":"bad request"}}`
	type event struct {
		Kind, Text, Name, Input, Result string
		Exit                            *int
	}
	var got []event
	runTimeline(t, lines, "codex", &got)
	zero := 0
	want := []event{
		{Kind: "systemEvent", Text: "thread/started"},
		{Kind: "systemEvent", Text: "turn/started"},
		{Kind: "systemEvent", Text: "Skill descriptions were shortened"},
		{Kind: "user", Text: "Plan the task"},
		{Kind: "assistant", Text: "I will read the repository first."},
		{Kind: "assistant", Text: "Checking main.go"},
		{Kind: "tool", Name: "command", Input: "/bin/zsh -lc pwd", Result: "/work\n", Exit: &zero},
		{Kind: "systemEvent", Text: "approval: /bin/zsh -lc 'go test ./...' · write the build cache"},
		{Kind: "tool", Name: "file_change", Input: "main.go", Result: "@@ -1 +1 @@\n-a\n+b"},
		{Kind: "tool", Name: "gitea.issue_read", Input: "{\n  \"number\": 6\n}", Result: "issue 6"},
		{Kind: "result", Text: "29712 input · 156288 cached · 2052 output tokens"},
		{Kind: "raw", Text: `{"method":"something/new","params":{"x":1}}`},
		{Kind: "raw", Text: `{"id":9,"error":{"code":-32600,"message":"bad request"}}`},
	}
	if len(got) != len(want) {
		t.Fatalf("%d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Text != w.Text || g.Name != w.Name || g.Input != w.Input || g.Result != w.Result ||
			(g.Exit == nil) != (w.Exit == nil) || (g.Exit != nil && *g.Exit != *w.Exit) {
			t.Errorf("event %d: %+v, want %+v", i, g, w)
		}
	}
}

// A codex exec run's lines keep reading as before.
func TestTheTimelineStillReadsCodexExecLines(t *testing.T) {
	lines := `{"type":"thread.started","thread_id":"th1"}
{"type":"item.completed","item":{"type":"agent_message","text":"done"}}
{"type":"turn.completed","usage":{"input_tokens":12,"cached_input_tokens":2,"output_tokens":3}}`
	var got []struct{ Kind, Text string }
	runTimeline(t, lines, "codex", &got)
	if len(got) != 3 || got[0].Kind != "systemEvent" || got[1].Text != "done" || got[2].Text != "10 input · 2 cached · 3 output tokens" {
		t.Fatalf("%+v", got)
	}
}

func runTimeline(t *testing.T, lines, provider string, out any) {
	t.Helper()
	nodeBin := nodeJS(t)
	b, err := os.ReadFile(filepath.Join("web", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start, end := strings.Index(src, "function normalizeOutput("), strings.Index(src, "\nfunction renderEvent(")
	if start < 0 || end < start {
		t.Fatal("app.js has no normalizeOutput before renderEvent")
	}
	script := src[start:end] + `
const events=normalizeOutput(require('fs').readFileSync(0,'utf8'),process.argv[1]).map(({index,raw,...e})=>e);
process.stdout.write(JSON.stringify(events));`
	cmd := exec.Command(nodeBin, "-e", script, provider)
	cmd.Stdin = strings.NewReader(lines)
	res, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	if err := json.Unmarshal(res, out); err != nil {
		t.Fatal(err)
	}
}
