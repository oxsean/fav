package tui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/skin"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// every frame is exactly the terminal's size: each line w wide (overlays are composited by column), h lines
func TestFrameLinesFillWidth(t *testing.T) {
	st := demoStore(t)
	states := []string{"", "detail", "projects", "syntax", "picker", "help", "help-input", "resume", "resume-edit", "edit",
		"edit-summary", "settings", "settings-ide", "palette", "status", "delete", "start", "message", "handoff", "peek", "remote", "remote-resume",
		"projects-grouped", "project-file", "project-edit", "server", "server-down", "server-down-projects", "server-picker",
		"server-shared", "server-shared-refused", "server-old", "remote-put", "remote-old", "run-there", "make-task", "make-task-project", "make-task-busy",
		"handoff-host", "handoff-dirs", "handoff-pick-dir", "handoff-no-ssh", "handoff-old", "handoff-env"}
	for _, size := range []struct{ w, h int }{{140, 40}, {120, 34}, {80, 24}, {80, 18}, {56, 20}, {50, 16}} {
		for _, state := range states {
			m := newModel(t, st, size.w, size.h)
			openOverlay(m, state)
			if layout := state == "" || state == "detail" || state == "projects" || state == "projects-grouped" || state == "syntax" || state == "remote" || state == "remote-put" || state == "remote-old" || strings.HasPrefix(state, "server") && state != "server-picker"; layout == m.ov.active() {
				t.Fatalf("%q did not open (overlay %d)", state, m.ov.kind)
			}
			lines := strings.Split(m.screen(), "\n")
			if len(lines) != size.h {
				t.Errorf("%dx%d %q: %d lines", size.w, size.h, state, len(lines))
			}
			for i, line := range lines {
				if got := ansi.StringWidth(line); got != size.w {
					t.Errorf("%dx%d %q line %d is %d wide: %q", size.w, size.h, state, i+1, got, ansi.Strip(line))
				}
			}
		}
	}
}

