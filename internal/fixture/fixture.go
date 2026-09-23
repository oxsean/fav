// Package fixture writes a small synthetic machine — Claude and Codex transcripts, a favorites store, project
// directories — covering the session shapes fav handles, with paths native to the OS it runs on.
package fixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
)

// Session is one scenario; Listed: shown by `fav sessions status:all`; Agent: a one-shot or scratch run.
type Session struct {
	Name, Provider, ID, Cwd, Path, Title string
	Listed, Agent, Favorite              bool
}

type Dataset struct {
	Root, Claude, Codex, Home, Work, Tmp string
	Sessions                             []Session
}

func (d *Dataset) Env() []string {
	return append([]string{"FAV_HOME=" + d.Home, "CLAUDE_CONFIG_DIR=" + d.Claude, "CODEX_HOME=" + d.Codex}, paths.TempEnv(d.Tmp)...)
}

func (d *Dataset) Get(name string) Session {
	for _, s := range d.Sessions {
		if s.Name == name {
			return s
		}
	}
	panic("fixture: no session " + name)
}

const remote = "https://example.com/acme/%s.git"

type builder struct {
	*Dataset
	now   time.Time
	files []*transcript
	ends  map[string]*transcript
	recs  []*fav.Rec
	n     int
}

// Build writes the dataset under root (which must not already hold one); now anchors every timestamp.
func Build(root string, now time.Time) (*Dataset, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if index.AgentScratch(root) {
		return nil, errors.New("fixture: " + root + " is inside an agent scratch directory, where every session is hidden as an agent run")
	}
	if _, err := os.Stat(filepath.Join(root, "claude")); err == nil {
		return nil, errors.New("fixture: " + root + " already holds a dataset")
	}
	d := &Dataset{Root: root, Claude: filepath.Join(root, "claude"), Codex: filepath.Join(root, "codex"),
		Home: filepath.Join(root, "home"), Work: filepath.Join(root, "work"), Tmp: filepath.Join(root, "tmp")}
	b := &builder{Dataset: d, now: now.Truncate(time.Second), ends: map[string]*transcript{}}
	for _, dir := range []string{d.Claude, d.Codex, d.Home, d.Tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if err := b.projects(); err != nil {
		return nil, err
	}
	b.claudeSessions()
	b.codexSessions()
	for _, t := range b.files {
		if err := t.write(); err != nil {
			return nil, err
		}
	}
	if err := b.sidecars(); err != nil {
		return nil, err
	}
	var recs []byte
	for _, r := range b.recs {
		l, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		recs = append(append(recs, l...), '\n')
	}
	return d, os.WriteFile(filepath.Join(d.Home, "records.jsonl"), recs, 0o600)
}

func (b *builder) dir(parts ...string) string {
	return filepath.Join(append([]string{b.Work}, parts...)...)
}

func (b *builder) projects() error {
	moved := filepath.Join(b.Root, "dev", "legacy-app")
	for _, p := range []string{b.dir("webapp"), b.dir("notes-api"), b.dir("中文项目"), b.dir("dir with space"), moved,
		b.dir("webapp", ".claude", "worktrees", "fix-login")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	files := map[string]string{
		b.dir("webapp", "src", "auth", "callback.ts"): "export function callback() {}\n",
		b.dir("webapp", "docs", "oauth.md"):           "# OAuth\n",
		b.dir("notes-api", "internal", "page.go"):     "package internal\n",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	if _, err := exec.LookPath("git"); err == nil {
		gitInit(b.dir("webapp"), fmt.Sprintf(remote, "webapp"))
		gitInit(moved, fmt.Sprintf(remote, "legacy-app"))
	}
	return nil
}

func gitInit(dir, url string) {
	exec.Command("git", "-C", dir, "init", "-q", "-b", "main").Run()
	exec.Command("git", "-C", dir, "remote", "add", "origin", url).Run()
}

func (b *builder) claudeID() string {
	b.n++
	return fmt.Sprintf("fa%06x-0c1a-4de0-8000-%012x", b.n, b.n)
}

func (b *builder) codexID() string {
	b.n++
	return fmt.Sprintf("01a0%04x-c0de-7000-8000-%012x", b.n, b.n)
}

func (b *builder) claude(name, cwd, branch string, ago time.Duration) *claudeSession {
	s := &claudeSession{id: b.claudeID(), cwd: cwd, branch: branch, entry: "cli"}
	s.t = b.now.Add(-ago)
	s.path = filepath.Join(b.Claude, "projects", index.ClaudeProjectName(cwd), s.id+".jsonl")
	b.files, b.ends[name] = append(b.files, &s.transcript), &s.transcript
	b.Sessions = append(b.Sessions, Session{Name: name, Provider: fav.ProviderClaude, ID: s.id, Cwd: cwd, Path: s.path, Listed: true})
	return s
}

func (b *builder) codex(name, cwd string, ago time.Duration, archived bool) *codexSession {
	s := &codexSession{id: b.codexID(), cwd: cwd}
	s.t = b.now.Add(-ago)
	b.files, b.ends[name] = append(b.files, &s.transcript), &s.transcript
	s.path = b.rollout(s.id, s.t, archived)
	b.Sessions = append(b.Sessions, Session{Name: name, Provider: fav.ProviderCodex, ID: s.id, Cwd: cwd, Path: s.path, Listed: true})
	return s
}

func (b *builder) rollout(id string, t time.Time, archived bool) string {
	lt := t.Local()
	name := "rollout-" + lt.Format("2006-01-02T15-04-05") + "-" + id + ".jsonl"
	if archived {
		return filepath.Join(b.Codex, "archived_sessions", name)
	}
	return filepath.Join(b.Codex, "sessions", lt.Format("2006"), lt.Format("01"), lt.Format("02"), name)
}

func (b *builder) set(name string, f func(*Session)) {
	for i := range b.Sessions {
		if b.Sessions[i].Name == name {
			f(&b.Sessions[i])
		}
	}
}

// favorite records the session as /fav would at the end of its last turn.
func (b *builder) favorite(name, title, summary, status string, tags []string, archived bool) {
	b.set(name, func(s *Session) { s.Favorite, s.Title = true, title })
	s := b.Get(name)
	at := b.ends[name].t.Add(time.Minute)
	r := &fav.Rec{ID: fmt.Sprintf("fx%06d", len(b.recs)+1), Schema: fav.Schema, Provider: s.Provider, SessionID: s.ID, Title: title,
		Summary: summary, Project: filepath.Base(s.Cwd), Tags: tags, Status: status, Cwd: s.Cwd, TranscriptPath: s.Path,
		FavoritedAt: &at, UpdatedAt: at}
	if archived {
		r.ArchivedAt = &at
	}
	b.recs = append(b.recs, r)
}

func (b *builder) claudeSessions() {
	h, d := time.Hour, 24*time.Hour

	s := b.claude("oauth", b.dir("webapp"), "feat/oauth-callback", 2*h)
	s.mark("permission-mode", "permissionMode", "default")
	s.user("ok")
	s.user("登录页 OAuth 回调偶发 400，帮我排查一下 state 参数是不是被截断了")
	s.reply("我先看回调处理的代码，再复现一次。")
	s.tool("Bash", obj{{"command", "rg -n state src/auth"}, {"description", "Find state handling"}},
		"src/auth/callback.ts:12:  const state = url.searchParams.get('state')")
	s.tool("Edit", obj{{"file_path", b.dir("webapp", "src", "auth", "callback.ts")}, {"old_string", "get('state')"},
		{"new_string", "getAll('state').join('')"}}, "The file has been updated.")
	s.mark("ai-title", "aiTitle", "排查 OAuth 回调 400")
	s.user("<command-name>/model</command-name>\n<command-message>model</command-message>")
	s.meta("# Skill: review\n展开的技能正文")
	s.user("<pasted_content id=\"1\">\nGET /oauth/callback?code=abc&state=xyz 400 Bad Request\n</pasted_content id=\"1\">")
	s.reply("日志里 state 被 URL 编码了两次，已经在回调里只解码一次。")
	s.tool("Write", obj{{"file_path", b.dir("webapp", "docs", "oauth.md")}, {"content", "# OAuth\n\nstate 只解码一次。\n"}},
		"File created successfully.")
	s.tool("NotebookEdit", obj{{"notebook_path", b.dir("webapp", "docs", "state.ipynb")}, {"new_source", "decode(state)"}},
		"Updated cell.")
	s.tool("Write", obj{{"file_path", filepath.Join(b.Tmp, "claude-501", "scratchpad", "notes.md")}, {"content", "draft"}},
		"File created successfully.")
	s.user("好，顺便把文档补一下，然后跑一遍测试")
	s.tool("Bash", obj{{"command", "npm test -- auth"}}, "PASS src/auth/callback.test.ts\nTests: 12 passed")
	s.reply("测试全部通过，文档已补到 docs/oauth.md。")
	s.system("away_summary", "修好了 OAuth 回调 state 双重解码导致的 400，补了文档，测试通过。")
	s.mark("custom-title", "customTitle", "登录页 OAuth 回调排障")
	b.favorite("oauth", "登录页 OAuth 回调排障", "state 双重解码导致 400，已修复并补文档", fav.StatusDoing, []string{"oauth", "login"}, false)

	big := b.claude("pagination", b.dir("notes-api"), "fix/cursor-drift", d)
	big.user("Cursor pagination returns duplicate rows on page 3, find out why")
	for i := 1; i <= 60; i++ {
		big.reply(fmt.Sprintf("Step %d: checking the cursor encoding for page boundaries.", i))
		big.tool("Bash", obj{{"command", fmt.Sprintf("go test ./internal -run TestPage%d -v", i)}}, bulk(i, 40*1024))
		if i%10 == 0 {
			big.user(fmt.Sprintf("keep going, we are at step %d", i))
		}
	}
	big.tool("Edit", obj{{"file_path", b.dir("notes-api", "internal", "page.go")}, {"old_string", "id >"}, {"new_string", "(ts, id) >"}},
		"The file has been updated.")
	big.reply("The cursor only encoded the id; rows sharing a timestamp drifted. It now encodes (ts, id).")
	big.mark("ai-title", "aiTitle", "Cursor pagination drift")
	b.favorite("pagination", "Cursor pagination drift", "Cursor encoded only the id; fixed to (ts, id)", fav.StatusTodo, []string{"pagination", "go"}, false)

	cjk := b.claude("cjk-dir", b.dir("中文项目"), "main", 3*d)
	cjk.user("把 README 里的安装步骤翻译成中文，保留命令原样")
	cjk.reply("已翻译，命令和路径都没动。")
	cjk.user("再加一节常见问题")
	cjk.reply("加好了，放在最后。")
	cjk.user("常见问题里补一条代理设置")
	cjk.reply("已补充 HTTPS_PROXY 的说明。")

	wt := b.claude("worktree", b.dir("webapp", ".claude", "worktrees", "fix-login"), "worktree-fix-login", 5*h)
	wt.add(obj{{"type", "worktree-state"}, {"worktreeSession", obj{{"originalCwd", b.dir("webapp")},
		{"worktreePath", b.dir("webapp", ".claude", "worktrees", "fix-login")}, {"worktreeName", "fix-login"},
		{"worktreeBranch", "worktree-fix-login"}, {"sessionId", wt.id}}}, {"sessionId", wt.id}})
	wt.user("在 worktree 里修登录按钮的禁用状态")
	wt.reply("已修复，按钮在请求中会禁用。")
	wt.user("加一个对应的单元测试")
	wt.reply("测试已加，覆盖请求中和失败两种状态。")
	wt.user("提交到 worktree 分支")
	wt.reply("已提交。")

	old := b.claude("chain-old", b.dir("notes-api"), "main", 2*d)
	old.user("给 notes-api 加限流中间件")
	old.reply("先加了令牌桶实现。")
	next := b.claude("chain-new", b.dir("notes-api"), "main", d+2*h)
	old.add(obj{{"type", "continued-in"}, {"timestamp", stamp(old.t)}, {"sessionId", old.id}, {"continuedInSessionId", next.id}})
	next.user("接着把限流的配置项加上")
	next.reply("配置项 rate_limit.rps 已加入。")
	next.user("超限时返回 429 并带 Retry-After")
	next.reply("已返回 429，Retry-After 按令牌补充时间计算。")
	b.set("chain-old", func(s *Session) { s.Listed = false })

	sdk := b.claude("sdk", b.dir("webapp"), "main", 6*h)
	sdk.entry = "sdk-cli"
	sdk.user([]obj{{{"type", "text"}, {"text", "Summarize the diff in one line"}}})
	sdk.reply("Fix OAuth state decoding.")
	b.set("sdk", func(s *Session) { s.Listed, s.Agent = false, true })

	scratch := b.claude("scratch", filepath.Join(b.Tmp, "claude-501", "scratchpad"), "", 7*h)
	scratch.user("run the migration dry-run and report")
	scratch.reply("Dry-run clean: 3 migrations pending.")
	b.set("scratch", func(s *Session) { s.Listed, s.Agent = false, true })

	quote := b.claude("quoted", b.dir("dir with space"), "main", 8*h)
	quote.user(`title with "quotes" and it's an apostrophe; also $HOME and %PATH%`)
	quote.reply("Noted.")
	quote.mark("custom-title", "customTitle", `it's "quoted" & 50%`)
	b.favorite("quoted", `it's "quoted" & 50%`, "resume command quoting", fav.StatusTodo, []string{"quoting"}, false)

	gone := b.claude("missing-dir", b.dir("legacy-app"), "main", 4*d)
	gone.user("升级 legacy-app 的依赖")
	gone.reply("依赖已升级到最新的补丁版本。")
	gone.user("跑一遍回归")
	gone.reply("回归通过。")
	gone.user("更新 CHANGELOG")
	gone.reply("CHANGELOG 已更新。")

	short := b.claude("short", b.dir("notes-api"), "main", 10*h)
	short.user("notes-api 现在用的 Go 版本是多少")
	short.reply("go.mod 里是 1.27。")
	b.set("short", func(s *Session) { s.Listed = false })

	arch := b.claude("archived", b.dir("webapp"), "main", 40*d)
	arch.user("初始化 webapp 的 CI 流水线")
	arch.reply("CI 已配置：lint、test、build 三个阶段。")
	b.favorite("archived", "初始化 webapp CI", "三个阶段的 CI", fav.StatusDone, []string{"ci"}, true)
}

func (b *builder) codexSessions() {
	h, d := time.Hour, 24*time.Hour

	cli := b.codex("codex-cli", b.dir("webapp"), 4*h, false)
	cli.meta("codex_cli_rs", "", fmt.Sprintf(remote, "webapp"))
	cli.context("<environment_context>\n  <cwd>" + cli.cwd + "</cwd>\n</environment_context>")
	cli.turnContext()
	cli.say("webapp 的 CI 在 lint 阶段失败了，帮我修好")
	cli.exec("npm run lint", "src/app.ts:3:7  error  'unused' is assigned a value but never used")
	cli.patch("*** Update File: src/app.ts\n@@\n-const unused = 1\n*** Add File: " + b.dir("webapp", "scripts", "lint.sh") + "\n+npm run lint\n")
	cli.reply("删掉了未使用的变量，lint 通过。")
	cli.say("再确认一下 build")
	cli.exec("npm run build", "built in 2.1s")
	cli.reply("build 正常。")
	cli.done("lint 报错已修复，build 通过。")
	b.favorite("codex-cli", "修 webapp CI", "lint 未使用变量", fav.StatusTodo, []string{"ci", "lint"}, false)

	app := b.codex("codex-desktop", b.dir("notes-api"), d+3*h, false)
	app.desktop = true
	app.meta("Codex Desktop", "", fmt.Sprintf(remote, "notes-api"))
	app.line("response_item", obj{{"type", "message"}, {"role", "developer"}, {"content", []obj{{{"type", "input_text"}, {"text", "<permissions instructions>"}}}}})
	app.turnContext()
	app.say("Add request logging with a trace id to notes-api")
	app.patch("*** Add File: internal/trace.go\n+package internal\n")
	app.reply("Added a middleware that tags each request with a trace id.")
	app.say("Propagate the trace id to outgoing HTTP calls")
	app.reply("The client now forwards the X-Trace-Id header.")
	app.say("Log the trace id on panics too")
	app.reply("The recover middleware logs it.")
	app.done("Request logging with trace ids is in place.")

	first := b.codex("codex-resumed", b.dir("notes-api"), 3*d, false)
	first.meta("codex_cli_rs", "", "")
	first.say("写一个导出 notes 为 CSV 的命令")
	first.reply("已加 export 子命令。")
	again := &codexSession{id: first.id, cwd: first.cwd}
	again.t = b.now.Add(-2 * d)
	again.path = b.rollout(again.id, again.t, false)
	b.files = append(b.files, &again.transcript)
	again.meta("codex_cli_rs", "", "")
	again.say("CSV 里加上创建时间列")
	again.reply("已加 created_at 列。")
	again.say("时间统一用 UTC")
	again.reply("已改成 UTC 的 RFC 3339。")

	ex := b.codex("codex-exec", b.dir("webapp"), 9*h, false)
	ex.meta("codex_exec", "", "")
	ex.say("print the current version")
	ex.reply("1.4.2")
	b.set("codex-exec", func(s *Session) { s.Listed, s.Agent = false, true })

	sub := b.codex("codex-subagent", b.dir("webapp"), 4*h-10*time.Minute, false)
	sub.meta("codex_cli_rs", cli.id, "")
	sub.say("check the lint config only")
	sub.reply("Lint config is fine.")
	b.set("codex-subagent", func(s *Session) { s.Listed, s.Agent = false, true })

	archived := b.codex("codex-archived", b.dir("webapp"), 20*d, true)
	archived.meta("codex_cli_rs", "", "")
	archived.say("清理旧的 feature flag 代码")
	archived.reply("删掉了三个已全量的开关。")
	archived.say("相关测试也删掉")
	archived.reply("已删除。")
	archived.say("跑一遍全量测试")
	archived.reply("全部通过。")
	archived.done("清理完成。")

	lost := b.codex("codex-missing-dir", b.dir("legacy-app"), 5*d, false)
	lost.meta("codex_cli_rs", "", fmt.Sprintf(remote, "legacy-app"))
	lost.say("legacy-app 的 Dockerfile 换成多阶段构建")
	lost.reply("已改为多阶段构建，镜像小了 60%。")
	b.favorite("codex-missing-dir", "legacy-app 多阶段构建", "项目目录已被移走", fav.StatusDone, []string{"docker"}, false)
	b.recs[len(b.recs)-1].GitRemote = strings.TrimSuffix(fmt.Sprintf(remote, "legacy-app"), ".git")
}

func (b *builder) sidecars() error {
	index := map[string]string{b.Get("codex-cli").ID: "修 webapp CI", b.Get("codex-desktop").ID: "Request logging with trace ids"}
	var lines []string
	for id, name := range index {
		l, _ := json.Marshal(map[string]string{"id": id, "thread_name": name, "updated_at": stamp(b.now)})
		lines = append(lines, string(l))
	}
	if err := os.WriteFile(filepath.Join(b.Codex, "session_index.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	settings, _ := json.MarshalIndent(map[string]any{"numStartups": 3, "projects": map[string]any{
		b.dir("webapp"): map[string]any{"allowedTools": []string{}}, b.dir("legacy-app"): map[string]any{"allowedTools": []string{}}}}, "", "  ")
	if err := os.WriteFile(filepath.Join(b.Claude, ".claude.json"), settings, 0o644); err != nil {
		return err
	}
	stale, _ := json.Marshal(map[string]any{"pid": 999999, "sessionId": b.Get("oauth").ID, "cwd": b.Get("oauth").Cwd,
		"kind": "interactive", "status": "idle", "statusUpdatedAt": b.now.UnixMilli()})
	if err := os.MkdirAll(filepath.Join(b.Claude, "sessions"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(b.Claude, "sessions", "999999.json"), stale, 0o644)
}

// bulk is n bytes of tool output: big enough that transcript paging crosses its 1 MB chunks.
func bulk(step, n int) string {
	var sb strings.Builder
	for i := 0; sb.Len() < n; i++ {
		fmt.Fprintf(&sb, "=== RUN   TestPage%d/row_%04d\n    page_test.go:41: cursor=%08x rows=50 dup=0\n", step, i, step*7919+i)
	}
	return sb.String()
}
