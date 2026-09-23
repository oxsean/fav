package index

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

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

func rolloutPath(codex string, day int, sid string) string {
	d := sprintf("%02d", day)
	return filepath.Join(codex, "sessions", "2026", "09", d, "rollout-2026-09-"+d+"T02-00-00-"+sid+".jsonl")
}

func codexLines(sid, cwd, originator string, prompts ...string) string {
	var b strings.Builder
	b.WriteString(sprintf(`{"timestamp":"2026-09-11T02:00:00Z","type":"session_meta","payload":{"session_id":%q,"cwd":%q,"originator":%q}}`+"\n", sid, cwd, originator))
	for _, p := range prompts {
		b.WriteString(sprintf(`{"timestamp":"2026-09-11T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`+"\n", p))
	}
	return b.String()
}

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

	env := "/Users/me/work/env"
	c1 := rolloutPath(codex, 11, "cccc")
	write(t, c1, codexLines("cccc", env, "Codex Desktop", "把 env 仓库的 CI 修好", "<environment_context>x</environment_context>"))
	write(t, rolloutPath(codex, 12, "dddd"), codexLines("cccc", env, "Codex Desktop", "再跑一遍"))
	write(t, rolloutPath(codex, 12, "eeee"), codexLines("eeee", env, "codex_exec", "exec 跑的"))
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

func TestClaudeContinuationChainIsOneSession(t *testing.T) {
	claude, _ := setup(t)
	dir := filepath.Join(claude, "projects", "-Users-me-work-webapp")
	write(t, filepath.Join(dir, "old1.jsonl"), `{"type":"ai-title","aiTitle":"续接测试","sessionId":"old1"}`+"\n"+
		claudeLines("第一段第一句", "第一段第二句", "第一段第三句")+
		`{"type":"continued-in","timestamp":"2026-09-10T02:00:00Z","sessionId":"old1","continuedInSessionId":"new2"}`+"\n")
	write(t, filepath.Join(dir, "new2.jsonl"), `{"type":"ai-title","aiTitle":"续接测试","sessionId":"new2"}`+"\n"+
		strings.ReplaceAll(claudeLines("This session is being continued from a previous conversation", "第二段"), "01:00", "03:00"))
	idx, err := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Refresh()
	ss := idx.Sessions()
	if len(ss) != 1 {
		t.Fatalf("chain should be one session, got %d", len(ss))
	}
	s := ss[0]
	if s.SessionID != "new2" || len(s.Aliases) != 1 || s.Aliases[0] != "old1" || s.Turns != 4 || filepath.Base(s.Path) != "new2.jsonl" {
		t.Fatalf("got id=%s aliases=%v turns=%d path=%s", s.SessionID, s.Aliases, s.Turns, s.Path)
	}

	st, err := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	r := &fav.Rec{ID: fav.NewID(), Provider: fav.ProviderClaude, SessionID: "old1", Title: "收藏在老 id 上", TranscriptPath: filepath.Join(dir, "old1.jsonl")}
	if err := st.Put(r); err != nil {
		t.Fatal(err)
	}
	if unfav := idx.Attach(st, nil); len(unfav) != 0 {
		t.Fatalf("the favorited chain must not show up as unfavorited: %d", len(unfav))
	}
	if r.SessionID != "new2" || filepath.Base(r.TranscriptPath) != "new2.jsonl" || r.Turns != 4 { // the continuation summary is not a human turn
		t.Fatalf("record should follow the chain: id=%s path=%s turns=%d", r.SessionID, r.TranscriptPath, r.Turns)
	}
}

func TestCodexArchivedSessionsStayFound(t *testing.T) {
	_, codex := setup(t)
	live := rolloutPath(codex, 11, "ffff")
	fix := "把 env 仓库的 CI 修好"
	write(t, live, codexLines("ffff", "/Users/me/work/env", "codex-tui", fix, fix, fix))
	idx, err := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Refresh()
	store, err := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rec := &fav.Rec{ID: "r1", Provider: fav.ProviderCodex, SessionID: "ffff", Title: "修 CI", TranscriptPath: live, FavoritedAt: &now}
	if err := store.Put(rec); err != nil {
		t.Fatal(err)
	}

	archived := filepath.Join(codex, "archived_sessions", filepath.Base(live))
	os.MkdirAll(filepath.Dir(archived), 0o755)
	if err := os.Rename(live, archived); err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Refresh()
	idx.Attach(store, nil)
	got := store.Get("r1")
	if got.TranscriptPath != archived || !got.CodexArchived {
		t.Fatalf("archiving in Codex moves the file: the record follows it and says so: %+v", got)
	}
	if files := SessionFiles(fav.ProviderCodex, "ffff"); len(files) != 1 || files[0] != archived {
		t.Fatalf("trash and move see the archived rollout: %v", files)
	}
}

