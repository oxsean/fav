package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/output"
)

// The page draws the events internal/output read: a call and its result as one card, codex's usage on the result
// after it, a failed command open, an approval as the call it asks about, a line no one knows as it was written.
func TestThePageDrawsTheEventsOfARun(t *testing.T) {
	lines := `{"method":"thread/started","params":{"thread":{"id":"th1"}}}
{"method":"item/completed","params":{"item":{"type":"userMessage","id":"u1","content":[{"type":"text","text":"Plan the task"}]}}}
{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"m1","text":"I will read <the> repository first."}}}
{"method":"item/completed","params":{"item":{"type":"commandExecution","id":"e1","command":"/bin/zsh -lc 'go vet ./...'","aggregatedOutput":"vet: bad\n","exitCode":1}}}
{"method":"item/commandExecution/requestApproval","id":0,"params":{"itemId":"e2","command":"/bin/zsh -lc 'go test ./...'","reason":"write the build cache"}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"main.go"}]}}
{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"total":{"inputTokens":186000,"cachedInputTokens":156288,"outputTokens":2052}}}}
{"method":"turn/completed","params":{"turn":{"id":"tu1","status":"completed","error":null}}}
{"method":"something/new","params":{"x":1}}
`
	evs, _, _ := output.Parse("f", 0, lines, output.State{})
	html := drawEvents(t, evs)
	for _, want := range []string{
		"Plan the task", "I will read &lt;the&gt; repository first.",
		`<details class="tool-card failed" data-tool="f:`, `go vet ./... · exit 1</summary>`, "vet: bad",
		"go test ./...</summary>",
		"ls</summary><pre>{\n  &quot;command&quot;: &quot;ls&quot;\n}\n\nmain.go</pre>",
		"29k in, 156k cached, 2k out",
		"{&quot;method&quot;:&quot;something/new&quot;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the page lacks %q:\n%s", want, html)
		}
	}
	if n := strings.Count(html, "main.go"); n != 1 {
		t.Errorf("a result joins its call: main.go shows %d times", n)
	}
	if strings.Contains(html, "usage") {
		t.Errorf("usage shows on the result only:\n%s", html)
	}
}

// While the page is paused, the count of new events is what a newer page adds to the timeline: not a result its call,
// already shown, carries, nor the usage the result after it shows.
func TestPausedOutputCountsTheEventsItWillShow(t *testing.T) {
	call := `{"type":"assistant","message":{"content":[{"type":"text","text":"listing"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}` + "\n"
	rest := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"main.go"}]}}
{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"total":{"inputTokens":10,"outputTokens":5}}}}
{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}
{"method":"turn/completed","params":{"turn":{"id":"tu1","status":"completed","error":null}}}
`
	held, _, heldTo := output.Parse("f", 0, call, output.State{})
	all, _, allTo := output.Parse("f", 0, call+rest, output.State{})
	page := func(evs []output.Event, to int64) string {
		b, _ := json.Marshal(map[string]any{"events": evs, "from": 0, "to": to, "earliest": 0, "file": "f"})
		return string(b)
	}
	script := `const ui={online:true,authenticated:true,busy:new Set(),outputs:new Map(),raw:false,follow:false,pending:0,frozenOutput:null};
const selectedRun=()=>({id:'r1'}),renderOutput=()=>{},updateFollowLabel=()=>{},toast=()=>{},errorText=String;
ui.outputs.set('r1',` + page(held, heldTo) + `);
const api={runOutputPage:async()=>(` + page(all, allTo) + `)};
` + webCode(t, "async function fetchOutput(", "\nasync function fetchChat(") + webCode(t, "function renderEvents(", "\n// toast says message") + `
fetchOutput().then(()=>process.stdout.write(String(ui.pending)));`
	if got := runNode(t, script, ""); got != "2" {
		t.Fatalf("paused, the new events are the reply and the result: counted %s", got)
	}
}

// drawEvents runs app.js's renderEvents on evs.
func drawEvents(t *testing.T, evs []output.Event) string {
	t.Helper()
	in, _ := json.Marshal(evs)
	script := `const words={usageTokens:'{0} in, {1} cached, {2} out'};const t=k=>words[k]||k;
const esc=s=>String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
const tokens=n=>n>=1e6?(n/1e6).toFixed(1)+'M':n>=1000?Math.floor(n/1000)+'k':String(n||0);
` + webCode(t, "function renderEvents(", "\n// toast says message") + `
process.stdout.write(renderEvents(JSON.parse(require('fs').readFileSync(0,'utf8'))));`
	return runNode(t, script, string(in))
}

// webCode is app.js from the line starting with start up to end.
func webCode(t *testing.T, start, end string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("web", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, start)
	j := strings.Index(src[max(i, 0):], end)
	if i < 0 || j < 0 {
		t.Fatalf("app.js has no %q before %q", start, end)
	}
	return src[i:i+j] + "\n"
}

func runNode(t *testing.T, script, stdin string) string {
	t.Helper()
	cmd := exec.Command(nodeJS(t), "-e", script)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	return string(out)
}
