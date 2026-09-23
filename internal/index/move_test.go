package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
)

func TestMoveProject(t *testing.T) {
	posixOnly(t)
	claude, codex := setup(t)
	t.Setenv("FAV_HOME", t.TempDir())
	old, new := "/Users/me/work/webapp", "/Users/me/dev/webapp"
	enc := func(cwd string) string {
		return filepath.Join(claude, "projects", claudeEnc.ReplaceAllString(cwd, "-"))
	}

	// Claude: one session in the root, one in a worktree subdir, one live; the old project dir also holds memory/
	a := filepath.Join(enc(old), "aaaa.jsonl")
	write(t, a, claudeLines("修 webapp 登录", "再看一下")+`{"type":"x","cwd":"file:///Users/me/work/webapp","cwd2":"/Users/me/work/webapp-other"}`+"\n")
	write(t, filepath.Join(enc(old), "aaaa", "sub.jsonl"), "{}\n")
	write(t, filepath.Join(enc(old), "memory", "MEMORY.md"), "# m\n")
	sub := filepath.Join(enc(old+"/wt/feat"), "bbbb.jsonl")
	write(t, sub, strings.ReplaceAll(claudeLines("worktree 里的活", "继续"), old, old+"/wt/feat"))
	live := filepath.Join(enc(old), "cccc.jsonl")
	write(t, live, claudeLines("还在跑", "别动"))
	// Codex: one under old, one elsewhere
	meta := `{"timestamp":"2026-09-11T02:00:00Z","type":"session_meta","payload":{"session_id":"%s","cwd":"%s","originator":"codex_cli_rs"}}` + "\n"
	msg := `{"timestamp":"2026-09-11T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"%s"}]}}` + "\n"
	turn := `{"type":"turn_context","payload":{"cwd":"%s"}}` + "\n"
	c1 := filepath.Join(codex, "sessions", "2026", "09", "11", "rollout-2026-09-11T02-00-00-c1c1.jsonl")
	write(t, c1, sprintf(meta, "c1c1", old)+sprintf(msg, "codex 在 webapp")+sprintf(turn, old))
	c2 := filepath.Join(codex, "sessions", "2026", "09", "11", "rollout-2026-09-11T02-00-00-c2c2.jsonl")
	write(t, c2, sprintf(meta, "c2c2", "/Users/me/work/other")+sprintf(msg, "别的项目"))
	write(t, filepath.Join(claude, ".claude.json"), `{"numStartups":3,"projects":{"/Users/me/work/webapp":{"allowedTools":["Bash"]},"/Users/me/work/other":{}}}`)

	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	store, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	rec := &fav.Rec{ID: "r1", Provider: fav.ProviderClaude, SessionID: "aaaa", Title: "登录", Cwd: old, GitRoot: old, Project: "webapp", TranscriptPath: a}
	store.Put(rec)

	plan, err := idx.PlanMove(store, map[string]capture.Live{"cccc": {}}, old, new)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Live) != 1 || plan.Live[0].SessionID != "cccc" {
		t.Fatalf("在跑的应单独列出：%+v", plan.Live)
	}
	if _, err := plan.Apply(store); err == nil {
		t.Fatal("有在跑的不该能 Apply")
	}
	plan.Live = nil
	if len(plan.Sessions) != 3 || !plan.Settings {
		t.Fatalf("应有 3 个会话要搬并且 .claude.json 里有条目：%+v", plan.Sessions)
	}
	rep, err := plan.Apply(store)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sessions != 3 || rep.Records != 1 || !rep.Settings {
		t.Fatalf("账不对：%+v", rep)
	}

	na := filepath.Join(enc(new), "aaaa.jsonl")
	b, err := os.ReadFile(na)
	if err != nil {
		t.Fatal("主会话应到新项目目录：", err)
	}
	s := string(b)
	if strings.Contains(s, `"cwd":"/Users/me/work/webapp"`) || !strings.Contains(s, `"cwd":"/Users/me/dev/webapp"`) || !strings.Contains(s, `"cwd":"file:///Users/me/dev/webapp"`) {
		t.Fatalf("cwd 应全部改写：%s", s)
	}
	if !strings.Contains(s, `"cwd2":"/Users/me/work/webapp-other"`) {
		t.Fatal("同前缀的别的字段不该动")
	}
	if _, err := os.Stat(filepath.Join(enc(new), "aaaa", "sub.jsonl")); err != nil {
		t.Fatal("子代理目录应跟着搬")
	}
	if _, err := os.Stat(filepath.Join(enc(old), "memory", "MEMORY.md")); err != nil {
		t.Fatal("旧项目目录里还有在跑的会话，memory/ 不能动")
	}
	if _, err := os.Stat(filepath.Join(enc(new+"/wt/feat"), "bbbb.jsonl")); err != nil {
		t.Fatal("子目录会话应到对应的新目录")
	}
	if b, _ := os.ReadFile(filepath.Join(enc(new+"/wt/feat"), "bbbb.jsonl")); !strings.Contains(string(b), `"cwd":"/Users/me/dev/webapp/wt/feat"`) {
		t.Fatal("子目录的 cwd 应换前缀")
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Fatal("原件应已挪走")
	}
	if b, _ := os.ReadFile(c1); !strings.Contains(string(b), `"cwd":"/Users/me/dev/webapp"`) || strings.Count(string(b), "/Users/me/dev/webapp") != 2 {
		t.Fatalf("Codex rollout 应原地改写两处 cwd：%s", b)
	}
	if b, _ := os.ReadFile(c2); strings.Contains(string(b), "/dev/") {
		t.Fatal("别的项目不该动")
	}
	got := store.Get("r1")
	if got.Cwd != new || got.GitRoot != new || got.TranscriptPath != na || got.Project != "webapp" {
		t.Fatalf("收藏记录应改 cwd / git_root / transcript：%+v", got)
	}
	var top struct {
		Projects map[string]json.RawMessage `json:"projects"`
		N        int                        `json:"numStartups"`
	}
	b, _ = os.ReadFile(filepath.Join(claude, ".claude.json"))
	json.Unmarshal(b, &top)
	if _, ok := top.Projects[new]; !ok || top.N != 3 || len(top.Projects) != 2 {
		t.Fatalf(".claude.json 的项目 key 应改名、其余保留：%s", b)
	}
	if hits, _ := filepath.Glob(filepath.Join(claude, ".claude.json.bak-*")); len(hits) != 1 {
		t.Fatal("改 .claude.json 前应留备份")
	}
	entries, _ := fav.LoadTrash()
	if len(entries) != 3 {
		t.Fatalf("原件应登记进回收站：%d", len(entries))
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatal("在跑的那条不能动")
	}

	// Codex was rewritten in place (same size and mtime) so Rescan must force it; Claude's file is a new path
	next, _ := idx.Rescan(rep.Touched)
	for _, f := range next.files {
		if f.SessionID == "c1c1" && f.Cwd != new {
			t.Fatalf("原地改写的 rollout 索引里 cwd 应更新：%q", f.Cwd)
		}
		if f.SessionID == "aaaa" && (f.Cwd != new || f.Path != na) {
			t.Fatalf("搬走的 Claude 会话索引应指向新文件：%+v", f)
		}
	}

	// restoring a moved entry = undo: rewritten files deleted, sub-agent dir moved back, record as before
	e, err := fav.RestoreTrash(fav.ProviderClaude, "aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(na); !os.IsNotExist(err) {
		t.Fatal("撤销后改写出来的新文件应删掉")
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatal("原件应回到旧位置")
	}
	if _, err := os.Stat(filepath.Join(enc(old), "aaaa", "sub.jsonl")); err != nil {
		t.Fatal("子代理目录应挪回去")
	}
	if e.Record == nil || e.Record.Cwd != old || e.Record.TranscriptPath != a {
		t.Fatalf("条目里应带搬前的记录快照：%+v", e.Record)
	}
	if _, err := fav.RestoreTrash(fav.ProviderCodex, "c1c1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(c1); !strings.Contains(string(b), `"cwd":"/Users/me/work/webapp"`) {
		t.Fatal("Codex 原地改写的撤销应换回原件")
	}
}

