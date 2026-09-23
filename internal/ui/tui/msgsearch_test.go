package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
)

func line(role, text string) string {
	return `{"type":"` + role + `","timestamp":"2026-09-22T10:00:00Z","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
}

// msgModel: three favorited sessions with transcripts, the text store built, message search typed in.
func msgModel(t *testing.T, query string) (*Model, []*fav.Rec) {
	t.Helper()
	t.Setenv("FAV_HOME", t.TempDir())
	src := t.TempDir()
	s, _ := fav.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	bodies := map[string][]string{
		"wheel":  {line("user", "滚轮太慢了"), line("assistant", "加上滚动加速，一帧最多滚十几步")},
		"cursor": {line("user", "分页游标漂移怎么查"), line("assistant", "先看排序键")},
		"both":   {line("user", "滚轮加速和分页都要改")},
	}
	var recs []*fav.Rec
	var paths []string
	for _, name := range []string{"wheel", "cursor", "both"} {
		p := filepath.Join(src, name+".jsonl")
		os.WriteFile(p, []byte(strings.Join(bodies[name], "")), 0o644)
		r := &fav.Rec{ID: fav.NewID(), Provider: fav.ProviderClaude, SessionID: name, Title: name + " session", Status: fav.StatusDone,
			Cwd: src, TranscriptPath: p, FavoritedAt: ptr(time.Now())}
		s.Put(r)
		recs = append(recs, r)
		paths = append(paths, p)
	}
	if _, err := fulltext.Update(context.Background(), fulltext.Dir(), paths, nil); err != nil {
		t.Fatal(err)
	}
	m := New(s, noIndex(t), fav.DefaultConfig(), query)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	return m, recs
}

// search runs the debounced message search synchronously.
func search(t *testing.T, m *Model) {
	t.Helper()
	m.issueMsgSearch()
	cmd := m.runMsgSearch(m.msg.seq)
	if cmd == nil {
		t.Fatal("no search started")
	}
	res, ok := cmd().(msgResultMsg)
	if !ok {
		t.Fatal("search returned no result")
	}
	m.Update(res)
}

func TestMessageSearchRanksSessionsAndShowsHits(t *testing.T) {
	m, recs := msgModel(t, "> 滚轮 加速")
	search(t, m)
	if len(m.rows) != 2 || m.rows[0].rec != recs[2] || m.rows[1].rec != recs[0] {
		var got []string
		for _, r := range m.rows {
			got = append(got, r.rec.Title)
		}
		t.Fatalf("both sessions holding the keywords, the one with a message holding both first: %q", got)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "2 sessions") && !strings.Contains(v, "2 个会话") {
		t.Fatalf("title counts the sessions:\n%s", v)
	}
	if !strings.Contains(v, "滚轮加速和分页都要改") {
		t.Fatalf("the card shows the hit:\n%s", v)
	}
	for _, l := range strings.Split(m.View(), "\n") {
		if w := ansi.StringWidth(l); w > 140 {
			t.Fatalf("line wider than the terminal (%d): %q", w, ansi.Strip(l))
		}
	}
}

func TestMessageSearchKeepsFiltersAndClearsWithThePrefix(t *testing.T) {
	m, _ := msgModel(t, "> 分页 #nothing-tagged-so")
	search(t, m)
	if len(m.rows) != 0 {
		t.Fatalf("a tag filter nobody has leaves nothing to search: %d rows", len(m.rows))
	}
	m.search.SetValue("滚轮")
	m.refresh()
	m.issueMsgSearch()
	if m.msgMode() || m.msg.res != nil {
		t.Fatal("without the prefix the box is a normal list query again")
	}
}

func TestFindQueryDrivesHitsInTheRightPane(t *testing.T) {
	m, recs := msgModel(t, "> 滚轮 加速")
	search(t, m)
	m.cursor = 1 // the "wheel" session: keywords spread over two messages
	if m.current() != recs[0] {
		t.Fatalf("cursor on %v", m.current())
	}
	m.probes = map[*fav.Rec]*probe{recs[0]: {done: true, full: true, msgs: []capture.Message{
		{Role: "assistant", Text: "加上滚动加速，一帧最多滚十几步"},
		{Role: "user", Text: "滚轮太慢了"},
		{Role: "user", Text: "无关的一句"},
	}}}
	if hs := m.hits(); len(hs) != 2 || hs[0] != 0 || hs[1] != 1 {
		t.Fatalf("every message holding any keyword is a hit: %v", hs)
	}
}

func TestMessageSearchSurvivesAStoreReload(t *testing.T) {
	m, recs := msgModel(t, "> 滚轮 加速")
	search(t, m)
	other, _ := fav.OpenAt(m.store.Path)
	r := *recs[1]
	r.Title = "edited elsewhere"
	other.Put(&r)
	m.syncStore()
	if len(m.rows) != 2 {
		t.Fatalf("rebuilt records keep their hits: %d rows", len(m.rows))
	}
	if m.rows[0].rec == recs[2] {
		t.Fatal("the store was not reloaded")
	}
}

func TestMessageSearchPagesBackOnlyToTheFirstHit(t *testing.T) {
	m, recs := msgModel(t, "> 游标")
	var body []string
	for i := 0; i < 300; i++ {
		body = append(body, line("user", "older filler"))
	}
	body = append(body, line("user", "游标在这里"))
	for i := 0; i < 150; i++ {
		body = append(body, line("assistant", "newer filler"))
	}
	r := recs[1]
	os.WriteFile(r.TranscriptPath, []byte(strings.Join(body, "")), 0o644)
	fulltext.Update(context.Background(), fulltext.Dir(), []string{recs[0].TranscriptPath, r.TranscriptPath, recs[2].TranscriptPath}, nil)
	search(t, m)
	if m.current() != r {
		t.Fatalf("cursor on %v", m.current())
	}
	page := capture.Messages(r.TranscriptPath, -1, recentMsgs)
	p := &probe{done: true, msgs: page.Msgs, from: page.From, full: page.Done}
	m.probes = map[*fav.Rec]*probe{r: p}
	cmd := m.findHit(0)
	for cmd != nil {
		m.applyPage(cmd().(pageMsg))
		cmd = m.findHit(m.msg.seekHit)
	}
	if p.full {
		t.Fatal("the transcript was read whole to find a hit near its end")
	}
	if hs := m.hits(); len(hs) != 1 || m.chatCur != hs[0] || !strings.Contains(p.msgs[hs[0]].Text, "游标") {
		t.Fatalf("the pane lands on the hit: hits %v cur %d", hs, m.chatCur)
	}
}

func TestChangingKeywordsReseeksTheSameRecord(t *testing.T) {
	m, recs := msgModel(t, "> 分页")
	search(t, m)
	r := recs[1]
	if m.current() != r {
		t.Fatalf("cursor on %v", m.current())
	}
	m.probes = map[*fav.Rec]*probe{r: {done: true, full: true, msgs: []capture.Message{
		{Role: "assistant", Text: "先看排序键"},
		{Role: "user", Text: "分页游标漂移怎么查"},
	}}}
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m.findHit(0)
	if m.probeWant != r || m.chatCur != 1 {
		t.Fatalf("setup: on the 分页 hit, cur %d", m.chatCur)
	}
	m.search.SetValue("> 排序键")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	search(t, m)
	if m.chatCur != 0 {
		t.Fatalf("the pane follows the new keywords to their hit: cur %d", m.chatCur)
	}
}

func TestALatePageDoesNotTrimTheCurrentRecord(t *testing.T) {
	m, recs := msgModel(t, "> 分页")
	search(t, m)
	cur, other := recs[1], recs[0]
	long := make([]capture.Message, 100)
	for i := range long {
		long[i] = capture.Message{Role: "user", Text: "x", Off: int64(1000 - i)}
	}
	m.probes = map[*fav.Rec]*probe{cur: {done: true, msgs: long, from: 1}, other: {done: true, loading: true}}
	m.applyPage(pageMsg{other, capture.Page{Msgs: []capture.Message{{Role: "user", Text: "late"}}}})
	if len(m.probes[cur].msgs) != 100 {
		t.Fatalf("the record on screen keeps its loaded pages: %d", len(m.probes[cur].msgs))
	}
}

func TestHitListWalksEveryHitAndTheRightPaneFollows(t *testing.T) {
	m, recs := msgModel(t, "> 滚轮 加速")
	search(t, m)
	m.cursor = 1
	r := recs[0] // "wheel": 滚轮 in the question, 加速 in the answer
	page := capture.Messages(r.TranscriptPath, -1, recentMsgs)
	m.probes = map[*fav.Rec]*probe{r: {done: true, msgs: page.Msgs, from: page.From, full: page.Done}}
	m.typing = false
	m.search.Blur()
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	loadHits(t, m)
	if !m.hitsOpen() || len(m.msg.hl.items) != 2 {
		t.Fatalf("→ lists the session's hits: open=%v items=%d", m.hitsOpen(), len(m.msg.hl.items))
	}
	first := m.chatCur
	if page.Msgs[first].Off != m.msg.hl.items[m.msg.hl.cur].Off {
		t.Fatal("the right pane stands on the selected hit")
	}
	key, other := tea.KeyDown, 1
	if m.msg.hl.cur == 1 {
		key, other = tea.KeyUp, 0
	}
	m.Update(tea.KeyMsg{Type: key})
	if m.chatCur == first || page.Msgs[m.chatCur].Off != m.msg.hl.items[other].Off {
		t.Fatalf("↓ moves the right pane to the next hit: %d → %d", first, m.chatCur)
	}
	v := m.View()
	if !strings.Contains(ansi.Strip(v), "滚轮太慢了") {
		t.Fatalf("the list shows the hits:\n%s", ansi.Strip(v))
	}
	for _, l := range strings.Split(v, "\n") {
		if w := ansi.StringWidth(l); w > 140 {
			t.Fatalf("line wider than the terminal (%d): %q", w, ansi.Strip(l))
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.kind != ovMessage {
		t.Fatal("Enter opens the full text of the hit")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.hitsOpen() {
		t.Fatal("← goes back to the sessions")
	}
}

func TestALongMessageShowsTheLinesAroundTheHit(t *testing.T) {
	m, _ := msgModel(t, "> 游标")
	text := strings.Repeat("无关的一大段说明。", 200) + "这里提到游标漂移。" + strings.Repeat("后面还有很多。", 50)
	block := ansi.Strip(strings.Join(m.chatBlock(capture.Message{Role: "assistant", Text: text}, 60, "游标", false), "\n"))
	if !strings.Contains(block, "游标") || !strings.Contains(block, "…") {
		t.Fatalf("the preview jumps to the hit inside the message:\n%s", block)
	}
}

func TestHitListSurvivesAStoreReload(t *testing.T) {
	m, recs := msgModel(t, "> 滚轮 加速")
	search(t, m)
	m.cursor = 1
	m.typing = false
	m.search.Blur()
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	loadHits(t, m)
	if !m.hitsOpen() {
		t.Fatal("→ opens the hit list")
	}
	other, _ := fav.OpenAt(m.store.Path)
	r := *recs[2]
	r.Title = "edited elsewhere"
	other.Put(&r)
	m.syncStore()
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if !m.hitsOpen() || m.msg.hl.rec == recs[0] {
		t.Fatalf("the list stays open on the rebuilt record: open=%v", m.hitsOpen())
	}
}

// loadHits finishes the hit list → started, synchronously.
func loadHits(t *testing.T, m *Model) {
	t.Helper()
	if !m.msg.hl.loading {
		t.Fatal("→ did not start the hit list")
	}
	m.Update(m.openHits(m.findQuery())())
}

func TestABusyStoreIsRetriedAndRerunsTheSearch(t *testing.T) {
	m, _ := msgModel(t, "> 滚轮")
	gen := m.msg.textGen
	if cmd := m.applyTextDone(textDoneMsg{err: fulltext.ErrBusy}); cmd == nil {
		t.Fatal("another fav building the store: check back later")
	}
	m.applyTextDone(textDoneMsg{})
	if m.msg.textGen == gen {
		t.Fatal("once the other build is done, the search reruns on its text")
	}
}

func TestLandingWaitsForPendingResultsEvenWithTheSameKeywords(t *testing.T) {
	m, _ := msgModel(t, "> 滚轮 加速")
	search(t, m)
	m.msg.textGen++ // the store grew: a rerun is pending
	m.issueMsgSearch()
	m.landHit()
	if m.msg.landQ != "" {
		t.Fatal("a landing deferred to the pending results must happen when they arrive")
	}
}

func TestOTogglesNewestHitFirst(t *testing.T) {
	m, recs := msgModel(t, "> 滚轮 加速")
	search(t, m)
	if m.rows[0].rec != recs[2] {
		t.Fatalf("relevance first: %v", m.rows[0].rec.Title)
	}
	x := m.msg.res[msgKey(recs[0])]
	x.Latest = time.Now()
	m.msg.res[msgKey(recs[0])] = x
	m.typing = false
	m.search.Blur()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if m.rows[0].rec != recs[0] || !strings.Contains(ansi.Strip(m.View()), "latest hit") && !strings.Contains(ansi.Strip(m.View()), "最近命中") {
		t.Fatalf("o puts the newest hit first and says so: %v", m.rows[0].rec.Title)
	}
}

func TestANewMessageSearchStartsAtTheTop(t *testing.T) {
	m, _ := msgModel(t, "> 滚轮 加速")
	search(t, m)
	m.cursor = 1
	m.msg.textGen++ // the store grew: same query, rerun
	search(t, m)
	if m.cursor != 1 {
		t.Fatalf("a rerun of the same query keeps the cursor: %d", m.cursor)
	}
	m.search.SetValue("> 滚轮")
	m.refresh()
	search(t, m)
	last := m.rows[len(m.rows)-1].rec // a session that ranks last for the next query
	m.search.SetValue("> 滚轮 加速")
	m.refresh()
	search(t, m)
	for i, r := range m.rows {
		if r.rec == last {
			m.cursor = i
		}
	}
	m.search.SetValue("> 滚轮")
	m.refresh()
	search(t, m)
	if len(m.rows) < 2 {
		t.Fatalf("the test needs two results: %d", len(m.rows))
	}
	if m.cursor != 0 || m.scroll != 0 {
		t.Fatalf("new keywords put the cursor on the first result: cursor %d scroll %d", m.cursor, m.scroll)
	}
}

func TestBackslashSearchesThisSessionStartingFromTheKeywords(t *testing.T) {
	m, recs := msgModel(t, "> 滚轮 加速")
	search(t, m)
	m.cursor = 1
	m.typing = false
	m.search.Blur()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`\`)})
	if !m.chat.typing || m.chat.input.Value() != "滚轮 加速" {
		t.Fatalf("\\ starts from the > keywords: %q", m.chat.input.Value())
	}
	m.chat.input.SetValue("加速")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	loadHits(t, m)
	if !m.hitsOpen() || len(m.msg.hl.items) != 1 || m.msg.hl.rec != recs[0] {
		t.Fatalf("the session's own hits of the new query: open=%v items=%d", m.hitsOpen(), len(m.msg.hl.items))
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Messages") && !strings.Contains(v, "搜消息") {
		t.Fatalf("the search box says it is in message search:\n%s", v)
	}
}

func TestImeSlashSearchesSessionsAndCtrlSThisSession(t *testing.T) {
	m, _ := msgModel(t, "")
	m.typing = false
	m.search.Blur()
	m.pane = paneChat
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("、")})
	if !m.typing || m.chat.typing {
		t.Fatal("、 (a / under a CJK input method) focuses the search box, even from the right pane")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.chat.typing {
		t.Fatal("ctrl+s searches this session")
	}
}

func TestEnterEndsTypingInMessageSearchSoNWalksHits(t *testing.T) {
	m, _ := msgModel(t, "> 滚轮 加速")
	search(t, m)
	m.focusSearch()
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Enter to walk the hits") && !strings.Contains(v, "Enter 开始看命中") {
		t.Fatalf("while typing the title says how to reach the hits:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // moved: in a normal search Enter would resume
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.typing || m.ov.active() {
		t.Fatalf("Enter only leaves the box: typing=%v overlay=%v", m.typing, m.ov.active())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.search.Value() != "> 滚轮 加速" {
		t.Fatalf("n no longer types into the box: %q", m.search.Value())
	}
	if f := ansi.Strip(m.footer()); !strings.Contains(f, "n/N") {
		t.Fatalf("out of the box the footer offers n/N: %q", f)
	}
}

func TestTheHitListFollowsTheRightPane(t *testing.T) {
	m, recs := msgModel(t, "> 滚轮 加速")
	search(t, m)
	m.cursor = 1
	r := recs[0]
	page := capture.Messages(r.TranscriptPath, -1, recentMsgs)
	m.probes = map[*fav.Rec]*probe{r: {done: true, msgs: page.Msgs, from: page.From, full: page.Done}}
	m.typing = false
	m.search.Blur()
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	loadHits(t, m)
	start := m.msg.hl.cur
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // into the right pane
	if m.pane != paneChat {
		t.Fatal("→ from the hit list goes to the right pane")
	}
	key := tea.KeyDown
	if m.chatCur == len(page.Msgs)-1 {
		key = tea.KeyUp
	}
	m.Update(tea.KeyMsg{Type: key})
	if m.msg.hl.cur == start || m.msg.hl.items[m.msg.hl.cur].Off != page.Msgs[m.chatCur].Off {
		t.Fatalf("moving in the right pane moves the list's selection: %d → %d", start, m.msg.hl.cur)
	}
}

func TestResumeDialogOffersTheDesktopApp(t *testing.T) {
	m := sized(t, 140, 40)
	capture.SetAppAvailable(fav.ProviderClaude, true)
	capture.SetAppAvailable(fav.ProviderCodex, true)
	r := m.current()
	r.SessionID, r.Provider = "c5126b86-64bb-46a8-9a69-fc421c8f4f9a", fav.ProviderClaude
	appFiles(t, r)
	m.cfg.ResumeIn = fav.ResumeOrigin

	r.App = false
	m.askResume()
	if m.ov.app || !strings.Contains(ansi.Strip(m.View()), "Claude") {
		t.Fatal("a terminal session: the app is offered, not first")
	}
	m.closeOverlay()

	r.App = true
	m.askResume()
	if !m.ov.app {
		t.Fatal("started in the app, the setting follows the origin: the app comes first")
	}
	for _, l := range strings.Split(m.View(), "\n") {
		if w := ansi.StringWidth(l); w > 140 {
			t.Fatalf("line wider than the terminal (%d): %q", w, ansi.Strip(l))
		}
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ov.active() || cmd == nil || !strings.Contains(m.notice, "Claude") {
		t.Fatalf("Enter hands it to the app: %q", m.notice)
	}
}

func TestAppButtonAppearsWhenTheLookupAnswers(t *testing.T) {
	m := sized(t, 140, 40)
	capture.ForgetAppAvailable(fav.ProviderClaude)
	t.Cleanup(func() { capture.ForgetAppAvailable(fav.ProviderClaude) })
	r := m.current()
	r.SessionID, r.Provider = "c5126b86-64bb-46a8-9a69-fc421c8f4f9a", fav.ProviderClaude
	appFiles(t, r)

	m.askResume()
	if m.pending == nil {
		t.Fatal("the lookup starts in the background")
	}
	btn := i18n.F("resume.btn_app", "Claude")
	if strings.Contains(ansi.Strip(m.View()), btn) {
		t.Fatal("no button yet")
	}
	m.pending = nil
	capture.SetAppAvailable(fav.ProviderClaude, true)
	m.Update(appProbedMsg{})
	if !strings.Contains(ansi.Strip(m.View()), btn) {
		t.Fatal("the button appears once the app is found")
	}
	m.closeOverlay()
	m.askResume()
	if m.pending != nil {
		t.Fatal("a known answer is not looked up again")
	}
}

// appFiles puts r's transcript where the desktop apps read it, under a temporary HOME, with an existing cwd.
func appFiles(t *testing.T, r *fav.Rec) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".claude", "projects", "-p")
	if r.Provider == fav.ProviderCodex {
		dir = filepath.Join(home, ".codex", "sessions")
	}
	os.MkdirAll(dir, 0o755)
	r.TranscriptPath, r.Cwd = filepath.Join(dir, r.SessionID+".jsonl"), home
	os.WriteFile(r.TranscriptPath, []byte("{}\n"), 0o644)
}

func TestResumeDialogOpensTheFileManager(t *testing.T) {
	m := sized(t, 140, 40)
	m.askResume()
	if !strings.Contains(ansi.Strip(m.View()), "o "+fileManagerName()) {
		t.Fatal("the project row offers the file manager")
	}
}

func TestAppKeyIsP(t *testing.T) {
	m := sized(t, 140, 40)
	capture.SetAppAvailable(fav.ProviderClaude, true)
	r := m.current()
	r.SessionID, r.Provider = "c5126b86-64bb-46a8-9a69-fc421c8f4f9a", fav.ProviderClaude
	appFiles(t, r)
	m.askResume()
	if !strings.Contains(ansi.Strip(m.View()), "p Claude App") {
		t.Fatal("the app button is labelled p")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if m.ov.active() || cmd == nil {
		t.Fatal("p hands the session to the app")
	}
}

func TestButtonKeysStandOut(t *testing.T) {
	for l, key := range map[string]string{"f 收藏": "f", "Enter 恢复": "Enter", "Esc 取消": "Esc", "Ctrl+S 保存": "Ctrl+S", "p ChatGPT App": "p", "j/k · Esc close": "j/k"} {
		if k, _, ok := labelKey(l); !ok || k != key {
			t.Errorf("%q: key %q, want %q", l, k, key)
		}
	}
	for _, l := range []string{"Confirm", "Open in App", "确认 删除"} {
		if _, _, ok := labelKey(l); ok || keyedLabel(l) != l {
			t.Errorf("%q has no key: unchanged", l)
		}
	}
}

func TestResumeDialogEditKeys(t *testing.T) {
	m := sized(t, 140, 40)
	m.askResume()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.ov.kind != ovResume || !m.ov.editing {
		t.Fatal("n edits the title in place")
	}
	m.closeOverlay()
	m.askResume()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.ov.kind != ovEdit {
		t.Fatal("e opens the full editor, like the e button")
	}
}

func TestNoticeExpires(t *testing.T) {
	m := sized(t, 140, 40)
	m.askResume()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !strings.HasPrefix(m.notice, i18n.T("resume.copied")) || cmd == nil {
		t.Fatalf("copying says so and schedules the notice's expiry: %q", m.notice)
	}
	seq := m.noticeSeq
	m.flash("newer")
	m.Update(noticeExpiredMsg{seq})
	if m.notice != "newer" {
		t.Fatal("an older expiry leaves a newer notice alone")
	}
	m.Update(noticeExpiredMsg{m.noticeSeq})
	if m.notice != "" {
		t.Fatal("the notice goes away when its time is up")
	}
}

func TestNoCopyButtonWithoutACommand(t *testing.T) {
	m := sized(t, 140, 40)
	m.askResume()
	if !strings.Contains(ansi.Strip(m.View()), i18n.T("resume.btn_copy")) {
		t.Fatal("a resumable session offers the command")
	}
	m.ov.plan.Spec = capture.CommandSpec{}
	if strings.Contains(ansi.Strip(m.View()), i18n.T("resume.btn_copy")) {
		t.Fatal("nothing to copy (a session running in Herdr): no copy button")
	}
}

func TestFooterKeepsHelpAndSearch(t *testing.T) {
	for _, lang := range []string{"zh", "en"} {
		i18n.Set(lang)
		for _, w := range []int{140, 100, 80, 60, 50} {
			m := sized(t, w, 30)
			f := ansi.Strip(m.footer())
			if ansi.StringWidth(f) > w || !strings.Contains(f, i18n.T("footer.help")) || !strings.Contains(f, i18n.T("footer.search")) {
				t.Errorf("%s %d: help and search stay, within the width: %q", lang, w, f)
			}
			if w >= 100 && strings.Contains(f, i18n.T("footer.delete")) {
				t.Errorf("%s %d: record management lives in the Enter dialog: %q", lang, w, f)
			}
		}
	}
	i18n.Set("zh")
}

func TestTrashBlocksEveryAlias(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 140, 40)
	r := m.current()
	transcript := filepath.Join(t.TempDir(), r.SessionID+".jsonl")
	os.WriteFile(transcript, []byte("{}\n"), 0o644)
	r.PinnedPath = transcript
	m.store.Put(r)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.search.SetValue("status:trash")
	m.refresh()
	if m.current() == nil {
		t.Fatal("the deleted session is in the trash")
	}
	m.pane = paneChat
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("*")}, {Type: tea.KeyCtrlX}, {Type: tea.KeyCtrlA}, {Type: tea.KeySpace, Runes: []rune(" ")}} {
		m.Update(k)
		if m.store.Get(r.ID) != nil || m.ov.active() || m.quitting {
			t.Fatalf("in the trash %q is blocked like f, also from the right pane", k.String())
		}
	}
}

func TestSpacePressesTheAppButtonWhenItLeads(t *testing.T) {
	m := sized(t, 140, 40)
	capture.SetAppAvailable(fav.ProviderClaude, true)
	r := m.current()
	r.SessionID, r.Provider = "c5126b86-64bb-46a8-9a69-fc421c8f4f9a", fav.ProviderClaude
	appFiles(t, r)
	m.cfg.ResumeIn = fav.ResumeApp
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if m.quitting || cmd == nil || !strings.Contains(m.notice, "Claude") {
		t.Fatalf("Space hands the session to the app, like Enter in the dialog: quitting=%v notice=%q", m.quitting, m.notice)
	}
}

func TestCompactEnterShowsTheDetail(t *testing.T) {
	m := sized(t, 50, 20)
	before := ansi.Strip(m.View())
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.detail || ansi.Strip(m.View()) == before {
		t.Fatal("under 60 columns Enter shows the detail")
	}
}

func TestSearchFromTheRightPaneMovesTheList(t *testing.T) {
	m := sized(t, 140, 40)
	m.pane = paneChat
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.typing || m.pane != paneList {
		t.Fatal("/ from the right pane: the arrows select records")
	}
}

func TestProjectHeaderFooterFollowsFolding(t *testing.T) {
	m := sized(t, 140, 40)
	m.view = viewProjects
	m.refresh()
	m.cursor = 0
	if m.current() != nil {
		t.Skip("first row is not a group header")
	}
	first := ansi.Strip(m.footer())
	m.toggleGroup()
	second := ansi.Strip(m.footer())
	has := func(f, k string) bool { return strings.Contains(f, i18n.T(k)) }
	if has(first, "footer.enter_expand") == has(second, "footer.enter_expand") || has(first, "footer.enter_collapse") == has(second, "footer.enter_collapse") {
		t.Fatalf("Enter says expand on a folded group and collapse on an open one: %q → %q", first, second)
	}
}

func TestResumeAsksWhichWorkspace(t *testing.T) {
	m := sized(t, 140, 40)
	appFiles(t, m.current())
	m.askResume()
	m.ov.plan.Ws = nil
	m.ov.plan.WsChoices = []herdr.Workspace{{WorkspaceID: "a", Label: "api"}, {WorkspaceID: "b", Label: "web"}}
	m.doResume(false)
	if m.ov.kind != ovPicker || len(m.ov.items) != 3 {
		t.Fatalf("two workspaces fit: the user picks (or this terminal): kind=%v items=%d", m.ov.kind, len(m.ov.items))
	}
	m.ov.cursor = 1
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !strings.Contains(m.notice, "web") || m.quitting {
		t.Fatalf("picking web opens the tab there: notice=%q", m.notice)
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

func TestAttentionQueue(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 140, 40)
	r := m.current()
	id := r.SessionID
	m.live = map[string]capture.Live{id: {Status: "idle"}}
	m.applyPulses(pulseMsg{id: {Size: 100, Finished: true}})
	if m.need(id) != needNone || m.notice != "" {
		t.Fatal("first seen: its old output is not news")
	}
	m.applyPulses(pulseMsg{id: {Size: 200, Finished: true}})
	if m.need(id) != needUnseen || !strings.Contains(m.notice, r.Title[:3]) || m.needCount() != 1 {
		t.Fatalf("new output, finished: unseen and announced once: need=%d notice=%q", m.need(id), m.notice)
	}
	if !strings.Contains(agentsTab(len(m.live), m.needCount()), "!1") {
		t.Fatal("the tab says one needs you")
	}
	m.notice = ""
	m.applyPulses(pulseMsg{id: {Size: 200, Finished: true}})
	if m.notice != "" {
		t.Fatal("announced once, not every poll")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(".")})
	if m.need(id) != needNone {
		t.Fatal(". marks it handled")
	}
	m.applyPulses(pulseMsg{id: {Size: 300, Finished: true}})
	if m.need(id) != needUnseen {
		t.Fatal("new output after handled brings it back")
	}

	reloaded := loadAttn()
	if reloaded[id].Seen != 200 || reloaded[id].Quiet != 200 {
		t.Fatalf("what the user took in survives a restart: %+v", reloaded[id])
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	if m.need(id) != needNone {
		t.Fatal("H snoozes it")
	}
}

func TestAttentionAskingAndGroups(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	m := sized(t, 140, 40)
	recs := m.store.All()
	a, b, c := recs[0].SessionID, recs[1].SessionID, recs[2].SessionID
	m.live = map[string]capture.Live{a: {Status: "working"}, b: {Status: "idle"}, c: {Status: "working"}}
	m.applyPulses(pulseMsg{a: {Size: 10}, b: {Size: 10, Finished: true}, c: {Size: 10, Asking: true}})
	if m.need(c) != needWait {
		t.Fatal("an open question needs the user even when first seen")
	}
	if text, _ := m.needLabel(c, m.live[c]); !strings.Contains(text, i18n.T("attn.asking")) {
		t.Fatalf("the card says it is asking: %q", text)
	}
	m.cfg.LiveSort = liveSortGroup
	rows := m.liveRows([]*fav.Rec{recs[0], recs[1], recs[2]})
	if rows[0].group != i18n.T("live.waiting") || rows[1].rec.SessionID != c {
		t.Fatalf("the waiting group comes first: %+v", rows)
	}
}