// openOverlay puts m into a named state: an overlay, or a list layout ("detail", "projects", "syntax").
func openOverlay(m *Model, kind string) {
	switch kind {
	case "detail":
		m.detail = true
	case "projects":
		m.setView(viewProjects)
		m.foldAll(nil)
	case "projects-grouped":
		demoProject(m)
		m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return groupProject(r.group) != "" })
		m.toggleGroup()
	case "project-file":
		demoProject(m)
		m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return r.group != "" && groupProject(r.group) == "" })
		m.openProject()
	case "project-edit":
		demoProject(m)
		m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return groupProject(r.group) != "" })
		m.openProject()
	case "syntax":
		m.search.SetValue("> ")
		m.refresh()
	case "remote":
		withRemote(m)
	case "remote-resume":
		withRemote(m)
		m.askResume()
	case "remote-put", "remote-old":
		f := newFakeHost()
		if kind == "remote-old" {
			f.methods = []string{remote.MHello, remote.MList, remote.MLive, remote.MMessages}
		}
		m.useHosts(remote.NewHostsDial([]tend.Host{{Name: "mba"}}, i18n.ZH, func(tend.Host) (*remote.Client, error) { return remote.Pipe(f), nil }))
		m.setView(viewSessions)
		pump(m, m.fetchHost("mba"))
		m.search.SetValue("host:mba")
		m.refresh()
		m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return r.rec != nil && r.rec.SessionID == "r-a" })
		key(m, map[string]string{"remote-put": "x", "remote-old": "f"}[kind])
	case "server":
		withServer(m)
	case "server-down":
		withServer(m)
		m.serverLost(&wire.Error{Code: wire.CodeTimeout})
	case "server-down-projects":
		withServer(m)
		m.serverLost(&wire.Error{Code: wire.CodeTimeout})
		m.setView(viewProjects)
		m.foldAll(nil)
	case "server-picker":
		withServer(m)
		m.pickHost()
	case "server-shared", "server-shared-refused":
		withServer(m)
		m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return r.rec != nil && r.rec.Host == "bobs" })
		if kind == "server-shared-refused" {
			m.Update(press("f"))
		}
	case "server-old":
		withServer(m)
		m.setView(viewProjects)
		m.foldAll(nil)
		m.flash(i18n.T("remote.old_server"))
	case "memory-block", "memory", "memory-read", "memory-trashed":
		dumpMemory(m, kind)
	case "handoff-host", "handoff-dirs", "handoff-pick-dir", "handoff-no-ssh", "handoff-old", "handoff-env":
		openHandoffTo(m, kind)
	case "run-there":
		withServer(m)
		m.openRunThere(m.current())
	case "make-task", "make-task-project":
		r := makeTaskRow(m)
		if kind == "make-task-project" {
			p := &task.Project{ID: "p_notes", Name: "notes-api", Owner: coord.Owner.User, Members: map[string]string{"u_bob": task.RoleParticipant},
				Repos: []task.Repo{{Name: "notes-api", Dirs: map[string]string{coord.Local: r.Cwd}}}}
			m.setProjects(projects.Mine(map[string]*task.Project{p.ID: p}))
		}
		m.openMakeTask(r)
		m.ov.area.SetValue("把 14 条表驱动测试补成 20 条，覆盖生效时间窗跨零点的情况；跑一遍 go test ./internal/tags")
		if kind == "make-task-project" {
			m.ov.pick[1] = 1
			m.makeField(makeProject)
		}
	case "make-task-busy":
		r := makeTaskRow(m)
		m.live = map[string]capture.Live{r.SessionID: {Status: "working"}}
		m.askResume()
	case "picker":
		m.pickTags()
	case "help":
		m.ov = overlay{kind: ovHelp, focus: -1}
	case "help-syntax":
		m.ov = overlay{kind: ovHelp, focus: -1, page: 1}
	case "help-input":
		m.ov = overlay{kind: ovHelp, focus: -1, page: 2}
	case "resume":
		m.askResume()
	case "resume-edit":
		m.askResume()
		m.editTitle()
	case "palette":
		m.openPalette()
	case "settings":
		m.openSettings()
	case "settings-ide":
		m.openSettings()
		for i, s := range settingsTable() {
			if s.text != nil {
				m.cycleSetting(i, 0)
			}
		}
	case "edit":
		m.openEdit(m.current())
	case "edit-summary":
		m.openEdit(m.current())
		m.editKey(press("tab"))
		m.editKey(press("tab"))
	case "status":
		m.pickStatus()
	case "delete":
		m.askDelete(m.current())
	case "start":
		m.current().Cwd = os.TempDir()
		m.askStart(m.current())
	case "find":
		m.startChatSearch()
	case "message":
		long := capture.Message{Role: "assistant", Text: strings.Repeat("一段很长的回复，放不下一屏。", 200),
			Steps: []capture.Step{{Tool: "Bash", Text: "go test ./..."}, {Result: true, Text: "ok"}}}
		m.probes = map[*tend.Rec]*probe{m.current(): {done: true, msgs: []capture.Message{long}}}
		m.pane = paneChat
		m.openMessage()
	case "handoff":
		m.ov = overlay{kind: ovHandoff, rec: m.current(), title: "/tmp/handoff.md", focus: -1,
			lines: strings.Split(strings.Repeat("# 交接\n继续做分页游标的修复，先跑测试。\n", 40), "\n")}
	case "peek":
		m.ov = overlay{kind: ovPeek, rec: m.current(), title: "p1", edit: newInput(), focus: -1,
			lines: strings.Split(strings.Repeat("Edit a.go?\n❯ 1. Yes\n", 60), "\n")}
	}
}

// makeTaskRow selects the demo session the make-task frames start from, with a coordinator to reach.
func makeTaskRow(m *Model) *tend.Rec {
	m.SetCoordinator(func(wire.Options) (*coord.Client, error) { return nil, &wire.Error{Code: wire.CodeOffline} })
	m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return r.rec != nil && r.rec.Title == "标签合并规则重写" })
	m.clampCursor()
	return m.current()
}