// after undo the original is back at the same path with possibly identical size/mtime; RescanAfterRestore must force it
func TestRescanAfterRestore(t *testing.T) {
	posixOnly(t)
	_, codex := setup(t)
	t.Setenv("FAV_HOME", t.TempDir())
	old, new := "/Users/me/work/p", "/Users/me/work/q" // same length: size unchanged by the rewrite
	meta := `{"timestamp":"2026-09-11T02:00:00Z","type":"session_meta","payload":{"session_id":"c1c1","cwd":"%s","originator":"codex_cli_rs"}}` + "\n"
	msg := `{"timestamp":"2026-09-11T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"活"}]}}` + "\n"
	c1 := filepath.Join(codex, "sessions", "2026", "09", "11", "rollout-2026-09-11T02-00-00-c1c1.jsonl")
	write(t, c1, sprintf(meta, old)+msg)
	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	store, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	plan, _ := idx.PlanMove(store, nil, old, new)
	rep, err := plan.Apply(store)
	if err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Rescan(rep.Touched)
	if idx.files[c1].Cwd != new {
		t.Fatalf("搬后索引应是新目录：%q", idx.files[c1].Cwd)
	}
	e, err := fav.RestoreTrash(fav.ProviderCodex, "c1c1")
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := idx.Refresh(); plain.files[c1].Cwd != new {
		t.Fatal("（前提）普通 Refresh 认不出撤销：等长等 mtime 复用旧条目")
	}
	idx, _ = idx.Rescan(RescanAfterRestore(e))
	if idx.files[c1].Cwd != old {
		t.Fatalf("撤销后强制重扫应回到旧目录：%q", idx.files[c1].Cwd)
	}
}