func TestAgentScratch(t *testing.T) {
	tmp := os.TempDir()
	cases := map[string]bool{
		filepath.Join(tmp, "claude-501", "x", "scratchpad"): true,
		filepath.Join(tmp, "notes"):                         false,
		"":                                                  false,
	}
	if runtime.GOOS != "windows" {
		// a person's own scratch work stays listed
		maps.Copy(cases, map[string]bool{
			"/private/tmp/claude-501/-Users-ozn-dev-fav-0a24/scratchpad": true,
			"/private/var/folders/m7/xx/T/claude-review-2370e1":          true,
			"/tmp/claude-501/x":      true,
			"/private/tmp":           false,
			"/tmp/notes":             false,
			"/Users/me/claude-tools": false,
		})
	}
	for cwd, want := range cases {
		if got := AgentScratch(cwd); got != want {
			t.Errorf("AgentScratch(%q) = %v", cwd, got)
		}
	}
}

func TestRecapBecomesTheSummary(t *testing.T) {
	claude, codex := setup(t)
	write(t, filepath.Join(claude, "projects", "-Users-me-work", "rrrr.jsonl"),
		claudeLines("帮我看看登录为什么收不到邮件", "继续", "好")+
			`{"type":"system","subtype":"away_summary","content":"旧的回顾","timestamp":"2026-09-10T01:00:10Z"}`+"\n"+
			`{"type":"system","subtype":"away_summary","content":"修登录邮件：加了 SPF。下一步等 DNS 生效。 (disable recaps in /config)","timestamp":"2026-09-10T01:00:20Z"}`+"\n")
	fix := "把 env 仓库的 CI 修好"
	done := `{"timestamp":"2026-09-11T02:00:09Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"CI 修好了：缓存键写错。\n\n细节：……"}}` + "\n"
	write(t, rolloutPath(codex, 11, "xxxx"), codexLines("xxxx", "/Users/me/work/env", "codex-tui", fix, fix, fix)+done)
	idx, _ := (&Index{files: map[string]*File{}}).Refresh()
	got := map[string]*fav.Rec{}
	for _, s := range idx.Sessions() {
		got[s.SessionID] = s.Rec()
	}
	if r := got["rrrr"]; r == nil || !r.Recap || r.Summary != "修登录邮件：加了 SPF。下一步等 DNS 生效。" {
		t.Fatalf("Claude: the newest recap, without the settings hint: %+v", r)
	}
	if r := got["xxxx"]; r == nil || !r.Recap || r.Summary != "CI 修好了：缓存键写错。" {
		t.Fatalf("Codex: the first paragraph of the last reply: %+v", r)
	}
}

func TestCacheStaysSmallWhenOneBigFileKeepsChanging(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "sessions.jsonl")
	p := filepath.Join(t.TempDir(), "live.jsonl")
	prompts := strings.Repeat("长提示语", 700)
	for i := range 200 {
		idx, err := OpenAt(cache)
		if err != nil {
			t.Fatal(err)
		}
		for j := range 3 {
			q := fmt.Sprintf("%s-%d.jsonl", p, j)
			if idx.files[q] == nil {
				f := &File{Path: q, Provider: fav.ProviderClaude, SessionID: fmt.Sprint(j), Turns: 1}
				idx.files[q], idx.dirty = f, append(idx.dirty, f)
			}
		}
		f := &File{Path: p, Provider: fav.ProviderClaude, SessionID: "live", Turns: i + 1, Prompts: prompts}
		idx.files[p], idx.dirty = f, append(idx.dirty, f)
		if err := idx.Save(); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := OpenAt(cache)
	if err != nil {
		t.Fatal(err)
	}
	if f := idx.files[p]; f == nil || f.Turns != 200 || idx.Len() != 4 {
		t.Fatalf("the newest line wins: %+v, %d files", f, idx.Len())
	}
	st, _ := os.Stat(cache)
	if line := int64(len(prompts)); st.Size() > 3*line+64<<10 {
		t.Fatalf("stale rewrites of one big line piled up: cache %d bytes, the line is ~%d", st.Size(), line)
	}
}

func TestAnUnreadableCacheLineIsRewrittenAway(t *testing.T) {
	claude, _ := setup(t)
	cache := filepath.Join(t.TempDir(), "sessions.jsonl")
	for _, id := range []string{"aaaa", "bbbb", "cccc"} {
		write(t, filepath.Join(claude, "projects", "-w", id+".jsonl"), claudeLines("第一句话", "第二句话", "第三句话"))
	}
	idx, _ := OpenAt(cache)
	idx, _ = idx.Refresh()
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cache)
	lines := strings.SplitAfter(string(b), "\n")
	junk := strings.Repeat("x", 5<<20) + "\n"
	os.WriteFile(cache, []byte(lines[0]+junk+strings.Join(lines[1:], "")), 0o644)
	for range 2 {
		idx, err := OpenAt(cache)
		if err != nil {
			t.Fatal(err)
		}
		idx, _ = idx.Refresh()
		idx.Save()
	}
	if st, _ := os.Stat(cache); st.Size() > 1<<20 {
		t.Fatalf("the bad line is still in the cache: %d bytes", st.Size())
	}
	if idx, _ := OpenAt(cache); idx.Len() != 3 {
		t.Fatalf("loaded %d of 3 files", idx.Len())
	}
}
