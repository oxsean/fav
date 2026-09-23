package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

func TestLiveChatRefreshHoldsWhileReading(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local)
	old := []capture.Message{{Role: "user", Text: "第三句", At: at.Add(2 * time.Minute)}, {Role: "assistant", Text: "第二句", At: at.Add(time.Minute)}, {Role: "user", Text: "第一句", At: at}}
	m.probes = map[*fav.Rec]*probe{r: {done: true, msgs: old}}
	m.live = map[string]capture.Live{r.SessionID: {TabID: "t1", Status: "working"}}
	m.Update(press("l"))
	m.Update(press("j"))
	fresh := append([]capture.Message{{Role: "assistant", Text: "第四句", At: at.Add(3 * time.Minute)}}, old...)
	m.Update(refreshMsg{r, capture.Page{Msgs: fresh}})
	p := m.probes[r]
	if len(p.msgs) != 3 || len(p.fresh) != 1 {
		t.Fatalf("焦点在右栏时新句子应先攒着：msgs=%d fresh=%d", len(p.msgs), len(p.fresh))
	}
	if !strings.Contains(ansi.Strip(m.screen()), "新 +1 句") {
		t.Fatal("标题里应提示攒了新句子")
	}
	m.Update(press("h"))
	if len(p.msgs) != 4 || p.fresh != nil || p.msgs[0].Text != "第四句" || m.chatCur != 2 {
		t.Fatalf("回左栏应并进去且高亮仍在第二句：msgs=%d cur=%d", len(p.msgs), m.chatCur)
	}
	m.Update(refreshMsg{r, capture.Page{Msgs: []capture.Message{{Role: "user", Text: "全新", At: at.Add(time.Hour)}}}})
	if len(p.msgs) != 1 || p.msgs[0].Text != "全新" {
		t.Fatalf("接不上时应整个换掉：%d", len(p.msgs))
	}
	m.Update(refreshMsg{r, capture.Page{Msgs: p.msgs}})
	if p.fresh != nil {
		t.Fatal("没新句子不该攒")
	}
}

func TestLiveView(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 140, 40)
	r := m.current()
	now := time.Now()
	m.Update(liveMsg{herdr: map[string]capture.Live{}})
	m.Update(liveMsg{herdr: map[string]capture.Live{
		r.SessionID: {TabID: "t1", Status: "working", Since: now},
		"brand-new": {TabID: "t2", Status: "blocked", Since: now, Agent: "claude", Title: "刚开的活", Cwd: "/tmp/x"},
	}})
	if !strings.Contains(m.notice, "等你") {
		t.Fatalf("变成 blocked 应提示：%q", m.notice)
	}
	m.screen()
	card := strings.Join(m.cardBox(r, true, m.listWidth()), "\n")
	if v := ansi.Strip(m.screen()); strings.Contains(ansi.Strip(card), "工作中") || !strings.Contains(v, "Agents 2") || !strings.Contains(v, "1 等你") || !strings.Contains(v, "1 工作中") {
		t.Fatal("收藏页卡片不该标实时状态（详情里标）；标签页应带计数；右上角应有状态汇总")
	}
	m.setView(viewLive)
	if m.countRecs() != 2 {
		t.Fatalf("Agents 页应列 2 条：%d", m.countRecs())
	}
	if m.rows[0].rec == nil || m.rows[0].rec.Title != "刚开的活" || m.rows[0].rec.Project != "x" || m.rows[1].rec != r {
		t.Fatalf("不分组、刚开的（没时间）排最上、凑出来的卡带标题目录：%+v", m.rows[:2])
	}
	m.notice = ""
	v := ansi.Strip(m.screen())
	if !strings.Contains(v, "工作中") || !strings.Contains(v, "Space 切过去") || !strings.Contains(v, "X 关掉 Herdr tab") || strings.Contains(v, "状态 ") {
		t.Fatalf("Agents 页应标状态、底栏提示切过去 / 关 tab、没有状态 chip\n%s", v)
	}
	synth := m.rows[0].rec
	m.Update(liveMsg{herdr: map[string]capture.Live{
		r.SessionID: {TabID: "t1", Status: "idle", Since: now.Add(-25 * time.Minute)},
		"brand-new": {TabID: "t2", Status: "working", Since: now, Agent: "claude", Title: "刚开的活", Cwd: "/tmp/x"},
	}})
	if m.rows[0].rec != synth || m.rows[1].rec != r {
		t.Fatal("凑出来的卡应跨刷新沿用同一个对象，状态变了位置不动")
	}
	m.Update(press("o"))
	if m.cfg.LiveSort != liveSortGroup || m.rows[0].group != "工作中" || m.rows[1].rec != synth || m.rows[2].group != "空闲" || m.rows[3].rec != r {
		t.Fatalf("o 应切到按状态分组：%+v", m.rows)
	}
	m.Update(press("o"))
	if m.cfg.LiveSort != liveSortActive || m.rows[0].rec != r || m.rows[1].rec != synth || !strings.Contains(ansi.Strip(m.screen()), "按最后活跃") {
		t.Fatalf("再按 o 应按最后活跃（有 LastAt 的在前，凑出来的没时间垫底）：%+v", m.rows)
	}
	m.Update(press("o"))
	if m.cfg.LiveSort != liveSortStarted || m.rows[0].rec != synth {
		t.Fatal("第三下 o 应回到按开始时间")
	}
	if v := ansi.Strip(m.screen()); !strings.Contains(v, "空闲 25 分钟") {
		t.Fatal("空闲应带时长")
	}
	m.Update(liveMsg{herdr: map[string]capture.Live{}})
	if m.countRecs() != 0 || !strings.Contains(ansi.Strip(m.screen()), "Agents 0") {
		t.Fatal("跑完关掉后Agents 页应空")
	}
}

