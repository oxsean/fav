package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPulseClaude(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"type":"user","timestamp":"2026-09-22T10:00:00Z","message":{"role":"user","content":"旧的提示"}}`,
		`{"type":"assistant","timestamp":"2026-09-22T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"旧的回复"}],"usage":{"input_tokens":1,"cache_creation_input_tokens":10,"cache_read_input_tokens":100}}}`,
		`{"type":"user","timestamp":"2026-09-22T10:01:00Z","message":{"role":"user","content":"把分页修好"}}`,
		`{"type":"assistant","timestamp":"2026-09-22T10:01:05Z","message":{"role":"assistant","content":[{"type":"text","text":"先看排序键。\n第二行"}],"usage":{"input_tokens":2,"cache_creation_input_tokens":2224,"cache_read_input_tokens":507254}}}`,
		`{"type":"assistant","timestamp":"2026-09-22T10:01:09Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"rg cursor"}}],"usage":{"input_tokens":3,"cache_creation_input_tokens":2300,"cache_read_input_tokens":509480}}}`,
		`{"type":"user","timestamp":"2026-09-22T10:01:10Z","message":{"role":"user","content":[{"type":"tool_result","content":"x"}]}}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	p, ok := ReadPulse(path)
	if !ok || p.Reply != "先看排序键。 第二行" || p.Context != 3+2300+509480 || p.Window != 0 || p.TurnAt.UTC().Format("15:04") != "10:01" {
		t.Fatalf("Claude: newest reply, the last request's context, the last prompt: %+v", p)
	}
}

func TestPulseCodex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	lines := []string{
		`{"timestamp":"2026-09-22T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"修 CI"}]}}`,
		`{"timestamp":"2026-09-22T10:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"CI 修好了"}]}}`,
		`{"timestamp":"2026-09-22T10:00:06Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":104425},"model_context_window":258400}}}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	p, ok := ReadPulse(path)
	if !ok || p.Reply != "CI 修好了" || p.Context != 104425 || p.Window != 258400 {
		t.Fatalf("Codex: token_count carries the window: %+v", p)
	}
	os.WriteFile(path, []byte(strings.Join(lines[:1], "\n")+"\n"), 0o644)
	if p, _ := ReadPulse(path); p.Reply != "" || p.Context != 0 {
		t.Fatalf("a rewritten file is read again: %+v", p)
	}
}

func TestPulseAskingAndFinished(t *testing.T) {
	dir := t.TempDir()
	read := func(name string, lines ...string) Pulse {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		p, _ := ReadPulse(path)
		return p
	}
	user := `{"type":"user","timestamp":"2026-09-22T10:00:00Z","message":{"role":"user","content":"做个方案"}}`
	ask := `{"type":"assistant","timestamp":"2026-09-22T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"两个方向"},{"type":"tool_use","name":"AskUserQuestion","input":{"questions":[]}}]}}`
	answer := `{"type":"user","timestamp":"2026-09-22T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"A"}]}}`
	reply := `{"type":"assistant","timestamp":"2026-09-22T10:01:05Z","message":{"role":"assistant","content":[{"type":"text","text":"按 A 做完了"}]}}`
	if p := read("a", user, ask); !p.Asking || p.Finished {
		t.Errorf("an open question waits for the user: %+v", p)
	}
	if p := read("b", user, ask, answer); p.Asking || p.Finished {
		t.Errorf("answered: working again: %+v", p)
	}
	if p := read("c", user, ask, answer, reply); p.Asking || !p.Finished || p.Size == 0 {
		t.Errorf("ends with the AI's reply: finished: %+v", p)
	}
	msg := `{"timestamp":"2026-09-22T10:00:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"还在改"}]}}`
	done := `{"timestamp":"2026-09-22T10:00:01Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"好了"}}`
	if p := read("d", done, msg); p.Finished {
		t.Errorf("Codex mid-turn after an older task_complete: not finished: %+v", p)
	}
	if p := read("e", msg, done); !p.Finished {
		t.Errorf("Codex task_complete last: finished: %+v", p)
	}
}
