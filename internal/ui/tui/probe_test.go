package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

// transcriptOf writes a Claude transcript of n user messages ("第0句" oldest).
func transcriptOf(t *testing.T, n int) string {
	t.Helper()
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, `{"type":"user","timestamp":"2026-09-12T15:%02d:00Z","message":{"content":"第%d句"}}`+"\n", i%60, i)
	}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScrollingProbesOnlyWhereTheCursorRests(t *testing.T) {
	s, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	for i := range 60 {
		s.Put(&fav.Rec{ID: fav.NewID(), Provider: fav.ProviderClaude, SessionID: strconv.Itoa(i), Title: "t", Status: fav.StatusDone,
			Cwd: t.TempDir(), TranscriptPath: transcriptOf(t, 3), FavoritedAt: new(time.Now().Add(-time.Duration(i) * time.Hour))})
	}
	m := newModel(t, s, 160, 45)
	for range 50 {
		m.Update(press("down"))
		m.screen()
	}
	if len(m.probes) != 0 || !strings.Contains(m.screen(), strings.TrimSpace(i18n.T("detail.checking"))) {
		t.Fatalf("scrolling reads nothing: the detail shows the placeholder, probes=%d", len(m.probes))
	}
	settle(m)
	if !strings.Contains(m.screen(), i18n.F("resume.check.source", "")) {
		t.Error("the probe of the record the cursor rests on renders into the detail")
	}
}

// settle runs the commands Update queued synchronously (ticks fire, probes run) until none are left.
func settle(m *Model) {
	var cmd tea.Cmd
	_, cmd = m.Update(probeTickMsg(m.probeSeq))
	for i := 0; cmd != nil && i < 8; i++ {
		msg := cmd()
		if msg == nil {
			return
		}
		if b, ok := msg.(tea.BatchMsg); ok {
			cmd = nil
			for _, c := range b {
				if c == nil {
					continue
				}
				if pm, ok := c().(probeMsg); ok {
					_, cmd = m.Update(pm)
				}
			}
			continue
		}
		_, cmd = m.Update(msg)
	}
}

func TestChatScrollKeys(t *testing.T) {
	s, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	s.Put(&fav.Rec{ID: fav.NewID(), Provider: fav.ProviderClaude, SessionID: "x", Title: "t", Summary: "s", Project: "p",
		Status: fav.StatusDone, Cwd: t.TempDir(), TranscriptPath: transcriptOf(t, 100), FavoritedAt: new(time.Now())})
	m := newModel(t, s, 150, 44)
	settle(m)
	m.screen()
	if !m.chatFills(0, 0) {
		t.Fatal("100 messages overflow the pane")
	}
	m.Update(press("J"))
	m.screen()
	if m.chatCur != 1 {
		t.Errorf("J should move the chat cursor to the second message, got %d", m.chatCur)
	}
	for range 40 {
		m.Update(press("J"))
		m.screen()
	}
	if m.chatScroll == 0 || m.chatCur < m.chatScroll {
		t.Errorf("viewport should follow the cursor: scroll=%d cur=%d", m.chatScroll, m.chatCur)
	}
	for range 60 {
		m.Update(press("K"))
		m.screen()
	}
	if m.chatScroll != 0 || m.chatCur != 0 {
		t.Errorf("K should not scroll past the newest message: scroll=%d cur=%d", m.chatScroll, m.chatCur)
	}
}

func TestProbeWaitsForCursorToRest(t *testing.T) {
	m := sized(t, 150, 44)
	m.Update(press("down"))
	m.Update(press("down"))
	if len(m.probes) != 0 {
		t.Fatalf("moving the cursor must not start probes, got %d", len(m.probes))
	}
	m.Update(probeTickMsg(m.probeSeq - 1))
	if len(m.probes) != 0 {
		t.Fatalf("stale tick must not probe, got %d", len(m.probes))
	}
	m.Update(probeTickMsg(m.probeSeq))
	if len(m.probes) != 1 || m.probes[m.current()] == nil {
		t.Fatalf("the tick for the record the cursor rests on should probe exactly it")
	}
}

