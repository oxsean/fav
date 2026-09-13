package tui

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

// Recent chat: the last 40 messages first; a page of olderMsgs is fetched from the last offset when nearing the end;
// `\` search reads the whole file. Only one record is expanded at a time, the previous one drops back to the tail.

const olderMsgs = 20

type pageMsg struct {
	rec  *fav.Rec
	page capture.Page
}

func transcript(r *fav.Rec) string {
	if r.PinnedPath != "" {
		return r.PinnedPath
	}
	return r.TranscriptPath
}

func (m *Model) ensureOlder(r *fav.Rec) tea.Cmd { return m.load(r, olderMsgs) }

func (m *Model) ensureFull(r *fav.Rec) tea.Cmd { return m.load(r, 1<<30) }

func (m *Model) load(r *fav.Rec, n int) tea.Cmd {
	p := m.probes[r]
	if r == nil || p == nil || !p.done || p.full || p.loading {
		return nil
	}
	path := transcript(r)
	if path == "" {
		return nil
	}
	p.loading = true
	from := p.from
	return func() tea.Msg { return pageMsg{r, capture.Messages(path, from, n)} }
}

// applyPage appends the page; other expanded records drop back to the tail.
func (m *Model) applyPage(msg pageMsg) {
	p := m.probes[msg.rec]
	if p == nil {
		return
	}
	for r, o := range m.probes {
		if r != msg.rec && !o.loading && len(o.msgs) > recentMsgs {
			o.msgs = o.msgs[:recentMsgs]
			o.from, o.full = o.msgs[recentMsgs-1].Off, false
		}
	}
	p.msgs = append(p.msgs, msg.page.Msgs...)
	p.from, p.full, p.loading = msg.page.From, msg.page.Done, false
	m.hitsFor = nil
}

type chatSearch struct {
	input  textinput.Model
	typing bool
	cur    int
}

func newChatSearch() chatSearch {
	ti := textinput.New()
	ti.Placeholder = i18n.T("chat.find_placeholder")
	ti.Prompt = render.GlyphSearch + " "
	ti.CharLimit = 80
	return chatSearch{input: ti}
}

func (c *chatSearch) query() string { return strings.ToLower(strings.TrimSpace(c.input.Value())) }

func (m *Model) startChatSearch() tea.Cmd {
	m.chat.typing = true
	m.chat.input.Focus()
	return tea.Batch(textinput.Blink, m.ensureFull(m.current()))
}

func (m *Model) chatSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.chat.input.SetValue("")
		fallthrough
	case tea.KeyEnter:
		m.chat.typing = false
		m.chat.input.Blur()
		m.jumpHit(0)
		return nil
	}
	var cmd tea.Cmd
	m.chat.input, cmd = m.chat.input.Update(msg)
	m.hitsFor = nil
	return cmd
}

// hits are the message indexes matching the query, cached by record / query / message count.
func (m *Model) hits() []int {
	r, q := m.current(), m.chat.query()
	p := m.probes[r]
	if r == nil || p == nil || q == "" {
		return nil
	}
	if m.hitsFor == r && m.hitsQ == q && m.hitsN == len(p.msgs) {
		return m.hitList
	}
	m.hitsFor, m.hitsQ, m.hitsN, m.hitList = r, q, len(p.msgs), nil
	for i, msg := range p.msgs {
		if strings.Contains(strings.ToLower(msg.Text), q) {
			m.hitList = append(m.hitList, i)
		}
	}
	return m.hitList
}

func (m *Model) jumpHit(k int) {
	hs := m.hits()
	if len(hs) == 0 {
		return
	}
	m.chat.cur = ((k % len(hs)) + len(hs)) % len(hs)
	m.chatScroll, m.chatSkip, m.chatCur, m.chatFollow = hs[m.chat.cur], 0, hs[m.chat.cur], true
}

func (m *Model) moveChat(delta int) {
	p := m.probes[m.current()]
	if p == nil || len(p.msgs) == 0 {
		return
	}
	m.chatCur = min(max(m.chatCur+delta, 0), len(p.msgs)-1)
	m.chatFollow = true
}

// clickChat: a click selects, a second click on the same message within a short time opens the full text.
func (m *Model) clickChat(i int) {
	now := time.Now()
	m.pane = paneChat
	if m.chatCur == i && m.chatHit == i && now.Sub(m.chatHitAt) < 500*time.Millisecond {
		m.chatHitAt = time.Time{}
		m.pending = m.openMessage()
		return
	}
	m.chatCur, m.chatHit, m.chatHitAt, m.chatFollow = i, i, now, true
}