func TestPlanMoveSeesWorktreeSettings(t *testing.T) {
	posixOnly(t)
	claude, _ := setup(t)
	t.Setenv("FAV_HOME", t.TempDir())
	write(t, filepath.Join(claude, ".claude.json"), `{"projects":{"/Users/me/work/p/wt/feat":{}}}`)
	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	store, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	plan, err := idx.PlanMove(store, nil, "/Users/me/work/p", "/Users/me/dev/p")
	if err != nil || !plan.Settings {
		t.Fatalf("只有子目录条目也算：%v %+v", err, plan)
	}
}

func TestMoveSweepsEmptyProjectDir(t *testing.T) {
	posixOnly(t)
	claude, _ := setup(t)
	t.Setenv("FAV_HOME", t.TempDir())
	old, new := "/Users/me/work/p", "/Users/me/dev/p"
	dir := filepath.Join(claude, "projects", claudeEnc.ReplaceAllString(old, "-"))
	write(t, filepath.Join(dir, "aaaa.jsonl"), strings.ReplaceAll(claudeLines("活", "继续"), "/Users/me/work/webapp", old))
	write(t, filepath.Join(dir, "memory", "MEMORY.md"), "# m\n")
	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	store, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	plan, err := idx.PlanMove(store, nil, old, new)
	if err != nil || len(plan.Sessions) != 1 {
		t.Fatalf("%v %+v", err, plan)
	}
	if _, err := plan.Apply(store); err != nil {
		t.Fatal(err)
	}
	ndir := filepath.Join(claude, "projects", claudeEnc.ReplaceAllString(new, "-"))
	if _, err := os.Stat(filepath.Join(ndir, "memory", "MEMORY.md")); err != nil {
		t.Fatal("腾空后 memory/ 应搬到新项目目录")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("旧项目目录应删掉")
	}
}

func TestMoveRefusesDestinationConflictAndGitRootOnlyRecord(t *testing.T) {
	posixOnly(t)
	claude, _ := setup(t)
	t.Setenv("FAV_HOME", t.TempDir())
	old, new := "/Users/me/work/p", "/Users/me/dev/p"
	enc := func(cwd string) string {
		return filepath.Join(claude, "projects", claudeEnc.ReplaceAllString(cwd, "-"))
	}
	write(t, filepath.Join(enc(old), "aaaa.jsonl"), strings.ReplaceAll(claudeLines("活", "继续"), "/Users/me/work/webapp", old))
	write(t, filepath.Join(enc(new), "aaaa.jsonl"), "{}\n") // target already has a file of that name
	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	store, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	store.Put(&fav.Rec{ID: "r", Provider: fav.ProviderClaude, SessionID: "zzzz", Title: "只有 git_root 在下面", Cwd: "/p", GitRoot: old})
	plan, err := idx.PlanMove(store, nil, old, new)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range plan.Sessions {
		if s.SessionID == "zzzz" {
			t.Fatal("cwd 不在 old 下的记录不该当会话搬")
		}
	}
	if _, err := plan.Apply(store); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("目标冲突应整个拒绝：%v", err)
	}
	if _, err := os.Stat(filepath.Join(enc(old), "aaaa.jsonl")); err != nil {
		t.Fatal("拒绝时原件不能动")
	}
	if r := store.Get("r"); r.GitRoot != old || r.Cwd != "/p" {
		t.Fatal("拒绝时记录也不能动")
	}
}

func TestRestoreRefusedAfterUse(t *testing.T) {
	posixOnly(t)
	claude, _ := setup(t)
	t.Setenv("FAV_HOME", t.TempDir())
	old, new := "/Users/me/work/p", "/Users/me/dev/p"
	enc := func(cwd string) string {
		return filepath.Join(claude, "projects", claudeEnc.ReplaceAllString(cwd, "-"))
	}
	write(t, filepath.Join(enc(old), "aaaa.jsonl"), strings.ReplaceAll(claudeLines("活", "继续"), "/Users/me/work/webapp", old))
	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	store, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	plan, _ := idx.PlanMove(store, nil, old, new)
	if _, err := plan.Apply(store); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(enc(new), "aaaa.jsonl")
	appendTo(t, moved, "{}\n") // used again after the move
	if _, err := fav.RestoreTrash(fav.ProviderClaude, "aaaa"); err == nil {
		t.Fatal("搬后又用过的会话不能被回收站里的原件盖掉")
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatal("拒绝还原时新文件要完好")
	}
}

