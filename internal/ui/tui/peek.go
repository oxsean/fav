package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/herdr"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

const (
	peekLines  = 80
	peekEvery  = time.Second
	peekArmFor = 5 * time.Second // a digit pressed once is forgotten after this
)

type peekMsg struct {
	pane, text string
	err        error
}

type peekTickMsg struct{ pane string }

type peekSentMsg struct {
	what string
	err  error
}

func peekRead(pane string) tea.Cmd {
	return func() tea.Msg {
		t, err := herdr.ReadAgent(pane, peekLines)
		return peekMsg{pane, t, err}
	}
}

// askPeek (v): the terminal of a session running in a Herdr pane, refreshed every second, with a line to answer it.
func (m *Model) askPeek(r *fav.Rec) {
	l, ok := m.liveOf(r)
	if !ok || l.PaneID == "" {
		m.flash(i18n.T("live.not_in_herdr"))
		return
	}
	ti := newInput()
	ti.Placeholder = i18n.T("peek.placeholder")
	ti.CharLimit = 4000
	m.ov = overlay{kind: ovPeek, rec: r, title: l.PaneID, edit: ti, focus: -1}
	m.pending = tea.Batch(m.pending, peekRead(l.PaneID))
}

func (m *Model) applyPeek(msg peekMsg) tea.Cmd {
	if m.ov.kind != ovPeek || m.ov.title != msg.pane {
		return nil
	}
	m.ov.hint = ""
	if msg.err != nil {
		m.ov.hint = msg.err.Error()
	} else {
		lines := strings.Split(strings.TrimRight(ansi.Strip(msg.text), "\n "), "\n")
		if strings.Join(lines, "\n") != strings.Join(m.ov.lines, "\n") {
			m.ov.armed = "" // the question it was meant for may be gone
		}
		m.ov.lines = lines
	}
	if m.ov.armed != "" && time.Since(m.ov.armedAt) > peekArmFor {
		m.ov.armed = ""
	}
	pane := msg.pane
	return tea.Tick(peekEvery, func(time.Time) tea.Msg { return peekTickMsg{pane} })
}

// sendPeek submits the typed line as the agent's next prompt.
func (m *Model) sendPeek() {
	text := strings.TrimSpace(m.ov.edit.Value())
	if text == "" {
		return
	}
	pane := m.ov.title
	m.ov.edit.SetValue("")
	m.pending = func() tea.Msg { return peekSentMsg{render.Truncate(text, 40), herdr.PromptAgent(pane, text)} }
}

// pressDigit answers a numbered question in the pane; the first press only arms it, the same digit again sends it.
func (m *Model) pressDigit(d string) {
	if m.ov.armed != d {
		m.ov.armed, m.ov.armedAt = d, time.Now()
		return
	}
	pane := m.ov.title
	m.ov.armed = ""
	m.pending = func() tea.Msg { return peekSentMsg{d, herdr.SendKeys(pane, d)} }
}

func (m *Model) peekSwitch() {
	r := m.ov.rec
	p, err := capture.PlanResume(r, m.live, false)
	if err != nil {
		m.flash(err.Error())
		return
	}
	m.runPlan(r, p, false)
}

func (m *Model) focusReply() {
	m.ov.armed = ""
	m.ov.focus = -1
	m.ov.edit.Focus()
}

func (m *Model) peekButtons() []btn {
	return []btn{
		{keyed(keyOf(inPeek, actEnter), i18n.T("peek.btn_switch")), true, (*Model).peekSwitch},
		{keyed(keyOf(inPeek, actReply), i18n.T("peek.btn_reply")), false, (*Model).focusReply},
		{keyed(keyOf(inPeek, actClose), i18n.T("btn.close")), false, (*Model).closeOverlay},
	}
}

func (m *Model) renderPeek() string {
	w := m.ovWidth()
	inner := w - 4
	r := m.ov.rec
	var body []string
	label, tone := m.needLabel(r.SessionID, m.live[r.SessionID])
	head := render.Truncate(i18n.F("peek.title", r.Title), inner-render.Width(label)-2)
	body = append(body, boldSty.Foreground(cText).Render(head)+strings.Repeat(" ", max(1, inner-render.Width(head)-render.Width(label)))+liveTone[tone].Render(label))
	body = append(body, dimmed.Render(render.Truncate(i18n.F("peek.source", m.ov.title), inner)))
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))

	room := max(3, m.h-17)
	lines := m.ov.lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	lines = lines[max(0, len(lines)-room):]
	for len(lines) < room {
		lines = append([]string{""}, lines...)
	}
	for _, l := range lines {
		body = append(body, fit(l, inner))
	}
	if m.ov.hint != "" {
		body = append(body, errSty.Render(render.Truncate(m.ov.hint, inner)))
	}
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))

	m.ov.edit.SetWidth(inner - 6)
	m.mark(len(body)+2, ovPad, inner, func(mm *Model) { mm.placeCursor(&mm.ov.edit, 2) })
	border := cFrame
	if m.ov.edit.Focused() {
		border = cAccent
	}
	body = append(body, strings.Split(panelSty.BorderForeground(border).Width(inner).Render(fit(inputView(m.ov.edit), inner-4)), "\n")...)
	switch {
	case m.ov.armed != "":
		body = append(body, warnSty.Render(render.Truncate(i18n.F("peek.armed", m.ov.armed, m.ov.armed), inner)))
	case m.ov.edit.Focused():
		body = append(body, dimmed.Render(i18n.T("peek.typing_hint")))
	default:
		body = append(body, dimmed.Render(i18n.T("peek.hint")))
	}
	body = append(body, m.buttons(len(body)+1, m.peekButtons())...)
	return ovRender(body, w)
}

func (m *Model) peekKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.ov.edit.Focused() {
		switch msg.String() {
		case "enter":
			m.sendPeek()
			return nil
		case "esc":
			m.ov.edit.Blur()
			return nil
		case "tab":
			m.ov.edit.Blur()
			m.ov.focus = 0
			return nil
		case "shift+tab":
			m.ov.edit.Blur()
			m.ov.focus = len(m.ov.btns) - 1
			return nil
		}
		var cmd tea.Cmd
		m.ov.edit, cmd = m.ov.edit.Update(msg)
		return cmd
	}
	k := msg.String()
	switch a := keyAct(inPeek, k); a {
	case actAnswer:
		m.pressDigit(k)
	case actReply:
		m.focusReply()
	case actTabNext:
		m.peekTab(1)
	case actTabPrev:
		m.peekTab(-1)
	case actClose:
		if m.ov.armed != "" {
			m.ov.armed = ""
			return nil
		}
		m.closeOverlay()
	default:
		m.dialogKey(a, m.peekSwitch)
	}
	return nil
}

// peekTab: Tab walks the reply input, then each button, then back to the input.
func (m *Model) peekTab(dir int) {
	m.ov.armed = ""
	n := len(m.ov.btns)
	switch f := m.ov.focus + dir; {
	case m.ov.focus < 0 && dir < 0:
		m.ov.focus = n - 1
	case m.ov.focus < 0:
		m.ov.focus = 0
	case f < 0 || f >= n:
		m.ov.focus = -1
		m.ov.edit.Focus()
	default:
		m.ov.focus = f
	}
}