func (m *Model) currentMessage() (capture.Message, bool) {
	p := m.probes[m.current()]
	if p == nil || m.chatCur < 0 || m.chatCur >= len(p.msgs) {
		return capture.Message{}, false
	}
	return p.msgs[m.chatCur], true
}

// box fixed at 8 cols / 4 rows from the edges: sizing to content would jump between messages
func (m *Model) openMessage() tea.Cmd {
	msg, ok := m.currentMessage()
	if !ok {
		return nil
	}
	same := m.ov.kind == ovMessage && m.ov.msg.At.Equal(msg.At) && m.ov.msg.Off == msg.Off
	if capture.Truncated(msg.Text) {
		msg.Text = capture.TextFull(transcript(m.current()), msg.Off, msg.Text)
	}
	ov := overlay{kind: ovMessage, msg: msg, boxW: max(44, m.w-8)}
	if same {
		ov.cursor = m.ov.cursor
	}
	m.ov = ov
	m.layoutMessage()
	return nil
}

func (m *Model) layoutMessage() {
	w := m.ov.boxW - 4
	add := func(ls []string, kind byte, step int) {
		for _, l := range ls {
			m.ov.lines, m.ov.kinds, m.ov.stepOf = append(m.ov.lines, l), append(m.ov.kinds, kind), append(m.ov.stepOf, step)
		}
	}
	m.ov.lines, m.ov.kinds, m.ov.stepOf = nil, nil, nil
	add(render.Wrap(m.ov.msg.Text, w), 'T', -1)
	if len(m.ov.msg.Steps) == 0 {
		return
	}
	add([]string{""}, 'T', -1)
	for i, text := range capture.StepsFull(transcript(m.current()), m.ov.msg.Steps) {
		st := m.ov.msg.Steps[i]
		text = prettyJSON(text)
		from := len(m.ov.lines)
		if st.Result {
			add(hardWrap("→ "+strings.ReplaceAll(text, "\n", "\n  "), w, "  "), 'R', i)
		} else {
			head, rest, _ := strings.Cut(text, "\n")
			add(hardWrap(st.Tool+"\x00"+head, w, "  "), 'H', i)
			if rest != "" {
				add(hardWrap(rest, w, ""), 'B', i)
			}
		}
		if len(m.ov.lines)-from > longStep {
			add([]string{""}, 'S', i)
		}
	}
}

// longStep: a step longer than this gets a separator line after it.
const longStep = 8

// stepMessage moves to the previous / next message inside the full-text overlay by moving the right pane's highlight.
func (m *Model) stepMessage(delta int) tea.Cmd {
	before := m.chatCur
	m.moveChat(delta)
	if m.chatCur != before {
		return m.openMessage()
	}
	return nil
}

func prettyJSON(s string) string {
	t := strings.TrimSpace(s)
	if len(t) < 2 || (t[0] != '{' && t[0] != '[') || !json.Valid([]byte(t)) {
		return s
	}
	var buf bytes.Buffer
	if json.Indent(&buf, []byte(t), "", "  ") != nil {
		return s
	}
	return buf.String()
}

func hardWrap(s string, w int, indent string) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		limit, n := w, 0
		var cur strings.Builder
		for _, r := range para {
			rw := render.Width(string(r))
			if n+rw > limit && cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				cur.WriteString(indent)
				limit, n = w, render.Width(indent)
			}
			cur.WriteRune(r)
			n += rw
		}
		out = append(out, cur.String())
	}
	return out
}

func (m *Model) copyMessage() {
	msg, ok := m.currentMessage()
	if !ok {
		return
	}
	if err := clipboard.WriteAll(msg.Text); err != nil {
		m.flash(i18n.T("flash.clipboard_unavailable") + err.Error())
		return
	}
	m.flash(i18n.F("chat.copied", len([]rune(msg.Text))))
}

// highlight marks the hits in a line (found case-insensitively, sliced from the original).
func highlight(line, q string) string { return highlightWith(line, q, lipgloss.NewStyle(), hitSty) }

func highlightWith(line, q string, base, hit lipgloss.Style) string {
	var b strings.Builder
	low := strings.ToLower(line)
	for q != "" {
		i := strings.Index(low, q)
		if i < 0 {
			break
		}
		b.WriteString(base.Render(line[:i]))
		b.WriteString(hit.Render(line[i : i+len(q)]))
		line, low = line[i+len(q):], low[i+len(q):]
	}
	b.WriteString(base.Render(line))
	return b.String()
}
