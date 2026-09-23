package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPromptsClaude(t *testing.T) {
	p := writeFile(t, "c.jsonl", `{"type":"mode"}
{"type":"user","timestamp":"2026-09-12T15:15:02Z","cwd":"/w/x","message":{"content":"<command-name>/fav</command-name>"}}
{"type":"user","timestamp":"2026-09-12T15:15:38Z","cwd":"/w/x","message":{"content":"把这个 zip 解开"}}
{"type":"assistant","timestamp":"2026-09-12T15:16:00Z"}
{"type":"user","timestamp":"2026-09-12T15:17:00Z","message":{"content":[{"type":"tool_result","content":"ok"}]}}
{"type":"user","timestamp":"2026-09-12T15:18:00Z","message":{"content":"再看看 SVG"}}
{"type":"user","timestamp":"2026-09-12T15:19:00Z","isCompactSummary":true,"message":{"content":"This session is being continued from a previous conversation that ran out of context."}}
{"type":"user","timestamp":"2026-09-12T15:20:00Z","message":{"content":"Stop hook feedback:\nrun the tests"}}
{"type":"user","timestamp":"2026-09-12T15:21:00Z","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}
`)
	if pl, _ := ReadPulse(p); pl.Prompt != "再看看 SVG" {
		t.Errorf("the newest typed prompt, not a recap, hook feedback or an interrupt: %+v", pl)
	}
}

func TestPromptsCodex(t *testing.T) {
	p := writeFile(t, "r.jsonl", `{"timestamp":"2026-09-11T20:40:45Z","type":"session_meta","payload":{"session_id":"s1","cwd":"/w/y"}}
{"timestamp":"2026-09-11T20:40:45Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<recommended_plugins>..."}]}}
{"timestamp":"2026-09-11T20:40:46Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"这是关于组织和角色"}]}}
{"timestamp":"2026-09-11T20:41:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"好"}]}}
`)
	if pl, _ := ReadPulse(p); pl.Prompt != "这是关于组织和角色" {
		t.Errorf("Prompt = %q", pl.Prompt)
	}
}

func TestMessagesSkipLinesLongerThanAChunk(t *testing.T) {
	long := strings.Repeat("x", 2*1024*1024)
	p := writeFile(t, "all.jsonl", `{"type":"user","timestamp":"2026-09-12T15:15:38Z","message":{"content":"第一句"}}
{"type":"assistant","timestamp":"2026-09-12T15:16:00Z","message":{"content":[{"type":"text","text":"回第一句"},{"type":"tool_use","name":"Bash"}]}}
{"type":"user","timestamp":"2026-09-12T15:17:00Z","message":{"content":[{"type":"tool_result","content":"`+long+`"}]}}
{"type":"user","timestamp":"2026-09-12T15:18:00Z","message":{"content":"第二句"}}
`)
	got := Messages(p, -1, 1<<30).Msgs
	if len(got) != 3 || got[0].Text != "第二句" || got[1].Text != "回第一句" || got[2].Text != "第一句" {
		t.Fatalf("Messages = %+v", got)
	}
}

func TestMessagesSteps(t *testing.T) {
	p := writeFile(t, "steps.jsonl", `{"type":"user","timestamp":"2026-09-12T15:15:38Z","message":{"content":"跑一下测试"}}
{"type":"assistant","timestamp":"2026-09-12T15:16:00Z","message":{"content":[{"type":"text","text":"好"},{"type":"tool_use","name":"Bash","input":{"command":"go test ./...","description":"run tests"}}]}}
{"type":"user","timestamp":"2026-09-12T15:17:00Z","message":{"content":[{"type":"tool_result","content":"ok  fav 0.1s"}]}}
{"type":"assistant","timestamp":"2026-09-12T15:18:00Z","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/a/b.go"}},{"type":"tool_use","name":"Write","input":{"file_path":"/a/c.py","content":"import os\n\nprint(1)\n"}},{"type":"tool_use","name":"Bash","input":{"command":"cat <<'EOF'\n  indented\nEOF"}}]}}
{"type":"assistant","timestamp":"2026-09-12T15:19:00Z","message":{"content":[{"type":"text","text":"都过了"}]}}
`)
	got := Messages(p, -1, 1<<30).Msgs
	if len(got) != 3 || got[0].Text != "都过了" || len(got[0].Steps) != 0 {
		t.Fatalf("Messages = %+v", got)
	}
	st := got[1].Steps
	if len(st) != 5 || st[0].Tool != "Bash" || st[0].Text != "go test ./..." || !st[1].Result || st[1].Text != "ok  fav 0.1s" || st[2].Tool != "Read" || st[2].Text != "/a/b.go" {
		t.Fatalf("steps = %+v", st)
	}
	if st[3].Text != "/a/c.py\nimport os\n\nprint(1)" || st[4].Text != "cat <<'EOF'\n  indented\nEOF" {
		t.Fatalf("多行参数应保留换行和缩进：%q / %q", st[3].Text, st[4].Text)
	}
	if h := head("1\n2\n3\n4", 2); h != "1\n2\n… 还有 2 行" {
		t.Fatalf("head = %q", h)
	}
}

