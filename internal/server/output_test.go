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

// drawEvents runs app.js's renderEvents on evs.
func drawEvents(t *testing.T, evs []output.Event) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("web", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start, end := strings.Index(src, "function renderEvents("), strings.Index(src, "\n// toast says message")
	if start < 0 || end < start {
		t.Fatal("app.js has no renderEvents before toast")
	}
	in, _ := json.Marshal(evs)
	script := `const words={usageTokens:'{0} in, {1} cached, {2} out'};const t=k=>words[k]||k;
const esc=s=>String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
const tokens=n=>n>=1e6?(n/1e6).toFixed(1)+'M':n>=1000?Math.floor(n/1000)+'k':String(n||0);
` + src[start:end] + `
process.stdout.write(renderEvents(JSON.parse(require('fs').readFileSync(0,'utf8'))));`
	cmd := exec.Command(nodeJS(t), "-e", script)
	cmd.Stdin = strings.NewReader(string(in))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	return string(out)
}
