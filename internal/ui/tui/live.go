package tui

import (
	"cmp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
)

// Who is running: polled every 3 s (Herdr, Claude sessions/*.json, Codex locks); with the cursor on a live session the right pane re-reads its tail.

const liveEvery = 3 * time.Second

type liveMsg struct {
	local, herdr map[string]capture.Live
}

type liveTickMsg struct{}

type pulseMsg map[string]capture.Pulse

// readPulses reads the transcript tails of the running sessions in the background; unchanged files cost a stat.
func (m *Model) readPulses() tea.Cmd {
	srcs := map[string]remote.Source{}
	for id := range m.live {
		if r := m.bySession(id); r != nil && transcript(r) != "" {
			srcs[id] = m.hosts.Source(r)
		}
	}
	if len(srcs) == 0 {
		return nil
	}
	return func() tea.Msg {
		out := make(pulseMsg, len(srcs))
		for id, src := range srcs {
			if pl, ok := src.Pulse(); ok {
				pl.Asking = pl.Asking || capture.HookWaiting(id, pl.Size)
				out[id] = pl
			}
		}
		return out
	}
}

// pulseText: "this turn 12m · context 40%" (Claude: tokens, its transcripts do not record the window) and the last reply.
func pulseText(p capture.Pulse, l capture.Live, now time.Time) string {
	var parts []string
	if l.Status == "working" && !p.TurnAt.IsZero() {
		parts = append(parts, i18n.F("live.turn", render.ShortDur(now.Sub(p.TurnAt))))
	}
	switch {
	case p.Window > 0:
		parts = append(parts, i18n.F("live.context_pct", p.Context*100/p.Window))
	case p.Context > 0:
		parts = append(parts, i18n.F("live.context_tokens", tokens(p.Context)))
	}
	if p.Reply != "" {
		parts = append(parts, i18n.F("live.reply", p.Reply))
	}
	return strings.Join(parts, "  ·  ")
}

// tokens: 950, 12k, 509k, 1.2M.
func tokens(n int) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	case n >= 1000:
		return strconv.Itoa(n/1000) + "k"
	}
	return strconv.Itoa(n)
}

func (m *Model) pollLive() tea.Cmd {
	prev := m.liveHerdr
	return func() tea.Msg {
		local := capture.LocalLive()
		return liveMsg{local: local, herdr: capture.HerdrLive(local, prev)}
	}
}

func (m *Model) applyLive(msg liveMsg) {
	m.now = time.Now()
	m.liveHerdr = msg.herdr
	next := capture.MergeLive(msg.local, msg.herdr)
	for id, l := range next {
		if l.Status == "" { // Codex reports no busy/idle: written within 15 s = working, else idle since the last write
			if r := m.bySession(id); r != nil && !r.LastAt.IsZero() {
				l.Since, l.Status = r.LastAt, "idle"
				if m.now.Sub(r.LastAt) < 15*time.Second {
					l.Status = "working"
				}
				next[id] = l
			}
		}
	}
	changed := len(next) != len(m.live)
	for id, l := range next {
		if old, ok := m.live[id]; !ok || old.Status != l.Status {
			changed = true
		}
	}
	m.live = next
	m.checkNeeds()
	if changed {
		m.refresh()
	}
}

func (m *Model) liveOf(r *fav.Rec) (capture.Live, bool) {
	if r == nil {
		return capture.Live{}, false
	}
	live := m.live
	if r.Host != "" {
		if hr := m.remote[r.Host]; hr != nil {
			live = hr.live
		} else {
			live = nil
		}
	}
	l, ok := live[r.SessionID]
	return l, ok
}

func (m *Model) bySession(id string) *fav.Rec {
	for _, rs := range [][]*fav.Rec{m.store.All(), m.unfav} {
		for _, r := range rs {
			if r.SessionID == id {
				return r
			}
		}
	}
	return nil
}

// indexes into theme.liveTone
const (
	toneWork = iota
	toneBlocked
	toneIdle
	toneDone
)

func liveLabel(l capture.Live, now time.Time) (string, int) {
	tone := toneWork
	switch l.Status {
	case "blocked":
		tone = toneBlocked
	case "idle":
		tone = toneIdle
	case "done":
		tone = toneDone
	}
	return render.LiveText(l, now), tone
}

