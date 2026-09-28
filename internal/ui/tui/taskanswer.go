package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
)

// The answer dialog: what a running run waits on — a tool to allow or deny, or questions answered by picking an
// option (digits 1-9 or ← → change it, a single question also takes an "other" answer in its own words). Tab moves
// among the questions and the buttons; Enter does what the focused button says, else the primary one; a reason
// typed into its field goes along with a deny.

// waitsOn is r's first request, nil when it waits on nothing.
func waitsOn(r *task.Run) *agent.Request {
	if r == nil || !task.Open(r.State) || len(r.Requests) == 0 {
		return nil
	}
	return &r.Requests[0]
}

// answerOther: q takes a free-text answer instead of picking, offered only when it is exactly one question.
func answerOther(q *agent.Request) bool {
	return q != nil && q.Kind == agent.RequestQuestion && len(q.Questions) == 1
}

// answerStops is q's field stops past the question selectors: an "other" answer for a single question, then always
// a reason (why it declined to answer, or why it denied).
func answerStops(q *agent.Request) (otherAt, reasonAt int) {
	qs := 0
	if q != nil {
		qs = len(q.Questions)
	}
	if answerOther(q) {
		return qs, qs + 1
	}
	return -1, qs
}

func (m *Model) openAnswer() {
	r := m.selectedRun()
	q := waitsOn(r)
	if q == nil {
		return
	}
	ov := overlay{kind: ovTaskAnswer, focus: -1, taskID: r.ID, request: q.ID}
	for _, x := range q.Questions {
		ov.opts = append(ov.opts, x.Options)
		ov.pick = append(ov.pick, 0)
	}
	if len(ov.opts) == 0 {
		ov.field = -1 // only buttons
	}
	other := newInput()
	other.Placeholder = i18n.T("tasks.answer_other_hint")
	reason := newInput()
	reason.Placeholder = i18n.T("tasks.answer_reason_hint")
	ov.edit, ov.edit2 = other, reason
	m.ov = ov
}

// answerRequest is the request the dialog answers, nil once the run no longer waits on it.
func (m *Model) answerRequest() *agent.Request {
	if m.tasks.st == nil {
		return nil
	}
	r := m.tasks.st.Runs[m.ov.taskID]
	if r == nil || !task.Open(r.State) {
		return nil
	}
	if i := slices.IndexFunc(r.Requests, func(q agent.Request) bool { return q.ID == m.ov.request }); i >= 0 {
		return &r.Requests[i]
	}
	return nil
}

func (m *Model) sendAnswer(allow bool) tea.Cmd {
	q := m.answerRequest()
	if q == nil {
		m.closeOverlay()
		return nil
	}
	a := agent.Answer{Request: q.ID, Allow: allow}
	if allow && q.Kind == agent.RequestQuestion {
		other, _ := answerStops(q)
		a.Answers = map[string]string{}
		for i, x := range q.Questions {
			ans := m.picked(i)
			if i == 0 && other >= 0 {
				if s := strings.TrimSpace(m.ov.edit.Value()); s != "" {
					ans = s
				}
			}
			a.Answers[x.Question] = ans
		}
	}
	if !allow {
		a.Message = strings.TrimSpace(m.ov.edit2.Value())
	}
	run := m.ov.taskID
	m.closeOverlay()
	return m.write(coord.MRunAnswer, coord.Answer{Run: run, Answer: a}, i18n.F("tasks.answered", run), nil)
}

func (m *Model) answerKey(msg tea.KeyPressMsg) tea.Cmd {
	q := m.answerRequest()
	qs := len(m.ov.opts)
	otherAt, reasonAt := answerStops(q)
	// ⚠️ a field's own text takes every key but esc, tab and enter: inTaskRun's letter bindings (j, k, q) would
	// otherwise swallow what the user types into the "other" answer or the deny reason.
	if m.ov.field == otherAt || m.ov.field == reasonAt {
		switch msg.String() {
		case "esc":
			m.closeOverlay()
		case "tab", "down":
			m.answerStop(1)
		case "shift+tab", "up":
			m.answerStop(-1)
		case "enter":
			if m.ov.field == reasonAt {
				return m.sendAnswer(false)
			}
			return m.sendAnswer(true)
		default:
			var cmd tea.Cmd
			if m.ov.field == otherAt {
				m.ov.edit, cmd = m.ov.edit.Update(msg)
			} else {
				m.ov.edit2, cmd = m.ov.edit2.Update(msg)
			}
			return cmd
		}
		return nil
	}
	switch a := keyAct(inTaskRun, msg.String()); a {
	case actClose:
		m.closeOverlay()
	case actEnter:
		if !m.pressFocused() {
			return m.sendAnswer(true)
		}
	case actFocusNext:
		m.answerStop(1)
	case actFocusPrev:
		m.answerStop(-1)
	case actLeft, actRight:
		d := map[act]int{actLeft: -1, actRight: 1}[a]
		if m.ov.field >= 0 && m.ov.field < qs {
			m.pickNext(m.ov.field, d)
		} else {
			m.moveFocus(d)
		}
	default:
		if m.ov.field >= 0 && m.ov.field < qs {
			if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
				if i := int(s[0] - '1'); i < len(m.ov.opts[m.ov.field]) {
					m.ov.pick[m.ov.field] = i
				}
			}
		}
	}
	return nil
}

