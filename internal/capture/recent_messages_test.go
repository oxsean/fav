package capture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecentMessagesSkipsToolsNewestFirst(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(p, []byte(`{"type":"user","timestamp":"2026-09-19T10:00:00Z","message":{"content":"第一句"}}
{"type":"assistant","timestamp":"2026-09-19T10:00:01Z","message":{"content":[{"type":"text","text":"回你一句"},{"type":"tool_use","name":"Bash","input":{}}]}}
{"type":"user","timestamp":"2026-09-19T10:00:02Z","message":{"content":[{"type":"tool_result","content":"ls output"}]}}
{"type":"assistant","timestamp":"2026-09-19T10:00:03Z","message":{"content":[{"type":"tool_use","name":"Read","input":{}}]}}
{"type":"user","timestamp":"2026-09-19T10:00:04Z","message":{"content":"<system-reminder>注入</system-reminder>"}}
{"type":"user","timestamp":"2026-09-19T10:00:05Z","message":{"content":"第二句"}}
{"type":"response_item","timestamp":"2026-09-19T10:00:06Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"codex 回答"}]}}
`), 0o600)
	got := RecentMessages(p, 10)
	want := []string{"assistant:codex 回答", "user:第二句", "assistant:回你一句", "user:第一句"}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if g := got[i].Role + ":" + got[i].Text; g != w {
			t.Errorf("[%d] got %q want %q", i, g, w)
		}
	}
	if got := RecentMessages(p, 2); len(got) != 2 || got[1].Text != "第二句" {
		t.Errorf("n=2 should give the two newest, got %+v", got)
	}
}
