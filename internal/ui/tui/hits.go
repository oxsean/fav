package tui

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

// hitList: → on a message-search result lists every hit of that session in the left pane; the right pane follows the selection.
type hitList struct {
	rec       *fav.Rec
	key       string // msgKey of rec: records are rebuilt on a store reload
	q         string
	items     []fulltext.Hit
	self      map[string]bool // item paths the right pane reads (the newest transcript, or its pinned hard link)
	loading   bool
	total     int                // every match; items holds the newest hitLimit
	cancel    context.CancelFunc // the read in flight
	openWant  bool               // Enter came while the pane was still paging to the hit
	syncOff   int64              // message offset the selection last matched: the right pane moving elsewhere moves it
	cur, top  int
	lastClick time.Time
}

const (
	hitRows  = 4 // header, two snippet lines, blank
	hitLimit = 500
)

func (m *Model) hitsOpen() bool {
	hl, r := &m.msg.hl, m.current()
	if hl.key == "" || r == nil || msgKey(r) != hl.key || hl.q != m.findQuery() {
		return false
	}
	hl.rec = r
	return true
}

// openHits lists the hits of kw in the current session: → on a > result, or a \ search.
func (m *Model) openHits(kw string) tea.Cmd {
	r := m.current()
	if r == nil || kw == "" {
		return nil
	}
	paths := fulltext.Cands([]*fav.Rec{r}, m.idx.PathsBySession())[0].Paths
	var pin fulltext.Hit
	if x, ok := m.msgHit(r); ok {
		pin = fulltext.Hit{Path: x.Path, Off: x.Off}
	}
	key, dir, tr := msgKey(r), fulltext.Dir(), transcript(r)
	var loaded []capture.Message // the right pane's messages: a live session's newest may not be in the text store yet
	if p := m.probes[r]; p != nil {
		loaded = p.msgs
	}
	m.closeHits()
	ctx, cancel := context.WithCancel(context.Background())
	m.msg.hl = hitList{rec: r, key: key, q: kw, loading: true, cancel: cancel}
	m.pane = paneList
	return func() tea.Msg {
		items, total := fulltext.Hits(ctx, dir, paths, kw, hitLimit, pin)
		if fresh := freshHits(dir, tr, kw, loaded); len(fresh) > 0 {
			items, total = append(fresh, items...), total+len(fresh)
		}
		return hitsMsg{key, kw, pin, items, total}
	}
}

// freshHits are the loaded messages past what the text store has read of tr, matching kw; newest first like msgs.
func freshHits(dir, tr, kw string, msgs []capture.Message) []fulltext.Hit {
	if tr == "" || len(msgs) == 0 {
		return nil
	}
	upto := fulltext.Covered(dir, tr)
	query := fulltext.ParseQuery(kw)
	var out []fulltext.Hit
	for _, msg := range msgs {
		if msg.Off < upto {
			break
		}
		role := roleByte(msg.Role)
		if !query.MatchEntry(role, msg.Text) {
			continue
		}
		out = append(out, fulltext.Hit{Path: tr, Off: msg.Off, Role: role, At: msg.At, Text: msg.Text})
	}
	return out
}

type hitsMsg struct {
	key, q string
	pin    fulltext.Hit
	items  []fulltext.Hit
	total  int
}

func (m *Model) applyHits(msg hitsMsg) tea.Cmd {
	hl := &m.msg.hl
	if msg.key != hl.key || msg.q != hl.q || !hl.loading {
		return nil
	}
	if len(msg.items) == 0 {
		m.dropHits()
		m.flash(i18n.F("hits.none", msg.q))
		return nil
	}
	tr := transcript(hl.rec)
	hl.items, hl.total, hl.loading, hl.cancel, hl.self = msg.items, msg.total, false, nil, map[string]bool{}
	cur := 0
	for i, h := range msg.items {
		if _, ok := hl.self[h.Path]; !ok {
			hl.self[h.Path] = paths.SameFile(h.Path, tr)
		}
		if h.Path == msg.pin.Path && h.Off == msg.pin.Off && cur == 0 {
			cur = i
		}
	}
	return m.selectHit(cur)
}

// dropHits closes the list for good: a \ search's query goes with it.
func (m *Model) dropHits() {
	if q := m.msg.hl.q; q != "" && q == m.chat.query() && !m.chat.typing {
		m.chat.input.SetValue("")
	}
	m.closeHits()
}

func (m *Model) closeHits() {
	if m.msg.hl.cancel != nil {
		m.msg.hl.cancel()
	}
	m.msg.hl = hitList{}
}

func (m *Model) selectHit(i int) tea.Cmd {
	hl := &m.msg.hl
	if len(hl.items) == 0 {
		return nil
	}
	hl.cur, hl.openWant = min(max(i, 0), len(hl.items)-1), false
	h := hl.items[hl.cur]
	hl.syncOff = h.Off
	if !hl.self[h.Path] {
		return nil // an earlier file of a continued session: the right pane only reads the newest
	}
	return m.showOff(h.Off)
}