func TestChatSearch(t *testing.T) {
	m := sized(t, 140, 40)
	a, b := m.rows[1].rec, m.rows[2].rec
	m.probes = map[*fav.Rec]*probe{a: {done: true}, b: {done: true}}
	for i := range recentMsgs {
		m.probes[a].msgs = append(m.probes[a].msgs, capture.Message{Role: "assistant", Text: "第" + strconv.Itoa(i) + "句", Off: int64(1000 - i)})
	}
	m.probes[a].msgs[3].Text = "cursor 回退到 id 兜底"
	m.probes[a].msgs[9].Text = "再查一下 cursor 漂移"
	m.probes[b].msgs = []capture.Message{{Role: "user", Text: "别的会话"}}
	a.TranscriptPath = filepath.Join(t.TempDir(), "none.jsonl")

	m.Update(press(`\`))
	if !m.chat.typing {
		t.Fatal("\\ 应打开右栏搜索")
	}
	m.Update(press("cursor"))
	m.Update(press("enter"))
	if m.chat.typing || !m.msg.hl.loading {
		t.Fatal("Enter 应在左栏列出本会话的命中")
	}
	loadHits(t, m) // the messages are not in the text store: the loaded ones are searched
	if !m.hitsOpen() || len(m.msg.hl.items) != 2 || m.chatScroll != 3 {
		t.Fatalf("应定位到最近一处命中（第 4 句）：items=%d chatScroll=%d", len(m.msg.hl.items), m.chatScroll)
	}
	m.Update(press("n"))
	if m.chatScroll != 9 {
		t.Fatalf("n 应跳到下一处，chatScroll=%d", m.chatScroll)
	}
	if v := m.screen(); !strings.Contains(v, "第 2/2 处") {
		t.Fatal("标题里应显示命中计数")
	}
	m.Update(press("esc"))
	if m.chat.query() != "" || m.hitsOpen() {
		t.Fatal("Esc 应关掉命中列表并清掉查询")
	}
	for i := recentMsgs; i < 60; i++ {
		m.probes[a].msgs = append(m.probes[a].msgs, capture.Message{Role: "assistant", Text: "旧" + strconv.Itoa(i), Off: int64(1000 - i)})
	}
	m.probes[a].full, m.probes[a].loading = true, false
	m.cursor = 2 // b's page lands after the cursor moved to b
	m.applyPage(pageMsg{rec: b, page: capture.Page{Msgs: []capture.Message{{Text: "x"}}, Done: true}})
	if m.probes[a].full || len(m.probes[a].msgs) != recentMsgs || m.probes[a].from != 1000-(recentMsgs-1) || !m.probes[b].full || len(m.probes[b].msgs) != 2 {
		t.Fatalf("b 补页后 a 应退回尾 40 句且记住偏移：a=%d from=%d full=%v b=%d", len(m.probes[a].msgs), m.probes[a].from, m.probes[a].full, len(m.probes[b].msgs))
	}
}

func TestChatPagesBackward(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	r.TranscriptPath = transcriptOf(t, 100)
	m.probes = nil
	m.Update(m.probeCurrent()())
	p := m.probes[r]
	if len(p.msgs) != recentMsgs || p.full || p.from == 0 {
		t.Fatalf("先只读尾 40 句：%d full=%v from=%d", len(p.msgs), p.full, p.from)
	}
	m.Update(press("right"))
	for range recentMsgs - 8 {
		m.Update(press("j"))
	}
	m.screen()
	_, cmd := m.Update(press("j"))
	if !p.loading || cmd == nil {
		t.Fatal("快到末尾应排一次补页")
	}
	m.Update(runCmd(t, cmd))
	if len(p.msgs) != recentMsgs+olderMsgs || p.full || p.msgs[59].Text != "第40句" {
		t.Fatalf("补一页应多 20 句、接得上：%d full=%v last=%q", len(p.msgs), p.full, p.msgs[len(p.msgs)-1].Text)
	}
	for guard := 0; !p.full && guard < 10; guard++ {
		m.chatCur = len(p.msgs) - 1
		m.chatFollow = true
		m.screen()
		_, cmd = m.Update(press("k"))
		if cmd != nil {
			m.Update(runCmd(t, cmd))
		}
	}
	if !p.full || len(p.msgs) != 100 || p.msgs[99].Text != "第0句" {
		t.Fatalf("翻到头应是全文 100 句：%d full=%v", len(p.msgs), p.full)
	}
	if v := ansi.Strip(m.screen()); !strings.Contains(v, "全文 100 句") {
		t.Fatal("标题应写全文")
	}
}

func TestMessageOverlay(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	long := strings.Repeat("这是一段很长的回复，长到一个小框放不下。", 300)
	m.probes = map[*fav.Rec]*probe{r: {done: true, msgs: []capture.Message{{Role: "user", Text: "好的"}, {Role: "assistant", Text: long}}}}
	m.screen()
	m.Update(press("right"))
	if m.pane != paneChat {
		t.Fatal("→ 应把焦点给右栏")
	}
	m.Update(press("enter"))
	if m.ov.kind != ovMessage || m.ov.boxW != 140-8 || len(m.ov.lines) != 1 {
		t.Fatalf("短句也用固定大小的框：kind=%v w=%d lines=%d", m.ov.kind, m.ov.boxW, len(m.ov.lines))
	}
	if h := len(strings.Split(m.renderMessage(), "\n")); h != 40-4 {
		t.Fatalf("框高应固定为终端高 − 4，得到 %d", h)
	}
	m.Update(press("esc"))
	m.Update(press("j"))
	m.Update(press("enter"))
	if m.ov.kind != ovMessage || len(m.ov.lines) < 15 {
		t.Fatalf("长文应折成多行：lines=%d", len(m.ov.lines))
	}
	v := m.screen()
	if !strings.Contains(v, "/ "+strconv.Itoa(len(m.ov.lines))+" 行") {
		t.Fatal("长文标题应带行数")
	}
	m.Update(press("pgdown"))
	if m.ov.cursor == 0 {
		t.Fatal("PgDn 应往下翻")
	}
	m.Update(press("left"))
	if m.chatCur != 0 || m.ov.kind != ovMessage || len(m.ov.lines) != 1 {
		t.Fatalf("← 应换到上一句：cur=%d lines=%d", m.chatCur, len(m.ov.lines))
	}
	m.Update(press("left"))
	if m.chatCur != 0 {
		t.Fatal("最新一句再往前不该动")
	}
	m.Update(press("esc"))
	m.pane = paneList
	m.clickChat(0)
	if m.pane != paneChat || m.chatCur != 0 || m.ov.active() {
		t.Fatal("第一下点击只应选中")
	}
	m.clickChat(0)
	if m.ov.kind != ovMessage {
		t.Fatal("再点同一句应打开全文")
	}
}

func TestMessageOverlayMultilineStep(t *testing.T) {
	m := sized(t, 60, 30)
	r := m.current()
	msg := capture.Message{Role: "assistant", Text: "写个脚本", Steps: []capture.Step{
		{Tool: "Bash", Text: "cat <<'EOF'\n    indented line\nEOF"},
		{Result: true, Text: strings.Repeat("x", 80)}}}
	m.probes = map[*fav.Rec]*probe{r: {done: true, full: true, msgs: []capture.Message{msg}}}
	m.screen()
	m.pane = paneChat
	m.Update(press("enter"))
	v := m.screen()
	for _, want := range []string{"cat <<'EOF'", "    indented line", "EOF"} {
		if !strings.Contains(v, want) {
			t.Errorf("全文里缺 %q", want)
		}
	}
	if len(m.ov.lines) < 5 {
		t.Fatalf("80 个 x 在 52 列宽的框里应被切成两行：lines=%v", m.ov.lines)
	}
	for _, l := range m.ov.lines {
		if render.Width(l) > m.ov.boxW-4 {
			t.Fatalf("有行超宽：%q", l)
		}
	}
}

func TestMessageOverlayFullSteps(t *testing.T) {
	m := sized(t, 100, 30)
	r := m.current()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	var big strings.Builder
	for i := range 40 {
		big.WriteString("line " + strconv.Itoa(i) + "\\n")
	}
	line1 := `{"type":"user","timestamp":"2026-09-12T15:15:38Z","message":{"content":"看一下"}}` + "\n"
	line2 := `{"type":"assistant","timestamp":"2026-09-12T15:16:00Z","message":{"content":[{"type":"text","text":"看了"},{"type":"tool_use","name":"Bash","input":{"command":"curl x"}}]}}` + "\n"
	line3 := `{"type":"user","timestamp":"2026-09-12T15:17:00Z","message":{"content":[{"type":"tool_result","content":"{\"a\":1,\"b\":[1,2]}"}]}}` + "\n"
	line4 := `{"type":"assistant","timestamp":"2026-09-12T15:18:00Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"` + big.String() + `"}}]}}` + "\n"
	os.WriteFile(path, []byte(line1+line2+line3+line4), 0o644)
	r.TranscriptPath = path
	msgs := capture.Messages(path, -1, 1<<30).Msgs
	if !strings.Contains(msgs[0].Steps[2].Text, "还有") {
		t.Fatalf("内存里的步骤应是截过的：%q", msgs[0].Steps[2].Text)
	}
	m.probes = map[*fav.Rec]*probe{r: {done: true, full: true, msgs: msgs}}
	m.screen()
	m.pane = paneChat
	m.chatCur = 0
	m.Update(press("enter"))
	if m.ov.kind != ovMessage || len(m.ov.msg.Steps) != 3 {
		t.Fatalf("应打开带三步的全文：%+v", m.ov.msg)
	}
	if v := ansi.Strip(m.screen()); !strings.Contains(v, "看了") || !strings.Contains(v, "Bash  curl x") || !strings.Contains(v, "→ {") {
		t.Errorf("the text, then each tool call and its result:\n%s", v)
	}
	joined := strings.Join(m.ov.lines, "\n")
	if !strings.Contains(joined, `"a": 1`) || !strings.Contains(joined, "line 39") || strings.Contains(joined, "还有") {
		t.Fatalf("全文应完整、JSON 格式化、不截行：%s", joined)
	}
	if !strings.Contains(string(m.ov.kinds), "S") {
		t.Fatal("40 行的一步后面应有分隔线")
	}
	v := m.screen()
	if !strings.Contains(v, "┃") {
		t.Fatal("超出一屏应画滚动条")
	}
}

func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			if pm, ok := runCmd(t, c).(pageMsg); ok {
				return pm
			}
		}
		return nil
	}
	return msg
}
