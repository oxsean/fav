package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fav"
)

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestHandoffPack(t *testing.T) {
	dir := t.TempDir()
	cwd := filepath.Join(dir, "proj")
	os.MkdirAll(cwd, 0o755)
	path := filepath.Join(dir, "s.jsonl")
	var lines []string
	for i, q := range []string{"一", "二", "三", "四", "五", "六"} {
		lines = append(lines, `{"type":"user","timestamp":"2026-09-22T10:0`+string(rune('0'+i))+`:00Z","message":{"role":"user","content":"要求`+q+`"}}`)
	}
	lines = append(lines,
		`{"type":"assistant","timestamp":"2026-09-22T10:07:00Z","message":{"role":"assistant","content":[{"type":"text","text":"先改排序"},{"type":"tool_use","name":"Edit","input":{"file_path":`+jsonString(filepath.Join(cwd, "a.go"))+`,"old_string":"x","new_string":"y"}},{"type":"tool_use","name":"Bash","input":{"command":"cat secret.env"}}]}}`,
		`{"type":"user","timestamp":"2026-09-22T10:07:01Z","message":{"role":"user","content":[{"type":"tool_result","content":"TOKEN=abc"}]}}`,
		`{"type":"assistant","timestamp":"2026-09-22T10:08:00Z","message":{"role":"assistant","content":[{"type":"text","text":"改完了\n| a | b |\n下一步跑测试"},{"type":"tool_use","name":"Write","input":{"file_path":"/elsewhere/b.md","content":"z"}}]}}`,
	)
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	r := &fav.Rec{Provider: fav.ProviderClaude, SessionID: "abc", Title: "修分页", Summary: "分页游标漂移", Cwd: cwd, TranscriptPath: path}

	msgs := RecentMessages(path, handoffScan)
	if got := lastRequests(path, msgs, 5); !slices.Equal(got, []string{"要求二", "要求三", "要求四", "要求五", "要求六"}) {
		t.Errorf("the newest five requests, oldest first: %q", got)
	}
	if got := lastReply(path, msgs); got != "改完了\n| a | b |\n下一步跑测试" {
		t.Errorf("last reply: %q", got)
	}
	if got := changedFiles(msgs, cwd, 30); !slices.Equal(got, []string{"/elsewhere/b.md", "a.go"}) {
		t.Errorf("changed files, newest first, relative under cwd: %q", got)
	}
	pack := Handoff(r)
	for _, want := range []string{"分页游标漂移", "要求六", "改完了", "- a.go", "> 改完了\n> | a | b |\n", path} {
		if !strings.Contains(pack, want) {
			t.Errorf("pack lacks %q:\n%s", want, pack)
		}
	}
	if strings.Contains(pack, "TOKEN=abc") || strings.Contains(pack, "cat secret.env") {
		t.Errorf("tool input and output stay out of the pack:\n%s", pack)
	}
}

func TestWriteHandoffPrunesOld(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	os.MkdirAll(handoffDir(), 0o700)
	old := filepath.Join(handoffDir(), "old.md")
	os.WriteFile(old, []byte("x"), 0o600)
	past := time.Now().Add(-handoffKeep - time.Hour)
	os.Chtimes(old, past, past)
	p, err := WriteHandoff(&fav.Rec{Provider: fav.ProviderCodex, SessionID: "0199aaaa-bbbb", Title: "t"})
	if err != nil || !strings.HasPrefix(filepath.Base(p), "0199aaaa-") {
		t.Fatalf("written %s: %v", p, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("a pack past handoffKeep is removed")
	}
}

func TestForkAndStartCommands(t *testing.T) {
	c, _ := BuildFork(&fav.Rec{Provider: fav.ProviderClaude, SessionID: "s1", Cwd: "/p"})
	if got := c.ShellLine(); got != "cd /p && claude --resume s1 --fork-session" {
		t.Errorf("claude fork: %s", got)
	}
	c, _ = BuildFork(&fav.Rec{Provider: fav.ProviderCodex, SessionID: "s2"})
	if got := c.ShellLine(); got != "codex fork s2" {
		t.Errorf("codex fork: %s", got)
	}
	c, _ = BuildStart(fav.ProviderCodex, "/p", "read /x.md")
	if !slices.Equal(c.Argv(), []string{"codex", "read /x.md"}) || c.Cwd != "/p" {
		t.Errorf("codex start: %q", c.Argv())
	}
}
