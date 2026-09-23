package tui

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

// Who is running: polled every 3 s (Herdr, Claude sessions/*.json, Codex locks); with the cursor on a live session the right pane re-reads its tail.

const liveEvery = 3 * time.Second

type liveMsg struct {
	local, herdr map[string]capture.Live
}

type liveTickMsg struct{}

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
	for id, l := range next {
		if l.Status == "blocked" && m.live[id].Status != "blocked" && m.live != nil {
			title := l.Title
			if r := m.bySession(id); r != nil {
				title = r.Title
			}
			m.flash(render.GlyphWarn + i18n.F("live.waiting_flash", render.Truncate(title, 40)))
		}
	}
	changed := len(next) != len(m.live)
	for id, l := range next {
		if old, ok := m.live[id]; !ok || old.Status != l.Status {
			changed = true
		}
	}
	m.live = next
	if changed {
		m.refresh()
	}
}

func (m *Model) liveOf(r *fav.Rec) (capture.Live, bool) {
	if r == nil {
		return capture.Live{}, false
	}
	l, ok := m.live[r.SessionID]
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

func (m *Model) synthLive(q fav.Query) []*fav.Rec {
	var out []*fav.Rec
	for id, l := range m.live {
		if m.bySession(id) != nil {
			continue
		}
		r := m.synth[id]
		if r == nil {
			if m.synth == nil {
				m.synth = map[string]*fav.Rec{}
			}
			r = &fav.Rec{Provider: l.Agent, SessionID: id, Status: fav.StatusDoing}
			m.synth[id] = r
		}
		r.Title, r.Cwd, r.Project = l.Title, l.Cwd, ""
		if l.Cwd != "" {
			r.Project = filepath.Base(l.Cwd)
		}
		if r.Title == "" {
			r.Title = i18n.F("live.just_started", id[:min(8, len(id))])
		}
		if r.TranscriptPath == "" {
			r.TranscriptPath = m.idx.Transcript(id)
		}
		r.Prepare()
		if q.Match(r) {
			out = append(out, r)
		}
	}
	return out
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
		sort.SliceStable(recs, func(i, j int) bool {
			a, b := recs[i].When(), recs[j].When()
			if a.IsZero() != b.IsZero() {
				return a.IsZero()
			}
			return a.After(b)
		})
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
		l, _ := m.liveOf(r)
		_, tone := liveLabel(l, m.now)
		by[tone] = append(by[tone], r)
	}
	var out []row
	for _, g := range []struct {
		tone int
		name string
	}{{toneBlocked, i18n.T("live.waiting")}, {toneWork, i18n.T("live.working")}, {toneIdle, i18n.T("live.idle")}, {toneDone, i18n.T("live.finished")}} {
		if len(by[g.tone]) == 0 {
			continue
		}
		out = append(out, row{group: g.name, count: len(by[g.tone])})
		for _, r := range by[g.tone] {
			out = append(out, row{rec: r})
		}
	}
	return out
}

func (m *Model) liveSummary() string {
	n := map[int]int{}
	for _, l := range m.live {
		_, tone := liveLabel(l, m.now)
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
	if r == nil || p == nil || !p.done || p.loading {
		return nil
	}
	if _, ok := m.liveOf(r); !ok {
		return nil
	}
	path := transcript(r)
	if path == "" {
		return nil
	}
	return func() tea.Msg { return refreshMsg{r, capture.Messages(path, -1, recentMsgs)} }
}

// canApply: no merging while the right pane has focus, is scrolled, is searching or shows the full text.
func (m *Model) canApply() bool {
	return m.pane == paneList && m.chatScroll == 0 && m.chatSkip == 0 && !m.chat.typing && m.ov.kind != ovMessage
}

func (m *Model) stash(msg refreshMsg) {
	p := m.probes[msg.rec]
	if p == nil || !p.done {
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

func (m *Model) closeLive() {
	r := m.current()
	l, ok := m.liveOf(r)
	if !ok || l.TabID == "" {
		m.flash(i18n.T("live.not_in_herdr"))
		return
	}
	label, _ := liveLabel(l, time.Now())
	tab := l.TabID
	// irreversible: focus starts on Cancel, y or ← Enter closes
	m.ov = overlay{kind: ovConfirm, title: i18n.T("live.close_title"), focus: 1, okLabel: i18n.T("live.btn_close"),
		lines: []string{i18n.F("live.close_item", render.Truncate(r.Title, 40)), label + i18n.T("live.close_hint")},
		confirm: func(m *Model) {
			m.pending = func() tea.Msg {
				if err := herdr.CloseTab(tab); err != nil {
					return herdrDoneMsg{rec: r, focused: true, err: err}
				}
				return herdrDoneMsg{rec: r, focused: true, msg: i18n.T("live.closed") + r.Title}
			}
		}}
}
