package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/render"
)

func TestScrollReadsNothing(t *testing.T) {
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".claude", "projects")); err != nil {
		t.Skip("no Claude transcripts on this machine")
	}
	paths, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	s, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	for i, p := range paths {
		if i >= 230 {
			break
		}
		s.Put(&fav.Rec{ID: fav.NewID(), Provider: fav.ProviderClaude, SessionID: filepath.Base(p), Title: "t", Summary: "s",
			Project: "webapp", Status: fav.StatusDone, Cwd: filepath.Join(home, "work", "webapp"), GitBranch: "main",
			TranscriptPath: p, FavoritedAt: ptr(time.Now().Add(-time.Duration(i) * time.Hour))})
	}
	m := New(s, noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 45})
	m.View()
	t0 := time.Now()

	for i := 0; i < 50; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m.View()
	}
	per := time.Since(t0) / 50
	t.Logf("key+frame while scrolling 50 records: %s", per)
	if per > 3*time.Millisecond {
		t.Errorf("scrolling should not touch disk; %s per step", per)
	}
	if !strings.Contains(m.View(), "检查中") {
		t.Errorf("detail should show the placeholder before the probe lands")
	}
	settle(m)
	if !strings.Contains(m.View(), "已识别会话来源") {
		t.Errorf("probe result should render into the detail panel")
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
	home, _ := os.UserHomeDir()
	paths, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	if len(paths) == 0 {
		t.Skip("no Claude transcripts on this machine")
	}
	biggest, size := paths[0], int64(0)
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.Size() > size {
			biggest, size = p, st.Size()
		}
	}
	s, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	s.Put(&fav.Rec{ID: fav.NewID(), Provider: fav.ProviderClaude, SessionID: "x", Title: "t", Summary: "s", Project: "p",
		Status: fav.StatusDone, Cwd: home, TranscriptPath: biggest, FavoritedAt: ptr(time.Now())})
	m := New(s, noIndex(t), fav.DefaultConfig(), "")
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 44})
	settle(m)
	if !strings.Contains(m.View(), "第 1–") || !m.chatFills(0, 0) {
		t.Skip("transcript has no conversation to show, or it all fits on screen")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("J")})
	m.View()
	if m.chatCur != 1 {
		t.Errorf("J should move the chat cursor to the second message, got %d", m.chatCur)
	}
	for i := 0; i < 40; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("J")})
		m.View()
	}
	if m.chatScroll == 0 || m.chatCur < m.chatScroll {
		t.Errorf("viewport should follow the cursor: scroll=%d cur=%d", m.chatScroll, m.chatCur)
	}
	for i := 0; i < 60; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")})
	}
	if !strings.Contains(m.View(), "第 1–") || m.chatCur != 0 {
		t.Errorf("K should not scroll past the newest message")
	}
}