// answerStop moves among the questions, the "other" and reason fields, and the buttons.
func (m *Model) answerStop(d int) {
	q := m.answerRequest()
	qs := len(m.ov.opts)
	otherAt, reasonAt := answerStops(q)
	extra := reasonAt - qs + 1
	stops := qs + extra + len(m.ov.btns)
	if stops == 0 {
		return
	}
	cur := m.ov.field
	if cur < 0 || cur >= qs+extra {
		cur = qs + extra + max(0, m.ov.focus)
	}
	i := (cur + d + stops) % stops
	m.ov.edit.Blur()
	m.ov.edit2.Blur()
	m.ov.field, m.ov.focus = i, -1
	if i >= qs+extra {
		m.ov.field, m.ov.focus = -1, i-qs-extra
		return
	}
	switch i {
	case otherAt:
		m.ov.edit.Focus()
	case reasonAt:
		m.ov.edit2.Focus()
	}
}

func (m *Model) renderAnswer() string {
	w := m.ovWidth()
	inner := w - 4
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(i18n.F("tasks.answer_title", m.ov.taskID), inner))}
	q := m.answerRequest()
	if q == nil {
		body = append(body, "", dimmed.Render(render.Truncate(i18n.T("tasks.answer_gone"), inner)), "")
		body = append(body, m.buttons(len(body)+1, []btn{cancelBtn()})...)
		return ovRender(body, w)
	}
	otherAt, reasonAt := answerStops(q)
	m.ov.edit.SetWidth(inner - 4)
	m.ov.edit2.SetWidth(inner - 4)
	field := func(label string, ti textinput.Model, i int, isOther bool) {
		body = append(body, dimmed.Render(label))
		m.markRows(len(body)+1, ovPad, inner, 1, func(mm *Model) {
			mm.ov.field, mm.ov.focus = i, -1
			if isOther {
				mm.placeCursor(&mm.ov.edit, 2)
			} else {
				mm.placeCursor(&mm.ov.edit2, 2)
			}
		})
		body = append(body, inputView(ti))
		body = append(body, "")
	}
	body = append(body, dimmed.Render(render.Truncate(i18n.T("tasks.answer_hint"), inner)), "")
	if q.Kind == agent.RequestQuestion {
		for i, x := range q.Questions {
			label := x.Question
			if x.Header != "" {
				label = x.Header + " · " + x.Question
			}
			for _, l := range render.Wrap(render.Sanitize(label), inner) {
				body = append(body, accent.Render(l))
			}
			m.selector(&body, "", i, i, inner, m.ov.field == i)
		}
		if otherAt >= 0 {
			field(i18n.T("tasks.answer_other"), m.ov.edit, otherAt, true)
		}
		field(i18n.T("tasks.answer_reason_decline"), m.ov.edit2, reasonAt, false)
		body = append(body, m.buttons(len(body)+1, []btn{
			{keyed(enterKey, i18n.T("tasks.btn_answer")), true, func(mm *Model) { mm.pending = mm.sendAnswer(true) }},
			{i18n.T("tasks.btn_no_answer"), false, func(mm *Model) { mm.pending = mm.sendAnswer(false) }},
			cancelBtn(),
		})...)
		return ovRender(body, w)
	}
	body = append(body, accent.Render(render.Truncate(i18n.F("tasks.answer_tool", q.Tool), inner)))
	lines := render.Wrap(render.Sanitize(q.Summary), inner)
	if len(lines) > 8 {
		lines = append(lines[:7], dimmed.Render(i18n.F("tasks.more_lines", len(lines)-7)))
	}
	body = append(append(body, lines...), "")
	field(i18n.T("tasks.answer_reason_deny"), m.ov.edit2, reasonAt, false)
	body = append(body, m.buttons(len(body)+1, []btn{
		{keyed(enterKey, i18n.T("tasks.btn_allow")), true, func(mm *Model) { mm.pending = mm.sendAnswer(true) }},
		{i18n.T("tasks.btn_deny"), false, func(mm *Model) { mm.pending = mm.sendAnswer(false) }},
		cancelBtn(),
	})...)
	return ovRender(body, w)
}

// requestLines: what r waits on, one line each.
func requestLines(r *task.Run, inner int) []string {
	var out []string
	for _, q := range r.Requests {
		text := i18n.F("tasks.waits_tool", q.Tool, render.Sanitize(q.Summary))
		if q.Kind == agent.RequestQuestion && len(q.Questions) > 0 {
			text = i18n.F("tasks.waits_question", render.Sanitize(q.Questions[0].Question))
		}
		out = append(out, accent.Render(render.Truncate(text, inner)))
	}
	for i := max(0, len(r.Sends)-2); i < len(r.Sends); i++ {
		s := r.Sends[i]
		out = append(out, dimmed.Render(render.Truncate(i18n.F("tasks.sent_line", render.SendState(s.State), render.Sanitize(s.Text)), inner)))
	}
	return out
}