// a live session whose transcript has no message yet must not crash the refresh
func TestStashEmptyPages(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	m.probes = map[*fav.Rec]*probe{r: {done: true}}
	m.stash(refreshMsg{rec: r})
	m.applyFresh()
	if p := m.probes[r]; len(p.msgs) != 0 || p.fresh != nil {
		t.Fatalf("空页不该留下东西：%+v", p)
	}
}

func TestPulseText(t *testing.T) {
	now := time.Now()
	p := capture.Pulse{Reply: "先看排序键", TurnAt: now.Add(-12 * time.Minute), Context: 104425, Window: 258400}
	got := pulseText(p, capture.Live{Status: "working"}, now)
	for _, want := range []string{"12", "40%", "先看排序键"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q misses %q", got, want)
		}
	}
	p.Window = 0
	if got := pulseText(p, capture.Live{Status: "idle"}, now); !strings.Contains(got, "104k") || strings.Contains(got, i18n.F("live.turn", "")) {
		t.Errorf("Claude shows tokens, an idle session no turn time: %q", got)
	}
	if tokens(1_234_567) != "1.2M" || tokens(950) != "950" {
		t.Error("token format")
	}
}

func TestCloseIdleTabs(t *testing.T) {
	m := sized(t, 140, 40)
	recs := m.store.All()
	old, fresh, unseen := recs[0], recs[1], recs[2]
	for _, r := range recs[:3] {
		r.LastAt = m.now.Add(-5 * time.Hour)
	}
	fresh.LastAt = m.now
	m.live = map[string]capture.Live{
		old.SessionID:    {TabID: "t-old", Status: "idle"},
		fresh.SessionID:  {TabID: "t-fresh", Status: "idle"},
		unseen.SessionID: {TabID: "t-unseen", Status: "idle"},
	}
	m.pulse = pulseMsg{unseen.SessionID: {Size: 10, Finished: true}}
	m.setView(viewLive)
	m.Update(press("Z"))
	if m.ov.kind != ovConfirm || m.ov.focus != 1 {
		t.Fatalf("Z asks first, focus on Cancel: kind=%d focus=%d", m.ov.kind, m.ov.focus)
	}
	body := strings.Join(m.ov.lines, "\n")
	if !strings.Contains(body, old.Title[:6]) || strings.Contains(body, fresh.Title[:6]) || strings.Contains(body, unseen.Title[:6]) {
		t.Fatalf("only tabs quiet for hours with nothing unseen:\n%s", body)
	}
}
