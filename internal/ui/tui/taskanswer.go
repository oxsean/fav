package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
)

// The answer dialog: what a running run waits on — a tool to allow or deny, or questions answered by picking an
// option (← → change it). Tab moves among the questions and the buttons; Enter does what the focused button says,
// else the primary one.

// waitsOn is r's first request, nil when it waits on nothing.
func waitsOn(r *task.Run) *agent.Request {
	if r == nil || !task.Open(r.State) || len(r.Requests) == 0 {
		return nil
	}
	return &r.Requests[0]
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
		a.Answers = map[string]string{}
		for i, x := range q.Questions {
			a.Answers[x.Question] = m.picked(i)
		}
	}
	run := m.ov.taskID
	m.closeOverlay()
	return m.write(coord.MRunAnswer, coord.Answer{Run: run, Answer: a}, i18n.F("tasks.answered", run), nil)
}

func (m *Model) answerKey(msg tea.KeyPressMsg) tea.Cmd {
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
		if m.ov.field >= 0 && m.ov.field < len(m.ov.opts) {
			m.pickNext(m.ov.field, d)
		} else {
			m.moveFocus(d)
		}
	}
	return nil
}

// answerStop moves among the questions and the buttons.
func (m *Model) answerStop(d int) {
	qs := len(m.ov.opts)
	stops := qs + len(m.ov.btns)
	cur := m.ov.field
	if cur < 0 || cur >= qs {
		cur = qs + max(0, m.ov.focus)
	}
	i := (cur + d + stops) % stops
	m.ov.field, m.ov.focus = i, -1
	if i >= qs {
		m.ov.field, m.ov.focus = -1, i-qs
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