func TestProbeWaitsForCursorToRest(t *testing.T) {
	m := sized(t, 150, 44)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
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
	for i := 0; i < recentMsgs; i++ {
		m.probes[a].msgs = append(m.probes[a].msgs, capture.Message{Role: "assistant", Text: "第" + strconv.Itoa(i) + "句", Off: int64(1000 - i)})
	}
	m.probes[a].msgs[3].Text = "cursor 回退到 id 兜底"
	m.probes[a].msgs[9].Text = "再查一下 cursor 漂移"
	m.probes[b].msgs = []capture.Message{{Role: "user", Text: "别的会话"}}
	a.TranscriptPath = filepath.Join(t.TempDir(), "none.jsonl")

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`\`)})
	if !m.chat.typing {
		t.Fatal("\\ 应打开右栏搜索")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("cursor")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.chat.typing || !m.msg.hl.loading {
		t.Fatal("Enter 应在左栏列出本会话的命中")
	}
	loadHits(t, m) // the messages are not in the text store: the loaded ones are searched
	if !m.hitsOpen() || len(m.msg.hl.items) != 2 || m.chatScroll != 3 {
		t.Fatalf("应定位到最近一处命中（第 4 句）：items=%d chatScroll=%d", len(m.msg.hl.items), m.chatScroll)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.chatScroll != 9 {
		t.Fatalf("n 应跳到下一处，chatScroll=%d", m.chatScroll)
	}
	if v := m.View(); !strings.Contains(v, "第 2/2 处") {
		t.Fatal("标题里应显示命中计数")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
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
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, `{"type":"user","timestamp":"2026-09-12T15:%02d:00Z","message":{"content":"第%d句"}}`+"\n", i%60, i)
	}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(path, []byte(b.String()), 0o644)
	m := sized(t, 140, 40)
	r := m.current()
	r.TranscriptPath = path
	m.probes = nil
	m.Update(m.probeCurrent()())
	p := m.probes[r]
	if len(p.msgs) != recentMsgs || p.full || p.from == 0 {
		t.Fatalf("先只读尾 40 句：%d full=%v from=%d", len(p.msgs), p.full, p.from)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	for i := 0; i < recentMsgs-8; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	m.View()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
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
		m.View()
		_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
		if cmd != nil {
			m.Update(runCmd(t, cmd))
		}
	}
	if !p.full || len(p.msgs) != 100 || p.msgs[99].Text != "第0句" {
		t.Fatalf("翻到头应是全文 100 句：%d full=%v", len(p.msgs), p.full)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "全文 100 句") {
		t.Fatal("标题应写全文")
	}
}

func TestMessageOverlay(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	long := strings.Repeat("这是一段很长的回复，长到一个小框放不下。", 300)
	m.probes = map[*fav.Rec]*probe{r: {done: true, msgs: []capture.Message{{Role: "user", Text: "好的"}, {Role: "assistant", Text: long}}}}
	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.pane != paneChat {
		t.Fatal("→ 应把焦点给右栏")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.kind != ovMessage || m.ov.boxW != 140-8 || len(m.ov.lines) != 1 {
		t.Fatalf("短句也用固定大小的框：kind=%v w=%d lines=%d", m.ov.kind, m.ov.boxW, len(m.ov.lines))
	}
	if h := len(strings.Split(m.renderMessage(), "\n")); h != 40-4 {
		t.Fatalf("框高应固定为终端高 − 4，得到 %d", h)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.kind != ovMessage || len(m.ov.lines) < 15 {
		t.Fatalf("长文应折成多行：lines=%d", len(m.ov.lines))
	}
	v := m.View()
	if !strings.Contains(v, "/ "+strconv.Itoa(len(m.ov.lines))+" 行") {
		t.Fatal("长文标题应带行数")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.ov.cursor == 0 {
		t.Fatal("PgDn 应往下翻")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.chatCur != 0 || m.ov.kind != ovMessage || len(m.ov.lines) != 1 {
		t.Fatalf("← 应换到上一句：cur=%d lines=%d", m.chatCur, len(m.ov.lines))
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.chatCur != 0 {
		t.Fatal("最新一句再往前不该动")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
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

func TestMessageOverlaySteps(t *testing.T) {
	m := sized(t, 140, 40)
	r := m.current()
	msg := capture.Message{Role: "assistant", Text: "我来跑测试", Steps: []capture.Step{
		{Tool: "Bash", Text: "go test ./..."}, {Result: true, Text: "ok fav 0.1s"}}}
	m.probes = map[*fav.Rec]*probe{r: {done: true, full: true, msgs: []capture.Message{msg}}}
	m.View()
	m.pane = paneChat
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	v := m.View()
	for _, want := range []string{"我来跑测试", "Bash  go test ./...", "→ ok fav 0.1s"} {
		if !strings.Contains(v, want) {
			t.Errorf("全文里缺 %q", want)
		}
	}
}

func TestMessageOverlayMultilineStep(t *testing.T) {
	m := sized(t, 60, 30)
	r := m.current()
	msg := capture.Message{Role: "assistant", Text: "写个脚本", Steps: []capture.Step{
		{Tool: "Bash", Text: "cat <<'EOF'\n    indented line\nEOF"},
		{Result: true, Text: strings.Repeat("x", 80)}}}
	m.probes = map[*fav.Rec]*probe{r: {done: true, full: true, msgs: []capture.Message{msg}}}
	m.View()
	m.pane = paneChat
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	v := m.View()
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
	for i := 0; i < 40; i++ {
		big.WriteString("line " + strconv.Itoa(i) + "\\n")
	}
	line1 := `{"type":"user","timestamp":"2026-09-12T15:15:38Z","message":{"content":"看一下"}}` + "\n"
	line2 := `{"type":"assistant","timestamp":"2026-09-12T15:16:00Z","message":{"content":[{"type":"text","text":"看了"},{"type":"tool_use","name":"Bash","input":{"command":"curl x"}}]}}` + "\n"
	line3 := `{"type":"user","timestamp":"2026-09-12T15:17:00Z","message":{"content":[{"type":"tool_result","content":"{\"a\":1,\"b\":[1,2]}"}]}}` + "\n"
	line4 := `{"type":"assistant","timestamp":"2026-09-12T15:18:00Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"` + big.String() + `"}}]}}` + "\n"
	os.WriteFile(path, []byte(line1+line2+line3+line4), 0o644)
	r.TranscriptPath = path
	msgs := capture.AllMessages(path)
	if !strings.Contains(msgs[0].Steps[2].Text, "还有") {
		t.Fatalf("内存里的步骤应是截过的：%q", msgs[0].Steps[2].Text)
	}
	m.probes = map[*fav.Rec]*probe{r: {done: true, full: true, msgs: msgs}}
	m.View()
	m.pane = paneChat
	m.chatCur = 0
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.kind != ovMessage || len(m.ov.msg.Steps) != 3 {
		t.Fatalf("应打开带三步的全文：%+v", m.ov.msg)
	}
	joined := strings.Join(m.ov.lines, "\n")
	if !strings.Contains(joined, `"a": 1`) || !strings.Contains(joined, "line 39") || strings.Contains(joined, "还有") {
		t.Fatalf("全文应完整、JSON 格式化、不截行：%s", joined)
	}
	if !strings.Contains(string(m.ov.kinds), "S") {
		t.Fatal("40 行的一步后面应有分隔线")
	}
	v := m.View()
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
