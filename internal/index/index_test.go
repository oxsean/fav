package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fav"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	// force an mtime change
	now := time.Now().Add(2 * time.Second)
	os.Chtimes(path, now, now)
}

const claudeUser = `{"type":"user","timestamp":"2026-09-10T01:00:%02dZ","cwd":"/Users/me/work/webapp","gitBranch":"feat/x","message":{"content":%q}}` + "\n"

func claudeLines(prompts ...string) string {
	var b strings.Builder
	for i, p := range prompts {
		b.WriteString(sprintf(claudeUser, i, p))
	}
	return b.String()
}

var sprintf = fmt.Sprintf

func setup(t *testing.T) (claude, codex string) {
	t.Helper()
	root := t.TempDir()
	claude, codex = filepath.Join(root, "claude"), filepath.Join(root, "codex")
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CODEX_HOME", codex)
	return claude, codex
}

func TestScanMergeAndIncremental(t *testing.T) {
	claude, codex := setup(t)
	cache := filepath.Join(t.TempDir(), "sessions.jsonl")

	a := filepath.Join(claude, "projects", "-Users-me-work-webapp", "aaaa.jsonl")
	write(t, a, `{"type":"ai-title","aiTitle":"AI 起的名","sessionId":"aaaa"}`+"\n"+
		`{"type":"assistant","timestamp":"2026-09-10T01:00:01Z","message":{"content":[{"type":"text","text":"好的"}]}}`+"\n"+
		`{"type":"assistant","timestamp":"2026-09-10T01:00:02Z","message":{"content":[{"type":"tool_use","name":"Bash"}]}}`+"\n"+
		claudeLines("ok", "帮我排查搜索分页为什么会重复返回", "<command-name>/model</command-name>", "继续")+
		`{"type":"user","isMeta":true,"timestamp":"2026-09-10T01:00:08Z","message":{"content":[{"type":"text","text":"# Skill 展开的正文"}]}}`+"\n"+
		`{"type":"user","timestamp":"2026-09-10T01:00:08Z","message":{"content":"\n\n<pasted_content id=\"1\">\n贴进来的话\n</pasted_content id=\"1\">\n"}}`+"\n"+
		`{"type":"user","timestamp":"2026-09-10T01:00:09Z","message":{"content":[{"type":"tool_result","content":"x"}]}}`+"\n"+
		`{"type":"custom-title","customTitle":"登录开关排障","sessionId":"aaaa"}`+"\n")
	write(t, filepath.Join(claude, "projects", "-Users-me", "sdk.jsonl"),
		`{"type":"user","entrypoint":"sdk-cli","timestamp":"2026-09-10T01:00:00Z","cwd":"/Users/me","message":{"content":"跑个脚本"}}`+"\n")
	write(t, filepath.Join(claude, "projects", "-Users-me", "quiet.jsonl"),
		`{"type":"user","timestamp":"2026-09-10T01:00:00Z","cwd":"/Users/me","message":{"content":[{"type":"tool_result","content":"x"}]}}`+"\n")

	meta := `{"timestamp":"2026-09-11T02:00:00Z","type":"session_meta","payload":{"session_id":"%s","cwd":"/Users/me/work/env","originator":"%s"}}` + "\n"
	msg := `{"timestamp":"2026-09-11T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"%s"}]}}` + "\n"
	c1 := filepath.Join(codex, "sessions", "2026", "09", "11", "rollout-2026-09-11T02-00-00-cccc.jsonl")
	write(t, c1, sprintf(meta, "cccc", "Codex Desktop")+sprintf(msg, "把 env 仓库的 CI 修好")+sprintf(msg, "<environment_context>x</environment_context>"))
	write(t, filepath.Join(codex, "sessions", "2026", "09", "12", "rollout-2026-09-12T02-00-00-dddd.jsonl"),
		sprintf(meta, "cccc", "Codex Desktop")+sprintf(msg, "再跑一遍"))
	write(t, filepath.Join(codex, "sessions", "2026", "09", "12", "rollout-2026-09-12T03-00-00-eeee.jsonl"),
		sprintf(meta, "eeee", "codex_exec")+sprintf(msg, "exec 跑的"))
	write(t, filepath.Join(codex, "session_index.jsonl"), `{"id":"cccc","thread_name":"修 env CI","updated_at":"x"}`+"\n")

	idx, err := OpenAt(cache)
	if err != nil {
		t.Fatal(err)
	}
	idx, changed := idx.Refresh()
	if !changed {
		t.Fatal("首次刷新应有变化")
	}
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	ss := idx.Sessions()
	if len(ss) != 2 {
		t.Fatalf("应只剩 2 个会话（SDK、exec、没人说话的都不列），得到 %d：%+v", len(ss), ss)
	}
	byKey := map[string]*Session{}
	for _, s := range ss {
		byKey[s.Key()] = s
	}
	cl := byKey["claude:aaaa"]
	if cl == nil || cl.Turns != 4 || cl.Replies != 1 || cl.Title != "登录开关排障" || cl.Branch != "feat/x" || cl.Cwd != "/Users/me/work/webapp" {
		t.Fatalf("Claude 会话不对：%+v", cl)
	}
	if !strings.HasPrefix(cl.First, "帮我排查") {
		t.Fatalf("短提示语「ok」不该当标题：%q", cl.First)
	}
	if !strings.Contains(cl.Prompts, "继续") || !strings.Contains(cl.Prompts, "贴进来的话") ||
		strings.Contains(cl.Prompts, "command-name") || strings.Contains(cl.Prompts, "Skill 展开") {
		t.Fatalf("提示语拼接不对：%q", cl.Prompts)
	}
	cx := byKey["codex:cccc"]
	if cx == nil || cx.Turns != 2 || cx.Title != "修 env CI" || cx.Path != c1 {
		t.Fatalf("Codex 会话没合并好：%+v", cx)
	}
	r := cx.Rec()
	if r.ID != "" || r.Project != "env" || r.Turns != 2 || r.Title != "修 env CI" {
		t.Fatalf("Rec 转换不对：%+v", r)
	}

	idx2, changed := idx.Refresh()
	if changed || len(idx2.dirty) != 0 {
		t.Fatal("没动文件不该有变化")
	}
	before := idx.files[a].Size
	appendTo(t, a, sprintf(claudeUser, 30, "然后把结论发给后端"))
	idx3, changed := idx.Refresh()
	if !changed {
		t.Fatal("追加后应有变化")
	}
	f := idx3.files[a]
	if f.Turns != 5 || f.Size <= before {
		t.Fatalf("续读没算对：turns=%d size %d→%d", f.Turns, before, f.Size)
	}
	if err := idx3.Save(); err != nil {
		t.Fatal(err)
	}
	idx4, err := OpenAt(cache)
	if err != nil {
		t.Fatal(err)
	}
	if got := idx4.files[a]; got == nil || got.Turns != 5 || got.Title != "登录开关排障" {
		t.Fatalf("缓存读回不对：%+v", got)
	}
	os.Remove(a)
	idx5, changed := idx4.Refresh()
	if !changed || idx5.files[a] != nil {
		t.Fatal("文件没了条目应消失")
	}
}