// demoProject puts the directory of the first session of the projects view into a team project, and the same
// directory on two other machines, then shows the projects view with every group folded.
func demoProject(m *Model) {
	m.setView(viewProjects)
	m.foldAll(new(true))
	var dir, name string
	for _, r := range m.groups {
		if len(r) > 0 && (dir == "" || r[0].Project < name) {
			dir, name = r[0].Cwd, r[0].Project
		}
	}
	p := &task.Project{ID: "p_demo", Name: name, Owner: "u_ann", Members: map[string]string{"u_bob": task.RoleParticipant, "u_cy": task.RoleParticipant},
		Repos: []task.Repo{{Name: name, Dirs: map[string]string{coord.Local: dir, "mba": "/home/u/dev/" + name, "win": `D:\dev\` + name}}}}
	m.setProjects(projects.Mine(map[string]*task.Project{p.ID: p}))
	m.foldAll(new(true))
}

func TestClickZones(t *testing.T) {
	m := newModel(t, demoStore(t), 120, 36)
	m.screen()

	x, y := findText(m.screen(), "项目")
	if x < 0 {
		t.Fatal("画面上找不到「项目」标签页")
	}
	click(m, x, y)
	if m.view != viewProjects {
		t.Errorf("点「项目」标签页后视图仍是 %v", m.view)
	}

	m.setView(viewSessions)
	m.screen()
	first := m.current()
	x, y = findText(m.screen(), "WebApp 分支栈")
	if x < 0 {
		t.Fatal("画面上找不到第二张卡片")
	}
	click(m, x, y)
	if m.current() == nil || m.current() == first {
		t.Errorf("点第二张卡片后选中项没变")
	}

	m.screen()
	x, y = findText(m.screen(), "标签 全部")
	click(m, x, y)
	if m.ov.kind != ovPicker {
		t.Errorf("点标签 chip 没有打开选择器，kind=%v", m.ov.kind)
	}
}

func click(m *Model, x, y int) {
	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
}

func findText(s, want string) (int, int) {
	for y, line := range strings.Split(s, "\n") {
		plain := ansi.Strip(line)
		if before, _, ok := strings.Cut(plain, want); ok {
			return ansi.StringWidth(before), y
		}
	}
	return -1, -1
}

// openHandoffTo opens the handoff dialog on this machine's first session in mode 2 (mba, win offline, nuc's tend too
// old, bobs shared), mba's answer applied as readHandoff returns it.
func openHandoffTo(m *Model, kind string) {
	withServer(m)
	m.setMachines([]remote.Machine{{Name: "mba", Mine: true}, {Name: "win", Mine: true}, {Name: "nuc", Mine: true}, {Name: "bobs"}})
	m.remote["win"].err = &wire.Error{Code: wire.CodeOffline}
	m.remote["nuc"].lacks = map[string]bool{remote.MHandoffPut: true}
	m.SetCoordinator(func(wire.Options) (*coord.Client, error) { return nil, &wire.Error{Code: wire.CodeOffline} })
	m.tasks.cl = &coord.Client{}
	m.proj.hello = &remote.Hello{Features: []string{remote.FeatureMigrate}}
	m.search.SetValue("")
	m.refresh()
	m.cursor = slices.IndexFunc(m.rows, func(r row) bool { return r.rec != nil && r.rec.Host == "" })
	r := m.current()
	here := remote.PeerOf("", remote.Hello{Hostname: "ann-mac", Endpoint: "ann-mac", OS: "darwin", Home: "/Users/ann"}, nil)
	mba := remote.PeerOf("mba", remote.Hello{Hostname: "mba", Endpoint: "mba", OS: "linux", Home: "/home/u"}, nil)
	x := &remote.Handover{From: here, To: mba, Facts: capture.HandoffFacts{Provider: r.Provider, SessionID: r.SessionID, Title: r.Title,
		Cwd: "/Users/ann/dev/notes-api", Summary: r.Summary, Requests: []string{"弱网下重连要有退避上限", "先补测试再改计时器"},
		Reply: "已定位：close 事件重置了 backoff 计时器。", Files: []string{"ws/reconnect.go", "ws/reconnect_test.go"},
		Git: capture.HandoffGit{Remote: "git@example.com:team/notes-api.git"}}}
	dirs := []remote.RepoDir{{Path: "/home/u/dev/notes-api", Branch: "main", From: "index"}}
	if kind == "handoff-dirs" || kind == "handoff-pick-dir" {
		dirs = append(dirs, remote.RepoDir{Path: "/home/u/work/notes-api", Branch: "fix/ws-reconnect", From: "claude"},
			remote.RepoDir{Path: "/srv/build/notes-api", From: "scan"})
	}
	m.ov = overlay{kind: ovHandoff, rec: r, focus: -1, handoff: &handoffTo{}}
	if kind == "handoff-host" || kind == "handoff-old" {
		path := filepath.Join(tend.Home(), "handoff-frame.md")
		os.WriteFile(path, []byte("# 交接："+r.Title+"\n\n"+r.Summary+"\n"), 0o600)
		m.Update(handoffReadMsg{path: path})
		m.ov.handoff.dir, m.ov.handoff.how = r.Cwd, "handoff.dir.same"
	} else {
		m.Update(handoffReadMsg{host: "mba", x: x, dirs: dirs})
	}
	m.screen()
	switch kind {
	case "handoff-host":
		m.pickHandoffHost()
	case "handoff-pick-dir":
		m.pickHandoffDir()
	case "handoff-old":
		m.flash(m.handoffWhy("nuc", remote.MHandoffPut))
	case "handoff-env":
		d := m.ov.handoff
		rep := envcheck.Report{Block: 1, Unequal: 2, Hint: 3}
		m.Update(handoffEnvMsg{seq: d.seq, dir: d.dir, rep: rep, env: &capture.HandoffEnv{Summary: rep.Summary(),
			Items: []string{envcheck.LevelText(envcheck.LevelBlock) + ": " + i18n.F("envcheck.cli_missing", "codex")}}})
		m.screen()
	}
}

// TEND_DUMP=120x34 go test ./internal/ui/tui -run TestDumpFrame -v prints a real frame.
func TestDumpFrame(t *testing.T) {
	spec := os.Getenv("TEND_DUMP")
	if spec == "" {
		t.Skip("设置 TEND_DUMP=WxH 查看画面")
	}
	var w, h int
	if _, err := fmt.Sscanf(spec, "%dx%d", &w, &h); err != nil {
		t.Fatalf("TEND_DUMP 应形如 120x34：%v", err)
	}
	m := newModel(t, demoStore(t), w, h)
	dumpT = t
	if os.Getenv("TEND_DUMP_VIEW") == "real" {
		idx, _ := index.Open()
		m.Update(m.pollLive()())
		m.applyIndex(idx)
	}
	openOverlay(m, os.Getenv("TEND_DUMP_OV"))
	fmt.Println(m.screen())
}

func demoStore(t *testing.T) *tend.Store {
	t.Helper()
	st, err := tend.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct {
		title, summary, project, work string
		tags                          []string
		provider, branch, ws          string
	}{
		{"notes-api WebSocket 断线重连排障", "弱网下心跳超时后不重连，定位到 backoff 计时器被 close 事件重置。", "notes-api", "排障", []string{"notes-api", "debug", "websocket"}, tend.ProviderClaude, "fix/ws-reconnect", "notes-api"},
		{"WebApp 分支栈 rebase 自动化", "把手工 rebase 流程收敛成 make 目标，处理 worktree 隔离与冲突恢复。", "webapp", "重构", []string{"webapp", "git", "tooling"}, tend.ProviderCodex, "feat/stack-rebase", "webapp"},
		{"Tend Session Manager 实现", "去掉 SQLite 改 JSONL，FZF 与原生 TUI 双前端。", "tend", "实现", []string{"tend", "golang", "tui"}, tend.ProviderClaude, "main", "dev"},
		{"标签合并规则重写", "合并优先级与生效时间窗的边界条件梳理，补了 14 条表驱动测试。", "notes-api", "实现", []string{"notes-api", "tags"}, tend.ProviderClaude, "feat/geo-override", ""},
		{"Codex rollout 文件反查会话 id", "没有环境变量可用，只能按 mtime + cwd 从首行反查，多命中时报错不猜。", "tend", "调研", []string{"tend", "codex"}, tend.ProviderCodex, "main", ""},
	} {
		r := &tend.Rec{
			ID: tend.NewID(), Provider: d.provider, SessionID: d.title,
			Title: d.title, Summary: d.summary, Project: d.project, WorkType: d.work,
			Tags: d.tags, GitBranch: d.branch, HerdrWorkspace: d.ws,
			Cwd: "/Users/me/work/" + d.project, Status: tend.StatusDone,
		}
		r.Tags = tend.Normalize(r.Tags)
		r.FavoritedAt = new(time.Now().Add(-time.Duration(len(d.title)) * time.Hour))
		if err := st.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// Every skin draws in both themes: each frame keeps the terminal's width and its colours are that skin's.
func TestEverySkinDrawsInBothThemes(t *testing.T) {
	st := demoStore(t)
	defer func() { setTheme(true); useSkin(tend.Config{}) }()
	for _, p := range skin.Presets {
		for _, dark := range []bool{false, true} {
			for _, state := range []string{"", "settings", "help"} {
				m := newModel(t, st, 120, 34)
				setTheme(dark)
				m.cfg.Skin = p.Name
				useSkin(m.cfg)
				openOverlay(m, state)
				frame := m.screen()
				for i, line := range strings.Split(frame, "\n") {
					if got := ansi.StringWidth(line); got != 120 {
						t.Fatalf("%s dark=%v %q line %d is %d wide", p.Name, dark, state, i+1, got)
					}
				}
				pal := curSkin.Light
				if dark {
					pal = curSkin.Dark
				}
				var r, g, b int
				fmt.Sscanf(pal["accent"], "#%02x%02x%02x", &r, &g, &b)
				if !strings.Contains(frame, fmt.Sprintf("38;2;%d;%d;%d", r, g, b)) {
					t.Fatalf("%s dark=%v %q: its accent %s is not on screen", p.Name, dark, state, pal["accent"])
				}
			}
		}
	}
}

func TestOverlayBackdropIsDarkerThanFrame(t *testing.T) {
	setTheme(true)
	back := lum(cBackdrop)
	for name, c := range map[string]color.Color{"cMuted": cMuted, "cText": cText, "cAccent": cAccent} {
		if got := lum(c); back >= got {
			t.Errorf("底图色亮度 %d 不低于 %s 的 %d", back, name, got)
		}
	}
	if got := lum(cFrame); back > got+24 {
		t.Errorf("底图色亮度 %d 明显高于框线色 %d，开浮层时边框会变亮", back, got)
	}
}

func lum(c color.Color) int {
	r, g, b, _ := c.RGBA()
	return (int(r>>8)*299 + int(g>>8)*587 + int(b>>8)*114) / 1000
}

func TestResumeDialogEditsTitle(t *testing.T) {
	st := demoStore(t)
	m := newModel(t, st, 120, 36)
	m.askResume()
	rec := m.ov.rec
	old := rec.Title
	rec.HerdrWorkspace, rec.Cwd = "", t.TempDir()
	rec.TranscriptPath = filepath.Join(rec.Cwd, "s.jsonl")
	os.WriteFile(rec.TranscriptPath, []byte("{}\n"), 0o644)
	m.ov.plan, _ = capture.PlanResume(rec, nil, false)

	m.screen()
	x, y := findText(m.screen(), "n 改标题")
	if x < 0 {
		t.Fatal("恢复框里没有改标题的入口")
	}
	click(m, x, y)
	if !m.ov.editing {
		t.Fatal("点了标题行没有进入编辑")
	}

	m.ov.edit.SetValue("改过的标题")
	m.Update(press("enter"))
	if m.ov.editing {
		t.Fatal("Enter 没有结束编辑")
	}
	m.Update(press("enter"))

	if !m.quitting || m.Result().Resume == nil {
		t.Fatal("第二次 Enter 没有触发恢复")
	}
	if rec.Title != "改过的标题" {
		t.Errorf("记录标题还是 %q", rec.Title)
	}
	reloaded, err := tend.OpenAt(st.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reloaded.All() {
		if r.ID == rec.ID && r.Title != "改过的标题" {
			t.Errorf("盘上还是旧标题 %q（原 %q）", r.Title, old)
		}
	}
}

func TestSessionsViewFavorites(t *testing.T) {
	st := demoStore(t)
	m := newModel(t, st, 120, 36)
	rec := &tend.Rec{Provider: tend.ProviderClaude, SessionID: "sess-x", Title: "第一句话",
		Cwd: t.TempDir(), FavoritedAt: new(time.Now()), Status: tend.StatusDone}
	rec.Attach(5, 10, time.Now(), "第一句话\n再来一句\n")
	short := &tend.Rec{Provider: tend.ProviderClaude, SessionID: "sess-short", Title: "hi",
		Cwd: t.TempDir(), FavoritedAt: new(time.Now()), Status: tend.StatusDone}
	short.Attach(1, 2, time.Now(), "hi\n")
	m.unfav = []*tend.Rec{rec, short}
	m.setView(viewSessions)
	if m.current() != rec {
		t.Fatalf("最新的未收藏会话应排在最前：%+v", m.current())
	}
	base := len(st.Query(tend.Parse("")))
	if n := m.countRecs(); n != base+1 {
		t.Fatalf("1 轮的会话默认应藏起来：列了 %d 条", n)
	}
	m.search.SetValue("turns:1")
	m.refresh()
	if n := m.countRecs(); n != base+2 {
		t.Fatalf("turns:1 应把短会话也列出来：%d 条", n)
	}
	m.search.SetValue("再来一句")
	m.refresh()
	if m.countRecs() != 1 || m.current() != rec {
		t.Fatal("应能按索引里的提示语搜到未收藏会话")
	}
	m.search.SetValue("")
	codex := &tend.Rec{Provider: tend.ProviderCodex, SessionID: "sess-cx", Title: "codex 的会话", Cwd: t.TempDir(), FavoritedAt: new(time.Now().Add(-time.Hour)), Status: tend.StatusDone}
	codex.Attach(5, 10, time.Now().Add(-time.Hour), "")
	m.unfav = append(m.unfav, codex)
	m.setView(viewSessions)
	var seen []string
	for range 3 {
		m.cycleProvider()
		seen = append(seen, m.search.Value())
	}
	if !strings.Contains(strings.Join(seen, ","), "provider:"+tend.ProviderCodex) || seen[2] != "" {
		t.Fatalf("来源轮换应经过没收藏的 Codex 会话再回到全部：%v", seen)
	}
	m.setView(viewFavorites)
	for _, r := range m.rows {
		if r.rec == rec {
			t.Fatal("「收藏」视图不该出现未收藏的会话")
		}
	}
	m.setView(viewSessions)
	m.Update(press("f"))
	if rec.ID == "" || st.BySession(tend.ProviderClaude, "sess-x") == nil {
		t.Fatal("按 f 后没有落库")
	}
	for _, r := range m.unfav {
		if r == rec {
			t.Errorf("落库后仍留在未收藏那批里")
		}
	}
}

func TestArrowNavigation(t *testing.T) {
	m := newModel(t, demoStore(t), 120, 36)
	key := func(s string) {
		m.Update(press(s))
	}
	key("up")
	if m.chipFocus != -1 {
		t.Fatalf("列表顶上再往上应停住、不进 chip 行，chipFocus=%d", m.chipFocus)
	}
	m.Update(press(";"))
	if m.chipFocus != 0 {
		t.Fatalf("; 应进 chip 行，chipFocus=%d", m.chipFocus)
	}
	key("right")
	key("enter")
	if m.ov.kind != ovPicker || m.ov.title != "筛选标签" {
		t.Fatalf("chip 行第二个是标签，Enter 应打开标签选择器：kind=%v title=%q", m.ov.kind, m.ov.title)
	}
	m.screen()
	key("right")
	if m.ov.focus != 1 {
		t.Fatalf("第一下方向键应从主按钮出发，focus=%d", m.ov.focus)
	}
	key("right")
	key("enter")
	if m.ov.active() {
		t.Fatal("焦点在「取消」上按 Enter 应关闭选择器")
	}
	key("down")
	if m.chipFocus != -1 {
		t.Fatalf("chip 行往下应回列表，chipFocus=%d", m.chipFocus)
	}
}

func TestFavoriteToggle(t *testing.T) {
	st := demoStore(t)
	m := newModel(t, st, 120, 36)
	m.setView(viewSessions)
	first := m.current()
	if first == nil || !first.Favorite() {
		t.Fatal("demo 库的第一条应是收藏")
	}
	title, nFavorites := first.Title, m.nFavorites
	m.Update(press("f"))
	if first.Favorite() || m.current() != first || m.nFavorites != nFavorites-1 {
		t.Fatalf("f 应取消收藏、记录留在原地、收藏总数减一：favorite=%v cur==first=%v nFavorites=%d", first.Favorite(), m.current() == first, m.nFavorites)
	}
	if !strings.Contains(ansi.Strip(m.screen()), "未收藏 · f 收藏") {
		t.Fatal("取消后详情应提示可再收藏")
	}
	m.setView(viewFavorites)
	for _, r := range m.rows {
		if r.rec == first {
			t.Fatal("取消过收藏的不该出现在「收藏」页")
		}
	}
	m.setView(viewSessions)
	m.Update(press("f"))
	got := st.BySession(first.Provider, first.SessionID)
	if got == nil || !got.Favorite() || got.Title != title || got.ID != first.ID || m.nFavorites != nFavorites {
		t.Fatalf("再按 f 应原样恢复原记录：%+v", got)
	}
}

func TestStateOnPlainSession(t *testing.T) {
	st := demoStore(t)
	m := newModel(t, st, 120, 40)
	m.setView(viewSessions)
	sess := &tend.Rec{Provider: "claude", SessionID: "plain-1", Title: "没收藏的会话", Cwd: "/tmp", Status: tend.StatusDone}
	sess.Attach(9, 20, time.Now(), "")
	m.unfav = append(m.unfav, sess)
	m.recount()
	m.refresh()
	nAll := m.nAll
	for i, r := range m.rows {
		if r.rec == sess {
			m.cursor = i
		}
	}
	key := func(k string) { m.Update(press(k)) }
	key("a")
	got := st.BySession("claude", "plain-1")
	if got != sess || !got.Archived() || got.Favorite() || len(m.unfav) != 0 || m.nAll != nAll {
		t.Fatalf("a 应把普通会话入库并归档、不算收藏、总数不变：%+v unfav=%d nAll=%d", got, len(m.unfav), m.nAll)
	}
	if m.current() != sess {
		t.Fatal("刚归档的应还在光标下")
	}
	m.Update(press("down"))
	m.refresh()
	for _, r := range m.rows {
		if r.rec == sess {
			t.Fatal("光标离开后默认筛选里不该再有它")
		}
	}
	key("s")
	m.ov.filter.SetValue("archived")
	m.Update(press("enter"))
	if !strings.Contains(m.search.Value(), "status:archived") {
		t.Fatalf("s 选已归档：%q", m.search.Value())
	}
	found := false
	for i, r := range m.rows {
		if r.rec == sess {
			found, m.cursor = true, i
		}
	}
	if !found {
		t.Fatal("status:archived 下应看到它")
	}
	if v := ansi.Strip(m.screen()); !strings.Contains(v, "已归档") || !strings.Contains(v, "a 取消归档") {
		t.Fatal("卡片和底栏应标出已归档 / a 取消归档")
	}
	key("a")
	if sess.Archived() {
		t.Fatal("再按 a 应取消归档")
	}
	key("s")
	m.Update(press("backspace"))
	if m.search.Value() != "" {
		t.Fatalf("选择器里退格清空应去掉 status:：%q", m.search.Value())
	}
	key("a")
	m.setView(viewFavorites)
	for _, r := range m.rows {
		if r.rec == sess {
			t.Fatal("只标过状态的会话不该出现在「收藏」页")
		}
	}
}

func TestCursorFollowsRecordOnRefresh(t *testing.T) {
	m := newModel(t, demoStore(t), 120, 40)
	m.move(1)
	m.move(1)
	cur := m.current()
	cur.Attach(cur.Turns, cur.Msgs, time.Now().Add(time.Hour), "")
	m.refresh()
	if m.current() != cur || m.cursor != 1 {
		t.Fatalf("重排后光标应还在同一条上：cursor=%d same=%v", m.cursor, m.current() == cur)
	}
}