const (
	liveSortStarted = "started"
	liveSortGroup   = "group"
	liveSortActive  = "active"
)

var liveSorts = []string{liveSortStarted, liveSortGroup, liveSortActive}

// liveRows: default is by start time (missing = newest) so rows do not jump; group is waiting / working / idle / finished; active is by last activity.
func (m *Model) liveRows(recs []*fav.Rec) []row {
	if m.cfg.LiveSort == liveSortActive {
		recs, _ = sortActive.sorted(recs)
	} else {
		fav.SortByStart(recs)
	}
	if m.cfg.LiveSort != liveSortGroup {
		out := make([]row, len(recs))
		for i, r := range recs {
			out[i] = row{rec: r}
		}
		return out
	}
	by := map[int][]*fav.Rec{}
	for _, r := range recs {
		g := m.liveGroup(r)
		by[g] = append(by[g], r)
	}
	var out []row
	for g, name := range liveGroupNames() {
		if len(by[g]) == 0 {
			continue
		}
		out = append(out, row{group: name, count: len(by[g])})
		for _, r := range by[g] {
			out = append(out, row{rec: r})
		}
	}
	return out
}

// live groups, most in need of the user first
const (
	groupWait = iota
	groupUnseen
	groupWork
	groupIdle
	groupDone
)

func liveGroupNames() []string {
	return []string{i18n.T("live.waiting"), i18n.T("attn.unseen_group"), i18n.T("live.working"), i18n.T("live.idle"), i18n.T("live.finished")}
}

func (m *Model) liveGroup(r *fav.Rec) int {
	need := needNone
	if r.Host == "" { // attention is this machine's, by session id
		need = m.need(r.SessionID)
	}
	switch need {
	case needWait:
		return groupWait
	case needUnseen:
		return groupUnseen
	}
	l, _ := m.liveOf(r)
	switch _, tone := liveLabel(l, m.now); tone {
	case toneBlocked: // blocked but handled / snoozed: it still waits
		return groupWait
	case toneIdle:
		return groupIdle
	case toneDone:
		return groupDone
	}
	return groupWork
}

// agentsTab: "Agents 5", with "!2" when two of them need the user.
func agentsTab(n, need int) string {
	s := "Agents " + strconv.Itoa(n)
	if need > 0 {
		s += " !" + strconv.Itoa(need)
	}
	return s
}

func (m *Model) liveSummary() string {
	n := map[int]int{}
	for id, l := range m.live {
		_, tone := m.needLabel(id, l)
		n[tone]++
	}
	var parts []string
	for _, g := range []struct {
		tone  int
		glyph string
		name  string
	}{{toneBlocked, render.GlyphWarn, i18n.T("live.waiting")}, {toneWork, render.GlyphLive, i18n.T("live.working")}, {toneIdle, render.GlyphSession, i18n.T("live.idle")}, {toneDone, render.GlyphDone, i18n.T("live.finished")}} {
		if n[g.tone] == 0 {
			continue
		}
		sty := dimmed
		if g.tone == toneBlocked {
			sty = liveTone[toneBlocked]
		}
		parts = append(parts, sty.Render(g.glyph+" "+strconv.Itoa(n[g.tone])+" "+g.name))
	}
	return strings.Join(parts, dimmed.Render(" · "))
}

type refreshMsg struct {
	rec  *fav.Rec
	page capture.Page
}

// refreshChat re-reads the tail of the live session under the cursor into p.fresh, merged when canApply.
func (m *Model) refreshChat() tea.Cmd {
	r := m.current()
	p := m.probes[r]
	if r == nil || p == nil || !p.done || p.loading || p.polling {
		return nil
	}
	if _, ok := m.liveOf(r); !ok {
		return nil
	}
	if r.Host == "" && transcript(r) == "" {
		return nil
	}
	p.polling = true
	src := m.hosts.Source(r)
	return func() tea.Msg { return refreshMsg{r, src.Messages(-1, recentMsgs)} }
}

// canApply: no merging while the right pane has focus, is scrolled, is searching or shows the full text.
func (m *Model) canApply() bool {
	return m.pane == paneList && m.chatScroll == 0 && m.chatSkip == 0 && !m.chat.typing && m.ov.kind != ovMessage
}

