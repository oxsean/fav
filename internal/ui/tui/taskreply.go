package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
)

// The reply dialog: text for a run's session, sent as a new run that continues it. Tab moves between the text and
// the buttons; Enter starts a new line in the text (Ctrl+S sends); Esc discards.
const (
	replyText = iota
	replyButtons
)

// canReply: r's session can go on in the background with a reply.
func canReply(r *task.Run) bool {
	return r != nil && r.Session != "" && !task.Open(r.State)
}

func (m *Model) openReply() {
	r := m.selectedRun()
	if !canReply(r) {
		return
	}
	area := newArea()
	area.CharLimit = 256 << 10
	area.ShowLineNumbers = false
	area.Prompt = ""
	area.Placeholder = i18n.T("tasks.reply_placeholder")
	area.SetHeight(5)
	m.ov = overlay{kind: ovTaskReply, focus: -1, taskID: r.ID, area: area}
	m.pending = m.ov.area.Focus()
}

func (m *Model) replyField(i int) tea.Cmd {
	stops := replyButtons + len(m.ov.btns)
	i = (i + stops) % stops
	m.ov.area.Blur()
	m.ov.field, m.ov.focus = min(i, replyButtons), -1
	if i == replyText {
		return m.ov.area.Focus()
	}
	m.ov.focus = i - replyButtons
	return nil
}

func (m *Model) replyKey(msg tea.KeyPressMsg) tea.Cmd {
	stop := m.ov.field
	if stop == replyButtons {
		stop += max(0, m.ov.focus)
	}
	switch a := keyAct(inTaskForm, msg.String()); a {
	case actClose:
		m.closeOverlay()
		return nil
	case actSave:
		return m.sendReply()
	case actEnter:
		if m.ov.field != replyText {
			if !m.pressFocused() {
				return m.sendReply()
			}
			return nil
		}
	case actFocusNext:
		return m.replyField(stop + 1)
	case actFocusPrev:
		return m.replyField(stop - 1)
	case actLeft, actRight:
		if m.ov.field == replyButtons {
			m.moveFocus(map[act]int{actLeft: -1, actRight: 1}[a])
			return nil
		}
	}
	if m.ov.field != replyText {
		return nil
	}
	var cmd tea.Cmd
	m.ov.area, cmd = m.ov.area.Update(msg)
	return cmd
}

func (m *Model) sendReply() tea.Cmd {
	text := strings.TrimSpace(m.ov.area.Value())
	if text == "" {
		m.flash(i18n.T("tasks.reply_empty"))
		return nil
	}
	id := m.ov.taskID
	m.closeOverlay()
	return m.write(coord.MRunContinue, coord.Continue{Run: id, Text: text}, i18n.F("tasks.replied", id), nil)
}

func (m *Model) renderReply() string {
	w := m.ovWidth()
	inner := w - 4
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(i18n.F("tasks.reply_title", m.ov.taskID), inner)),
		dimmed.Render(render.Truncate(i18n.T("tasks.reply_hint"), inner)), ""}
	if r := m.tasks.st.Runs[m.ov.taskID]; r != nil && r.Ask != "" {
		lines := render.Wrap(render.Sanitize(r.Ask), inner)
		if len(lines) > 6 {
			lines = append(lines[:5], dimmed.Render(i18n.F("tasks.more_lines", len(lines)-5)))
		}
		body = append(append(body, accent.Render(i18n.T("tasks.reply_question"))), lines...)
		body = append(body, "")
	}
	sty := panelSty
	if m.ov.field == replyText {
		sty = panelSty.BorderForeground(cAccent)
	}
	m.ov.area.SetWidth(inner - 4)
	lines := strings.Split(sty.Width(inner).Render(m.ov.area.View()), "\n")
	for row := range lines {
		m.mark(len(body)+1+row, ovPad, inner, func(mm *Model) {
			mm.pending = mm.replyField(replyText)
			mm.placeAreaCursor(row-1, 2)
		})
	}
	body = append(body, lines...)
	body = append(body, "")
	body = append(body, m.buttons(len(body)+1, []btn{
		{keyed(keyOf(inTaskForm, actSave), i18n.T("tasks.btn_send")), true, func(mm *Model) { mm.pending = mm.sendReply() }},
		cancelBtn(),
	})...)
	return ovRender(body, w)
}

// runPreviewMsg: run.preview answered for the run dialog's choice key.
type runPreviewMsg struct {
	key string
	pv  coord.Preview
	err error
}

func (msg runPreviewMsg) apply(m *Model) tea.Cmd {
	if m.ov.kind == ovTaskRun && m.previewKey() == msg.key {
		m.ov.preview, m.ov.previewErr = &msg.pv, msg.err
	}
	return nil
}

func (m *Model) previewKey() string { return m.ov.taskID + "\x00" + m.picked(0) + "\x00" + m.picked(1) }

// previewRun asks the coordinator how running the dialog's choice would go.
func (m *Model) previewRun() tea.Cmd {
	cl := m.tasks.cl
	if cl == nil || m.ov.kind != ovTaskRun {
		return nil
	}
	m.ov.preview, m.ov.previewErr = nil, nil
	key, p := m.previewKey(), coord.Dispatch{Task: m.ov.taskID, Machine: m.picked(0), Agent: m.picked(1)}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		var pv coord.Preview
		err := cl.Call(ctx, coord.MRunPreview, p, &pv)
		return runPreviewMsg{key: key, pv: pv, err: err}
	}
}

// previewLines: what the run dialog says about how the run would go.
func (m *Model) previewLines(inner int) []string {
	pv := m.ov.preview
	switch {
	case m.ov.previewErr != nil:
		return []string{errSty.Render(render.Truncate(reasonText(m.ov.previewErr), inner))}
	case pv == nil:
		return []string{dimmed.Render(i18n.T("tasks.previewing"))}
	}
	var out []string
	if pv.Check != nil {
		out = append(out, dimmed.Render(render.Truncate(i18n.F("tasks.preview_check", pv.Provider, orDash(pv.Check.Version)), inner)))
	}
	for _, w := range pv.Blockers {
		for _, l := range render.Wrap(render.Why(w.Code, w.Detail), inner-2) {
			out = append(out, errSty.Render(render.GlyphWarn+" ")+l)
		}
	}
	for _, w := range pv.Notes {
		out = append(out, dimmed.Render(render.Truncate("- "+render.Why(w.Code, w.Detail), inner)))
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