func TestMessagesPaging(t *testing.T) {
	var b strings.Builder
	filler := strings.Repeat("字", 20000) // 60 messages ≈ 3.6MB, spans several chunks
	const n = 60
	for i := range n {
		fmt.Fprintf(&b, `{"type":"user","timestamp":"2026-09-12T15:%02d:00Z","message":{"content":"第%d句 %s"}}`+"\n", i%60, i, filler)
		fmt.Fprintf(&b, `{"type":"assistant","timestamp":"2026-09-12T15:%02d:01Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"echo %d"}}]}}`+"\n", i%60, i)
	}
	p := writeFile(t, "pages.jsonl", b.String())
	var all []Message
	page := Messages(p, -1, 7)
	pages := 0
	for {
		pages++
		all = append(all, page.Msgs...)
		if page.Done {
			break
		}
		if len(page.Msgs) != 7 {
			t.Fatalf("没到头的页应正好 7 句，第 %d 页 %d 句", pages, len(page.Msgs))
		}
		page = Messages(p, page.From, 7)
	}
	if len(all) != n || pages != 9 {
		t.Fatalf("应分 9 页读到 %d 句，got %d 句 %d 页", n, len(all), pages)
	}
	for i, m := range all {
		want := fmt.Sprintf("第%d句", n-1-i)
		if !strings.HasPrefix(m.Text, want+" ") || m.Chars != len([]rune(want))+1+20000 {
			t.Fatalf("第 %d 条应是 %s（%d 字）：%q… chars=%d", i, want, 20006, m.Text[:20], m.Chars)
		}
		if len(m.Steps) != 1 || m.Steps[0].Text != fmt.Sprintf("echo %d", n-1-i) {
			t.Fatalf("第 %d 条的动作挂错了：%+v", i, m.Steps)
		}
	}
	if got := Messages(p, -1, 1<<30).Msgs; len(got) != n || got[n-1].Off != 0 {
		t.Fatalf("一次读全应等价：%d 句，最早一句偏移 %d", len(got), got[n-1].Off)
	}
	if recent := recentMessages(p, 3); len(recent) != 3 || !strings.HasPrefix(recent[0].Text, "第59句") {
		t.Fatalf("recentMessages = %d 句 %q", len(recent), recent[0].Text[:6])
	}
}

// over 16KB is truncated in memory and re-read by offset for the full text
func TestTextFull(t *testing.T) {
	long := strings.Repeat("长", 20000)
	p := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(p, []byte(`{"type":"user","timestamp":"2026-09-01T00:00:00Z","message":{"content":"`+long+`"}}`+"\n"), 0o644)
	got := Messages(p, -1, 5)
	if len(got.Msgs) != 1 || !Truncated(got.Msgs[0].Text) {
		t.Fatalf("应截断：%d", len(got.Msgs))
	}
	if !utf8.ValidString(got.Msgs[0].Text) {
		t.Fatal("the cut must fall on a rune boundary")
	}
	if full := TextFull(p, got.Msgs[0].Off, ""); full != long {
		t.Fatalf("回读应是原文：%d", len(full))
	}
}

func TestRecentMessagesSkipsToolsNewestFirst(t *testing.T) {
	p := writeFile(t, "s.jsonl", `{"type":"user","timestamp":"2026-09-19T10:00:00Z","message":{"content":"第一句"}}
{"type":"assistant","timestamp":"2026-09-19T10:00:01Z","message":{"content":[{"type":"text","text":"回你一句"},{"type":"tool_use","name":"Bash","input":{}}]}}
{"type":"user","timestamp":"2026-09-19T10:00:02Z","message":{"content":[{"type":"tool_result","content":"ls output"}]}}
{"type":"assistant","timestamp":"2026-09-19T10:00:03Z","message":{"content":[{"type":"tool_use","name":"Read","input":{}}]}}
{"type":"user","timestamp":"2026-09-19T10:00:04Z","message":{"content":"<system-reminder>注入</system-reminder>"}}
{"type":"user","timestamp":"2026-09-19T10:00:05Z","message":{"content":"第二句"}}
{"type":"response_item","timestamp":"2026-09-19T10:00:06Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"codex 回答"}]}}
`)
	got := recentMessages(p, 10)
	want := []string{"assistant:codex 回答", "user:第二句", "assistant:回你一句", "user:第一句"}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if g := got[i].Role + ":" + got[i].Text; g != w {
			t.Errorf("[%d] got %q want %q", i, g, w)
		}
	}
	if got := recentMessages(p, 2); len(got) != 2 || got[1].Text != "第二句" {
		t.Errorf("n=2 should give the two newest, got %+v", got)
	}
}