func (m *Model) stash(msg refreshMsg) {
	p := m.probes[msg.rec]
	if p != nil {
		p.polling = false
	}
	if p == nil || !p.done || msg.page.Err != nil {
		return
	}
	// keep only what is newer than the current first message; nothing lines up → replace everything
	msgs := msg.page.Msgs
	n := len(msgs)
	if len(p.msgs) > 0 {
		n = -1
		for i, x := range msgs {
			if x.At.Equal(p.msgs[0].At) && x.Text == p.msgs[0].Text {
				n = i
				break
			}
		}
	}
	if n == 0 {
		if len(msgs) > 0 && len(p.msgs) > 0 && len(msgs[0].Steps) != len(p.msgs[0].Steps) { // tool steps appended after the newest message
			p.msgs[0].Steps = msgs[0].Steps
		}
		p.fresh = nil
		return
	}
	p.fresh, p.replace = msgs, n < 0
	if n > 0 {
		p.fresh = msgs[:n]
	}
}

func (m *Model) applyFresh() {
	p := m.probes[m.current()]
	if p == nil || p.fresh == nil || !m.canApply() || (p.replace && p.loading) {
		return
	}
	if p.replace {
		p.msgs, p.full = p.fresh, false
		p.from = p.msgs[len(p.msgs)-1].Off
		m.chatCur = 0
	} else {
		p.msgs = append(p.fresh, p.msgs...)
		if m.chatCur > 0 {
			m.chatCur += len(p.fresh)
		}
	}
	p.fresh, p.replace = nil, false
	m.hitsFor = nil
}

func (m *Model) closeLive(r *fav.Rec) {
	l, ok := m.liveOf(r)
	if !ok || l.TabID == "" {
		m.flash(i18n.T("live.not_in_herdr"))
		return
	}
	label, _ := liveLabel(l, time.Now())
	tab := l.TabID
	lines := []string{i18n.F("live.close_item", render.Truncate(r.Title, 40)), label + i18n.T("live.close_hint")}
	m.openConfirm(i18n.T("live.close_title"), i18n.T("live.btn_close"), lines, func(m *Model) {
		m.pending = func() tea.Msg {
			if err := herdr.CloseTab(tab); err != nil {
				return herdrDoneMsg{rec: r, err: err}
			}
			return herdrDoneMsg{rec: r, msg: i18n.F("live.closed", r.Title)}
		}
	}, nil)
}

// closeIdle (Z, Agents): close every Herdr tab whose session has been quiet for capture.IdleAfter and has nothing the user
// has not seen; irreversible, so the dialog starts on Cancel.
func (m *Model) closeIdle() {
	type idle struct {
		tab, title string
		for_       time.Duration
	}
	var list []idle
	for id, l := range m.live {
		r := m.bySession(id)
		if l.TabID == "" || r == nil || l.Status == "working" || l.Status == "blocked" || m.need(id) != needNone {
			continue
		}
		if d := m.now.Sub(r.ActiveAt()); d >= capture.IdleAfter {
			list = append(list, idle{l.TabID, r.Title, d})
		}
	}
	hours := int(capture.IdleAfter.Hours())
	if len(list) == 0 {
		m.flash(i18n.F("live.no_idle", hours))
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].for_ > list[j].for_ })
	lines := []string{i18n.F("live.close_idle_intro", len(list), hours), ""}
	for i, it := range list {
		if i == 8 {
			lines = append(lines, i18n.F("project.more", len(list)-i))
			break
		}
		lines = append(lines, "  "+render.Truncate(it.title, 50)+"  ·  "+render.ShortDur(it.for_))
	}
	lines = append(lines, "", i18n.T("live.close_hint"))
	tabs := make([]string, len(list))
	for i, it := range list {
		tabs[i] = it.tab
	}
	m.openConfirm(i18n.T("live.close_idle_title"), i18n.F("live.btn_close_n", len(list)), lines, func(m *Model) {
		m.pending = func() tea.Msg {
			closed := 0
			var first error
			for _, t := range tabs {
				if err := herdr.CloseTab(t); err != nil {
					first = cmp.Or(first, err)
					continue
				}
				closed++
			}
			return herdrDoneMsg{msg: i18n.F("live.closed_idle", closed), err: first}
		}
	}, nil)
}
