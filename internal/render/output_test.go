package render

import (
	"strings"
	"testing"
)

func TestRunOutputReadsAgentLines(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain text stays", "step one", "step one"},
		{"claude init", `{"type":"system","subtype":"init","session_id":"s"}`, "- init"},
		{"claude text", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"fake step 1 of 30"}]}}`, "fake step 1 of 30"},
		{"claude tool", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`, `+ Bash {"command":"go test ./..."}`},
		{"claude user text", `{"type":"user","message":{"content":[{"type":"text","text":"try again"}]}}`, "> try again"},
		{"claude tool result", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`, ""},
		{"claude protocol", `{"type":"control_request","request_id":"ask-1","request":{"subtype":"can_use_tool"}}`, ""},
		{"claude protocol answer", `{"type":"control_response","response":{"subtype":"success"}}`, ""},
		{"claude partial", `{"type":"stream_event","event":{"delta":{"text":"fa"}}}`, ""},
		{"claude result", `{"type":"result","subtype":"success","result":"Done: 3 files changed."}`, "= Done: 3 files changed."},
		{"codex message", `{"method":"item/completed","params":{"item":{"type":"agentMessage","text":"All green."}}}`, "All green."},
		{"codex command", `{"method":"item/completed","params":{"item":{"type":"commandExecution","command":"go vet ./...","exitCode":1}}}`, "$ go vet ./... (exit 1)"},
		{"codex files", `{"method":"item/completed","params":{"item":{"type":"fileChange","changes":[{"path":"a.go"},{"path":"b.go"}]}}}`, "~ a.go b.go"},
		{"codex noise", `{"method":"item/agentMessage/delta","params":{"delta":"Al"}}`, ""},
		{"codex answer", `{"id":3,"result":{}}`, ""},
		{"codex exec message", `{"type":"item.completed","item":{"type":"agent_message","text":"Fixed."}}`, "Fixed."},
		{"codex exec command", `{"type":"item.completed","item":{"type":"command_execution","command":"ls","exit_code":0}}`, "$ ls (exit 0)"},
		{"unknown json stays", `{"hello":"world"}`, `{"hello":"world"}`},
	}
	for _, c := range cases {
		if got := strings.Join(RunOutput(c.in), "\n"); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if got := RunOutput("a\n\n{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"two\\nlines\"}]}}\n"); strings.Join(got, "|") != "a|two|lines" {
		t.Fatalf("lines split, blanks dropped: %q", got)
	}
}