func TestRestoreRetryAndPin(t *testing.T) {
	posixOnly(t)
	claude, codex := setup(t)
	t.Setenv("FAV_HOME", t.TempDir())
	old, new := "/Users/me/work/p", "/Users/me/dev/p"
	enc := func(cwd string) string {
		return filepath.Join(claude, "projects", claudeEnc.ReplaceAllString(cwd, "-"))
	}
	a := filepath.Join(enc(old), "aaaa.jsonl")
	write(t, a, strings.ReplaceAll(claudeLines("活", "继续"), "/Users/me/work/webapp", old))
	meta := `{"timestamp":"2026-09-11T02:00:00Z","type":"session_meta","payload":{"session_id":"c1c1","cwd":"%s","originator":"codex_cli_rs"}}` + "\n"
	msg := `{"timestamp":"2026-09-11T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"活"}]}}` + "\n"
	c1 := filepath.Join(codex, "sessions", "2026", "09", "11", "rollout-2026-09-11T02-00-00-c1c1.jsonl")
	c2 := filepath.Join(codex, "sessions", "2026", "09", "12", "rollout-2026-09-12T02-00-00-c1c1.jsonl")
	write(t, c1, sprintf(meta, old)+msg)
	write(t, c2, sprintf(meta, old)+msg)
	pin := filepath.Join(t.TempDir(), "pin.jsonl")
	os.Link(a, pin)
	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	store, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	store.Put(&fav.Rec{ID: "r1", Provider: fav.ProviderClaude, SessionID: "aaaa", Title: "钉住的", Cwd: old, TranscriptPath: a, PinnedPath: pin})
	plan, _ := idx.PlanMove(store, nil, old, new)
	if _, err := plan.Apply(store); err != nil {
		t.Fatal(err)
	}

	// two rollouts of one Codex session, only the second changed after the move: refuse entirely, first original untouched
	appendTo(t, c2, "{}\n")
	if _, err := fav.RestoreTrash(fav.ProviderCodex, "c1c1"); err == nil {
		t.Fatal("有文件搬后用过应拒绝")
	}
	entries, _ := fav.LoadTrash()
	var ce fav.TrashEntry
	for _, e := range entries {
		if e.SessionID == "c1c1" {
			ce = e
		}
	}
	for _, f := range ce.Files {
		if _, err := os.Stat(f.To); err != nil {
			t.Fatalf("拒绝时回收站里的原件都得在：%s", f.To)
		}
	}
	// detected even within a second of the move (fingerprint, not time)
	if _, err := fav.RestoreTrash(fav.ProviderCodex, "c1c1"); err == nil {
		t.Fatal("再来一次还是拒绝")
	}

	// undoing the Claude entry relinks the pin to the original
	e, err := fav.RestoreTrash(fav.ProviderClaude, "aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if e.Record.PinnedPath != pin {
		t.Fatal("pin 应保留")
	}
	sa, _ := os.Stat(a)
	sp, _ := os.Stat(pin)
	if !os.SameFile(sa, sp) {
		t.Fatal("撤销后 pin 应和还原的原件是同一个 inode")
	}
}

func TestRestoreIdempotentAfterPartialFailure(t *testing.T) {
	posixOnly(t)
	_, codex := setup(t)
	t.Setenv("FAV_HOME", t.TempDir())
	old, new := "/Users/me/work/p", "/Users/me/dev/p"
	meta := `{"timestamp":"2026-09-11T02:00:00Z","type":"session_meta","payload":{"session_id":"c1c1","cwd":"%s","originator":"codex_cli_rs"}}` + "\n"
	msg := `{"timestamp":"2026-09-11T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"活"}]}}` + "\n"
	c1 := filepath.Join(codex, "sessions", "2026", "09", "11", "rollout-2026-09-11T02-00-00-c1c1.jsonl")
	write(t, c1, sprintf(meta, old)+msg)
	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	store, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	plan, _ := idx.PlanMove(store, nil, old, new)
	if _, err := plan.Apply(store); err != nil {
		t.Fatal(err)
	}
	// simulate a half-finished restore: the original is back but the entry remains
	entries, _ := fav.LoadTrash()
	os.Rename(entries[0].Files[0].To, c1)
	if _, err := fav.RestoreTrash(fav.ProviderCodex, "c1c1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(c1); !strings.Contains(string(b), old) {
		t.Fatal("重来一次不能把已经放回去的原件删掉")
	}
}