func TestHalfLineWaits(t *testing.T) {
	claude, _ := setup(t)
	p := filepath.Join(claude, "projects", "x", "h.jsonl")
	write(t, p, sprintf(claudeUser, 1, "第一句完整的提示语在这里")+`{"type":"user","message":{"content":"半`)
	f := &File{Path: p, Provider: "claude", SessionID: "h"}
	f.scan()
	if f.Turns != 1 {
		t.Fatalf("turns=%d", f.Turns)
	}
	appendTo(t, p, `句"}}`+"\n")
	f.scan()
	if f.Turns != 2 {
		t.Fatalf("补齐后 turns=%d", f.Turns)
	}
}

func TestAttachKeepsIdentity(t *testing.T) {
	claude, _ := setup(t)
	p := filepath.Join(claude, "projects", "x", "kkkk.jsonl")
	write(t, p, claudeLines("第一句完整的提示语在这里", "第二句", "第三句"))
	idx, _ := (&Index{files: map[string]*File{}}).Refresh()
	store, err := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	first := idx.Attach(store, nil)
	if len(first) != 1 || first[0].ID != "" || first[0].Turns != 3 {
		t.Fatalf("未收藏记录不对：%+v", first)
	}
	appendTo(t, p, sprintf(claudeUser, 40, "第四句"))
	idx, _ = idx.Refresh()
	second := idx.Attach(store, first)
	if second[0] != first[0] || second[0].Turns != 4 {
		t.Fatal("同一会话应沿用同一个对象并更新轮数")
	}
	now := time.Now()
	rec := &fav.Rec{ID: fav.NewID(), Provider: "claude", SessionID: "kkkk", Title: "收藏了", FavoritedAt: &now}
	if err := store.Put(rec); err != nil {
		t.Fatal(err)
	}
	if got := idx.Attach(store, second); len(got) != 0 || rec.Turns != 4 || !fav.Parse("第四句").Match(rec) {
		t.Fatalf("收藏后应挂上索引信息且能按提示语搜到：%d %+v", len(got), rec)
	}
}