func (m *Model) clickHit(i int) tea.Cmd {
	hl := &m.msg.hl
	now := time.Now()
	double := hl.cur == i && now.Sub(hl.lastClick) < 500*time.Millisecond
	hl.lastClick = now
	m.pane = paneList
	if double {
		hl.lastClick = time.Time{}
		return m.openHitMessage()
	}
	return m.selectHit(i)
}

// openHitMessage opens the full text of the selected hit: the right pane's message once it stands there, the stored text
// for a hit the pane cannot show.
func (m *Model) openHitMessage() tea.Cmd {
	hl := &m.msg.hl
	if len(hl.items) == 0 {
		return nil
	}
	h := hl.items[hl.cur]
	if !hl.self[h.Path] {
		role := "assistant"
		if h.Role == 'u' || h.Role == 's' {
			role = "user"
		}
		m.showMessage(capture.Message{Role: role, Text: h.Text, Chars: utf8.RuneCountInString(h.Text), Off: h.Off, At: h.At})
		return nil
	}
	if msg, ok := m.currentMessage(); ok && msg.Off == h.Off {
		return m.openMessage()
	}
	hl.openWant = true
	return nil
}

// hitKey handles the keys of an open hit list; false leaves the key to the list.
func (m *Model) hitKey(a act) (tea.Cmd, bool) {
	cur := m.msg.hl.cur
	switch a {
	case actDown, actNextHit:
		return m.selectHit(cur + 1), true
	case actUp, actPrevHit:
		return m.selectHit(cur - 1), true
	case actPageDown:
		return m.selectHit(cur + 5), true
	case actPageUp:
		return m.selectHit(cur - 5), true
	case actTop:
		return m.selectHit(0), true
	case actBottom:
		return m.selectHit(len(m.msg.hl.items) - 1), true
	case actEnter:
		return m.openHitMessage(), true
	case actRight:
		m.pane = paneChat
		return nil, true
	case actLeft, actBack:
		m.dropHits()
		return nil, true
	}
	return nil, false
}

func (m *Model) hitTitle() string {
	hl := &m.msg.hl
	title := i18n.F("hits.title", len(hl.items))
	if hl.total > len(hl.items) {
		title = i18n.F("hits.title_capped", len(hl.items), hl.total)
	}
	if fixes := queryOf(hl.q).Fixes(); len(fixes) > 0 {
		title += i18n.F("msg.also", strings.Join(fixes, " "))
	}
	return title
}

func (m *Model) hitPane(y0, x0, w, h int) []string {
	hl := &m.msg.hl
	fit1 := max(1, h/hitRows)
	if hl.cur < hl.top {
		hl.top = hl.cur
	}
	if hl.cur >= hl.top+fit1 {
		hl.top = hl.cur - fit1 + 1
	}
	hl.top = min(max(hl.top, 0), max(0, len(hl.items)-fit1))
	var out []string
	if hl.loading {
		out = append(out, dimmed.Render(fit(i18n.T("hits.loading"), w)))
	}
	for i := hl.top; i < len(hl.items) && len(out)+hitRows <= h; i++ {
		it, sel, idx := hl.items[i], i == hl.cur, i
		who := "AI"
		switch it.Role {
		case 'u':
			who = i18n.T("chat.you")
		case 't':
			who = i18n.T("hits.tool")
		case 's':
			who = i18n.T("hits.recap")
		case 'o':
			who = i18n.T("hits.output")
		}
		head := who
		if !it.At.IsZero() {
			head = it.At.Local().Format("01-02 15:04") + " · " + who
		}
		if !hl.self[it.Path] {
			head += " · " + i18n.T("hits.earlier_file")
		}
		lines := render.Wrap(fulltext.Snippet(hl.q, it.Text), w-2)
		for len(lines) < 2 {
			lines = append(lines, "")
		}
		block := []string{fit(dimmed.Render(head), w)}
		if sel {
			block[0] = selTitle.Render(fit(head, w))
		}
		for _, l := range lines[:2] {
			if sel {
				block = append(block, highlightWith(fit("  "+l, w), hl.q, selBody, hitSty.Background(cSelBg)))
			} else {
				block = append(block, fit("  "+highlight(l, hl.q), w))
			}
		}
		block = append(block, "")
		for k, l := range block {
			if k < hitRows-1 {
				m.mark(y0+len(out), x0, w, func(mm *Model) { mm.pending = tea.Batch(mm.pending, mm.clickHit(idx)) })
			}
			out = append(out, l)
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

// followChat moves the selection to the hit of the message the right pane stands on, or the nearest one of the same
// transcript, so ← back to the list continues from where the reading got to.
func (m *Model) followChat() {
	hl := &m.msg.hl
	msg, ok := m.currentMessage()
	if !ok || msg.Off == hl.syncOff || hl.loading {
		return
	}
	hl.syncOff = msg.Off
	best, dist := -1, int64(-1)
	for i, h := range hl.items {
		if !hl.self[h.Path] {
			continue
		}
		d := h.Off - msg.Off
		if d < 0 {
			d = -d
		}
		if best < 0 || d < dist {
			best, dist = i, d
		}
	}
	if best >= 0 {
		hl.cur = best
	}
}
