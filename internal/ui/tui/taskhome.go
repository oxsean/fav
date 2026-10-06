package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
)

// The home layout answers in place: the first waiting task that asks something is expanded under its row (list
// index 0, since NeedsYou already sorts what blocks its agent first). Digits 1-9 pick an option, Enter sends; a
// permission ask offers allow on Enter and n for deny, which can carry a reason.

// homeAsk is the run expanded under the home layout's first row, nil when nothing there asks anything.
func (m *Model) homeAsk() *task.Run {
	if m.tasks.layout != layoutHome || m.tasks.st == nil {
		return nil
	}
	for _, r := range m.tasks.st.NeedsYou() {
		if waitsOn(r) != nil {
			return r
		}
	}
	return nil
}

// homeAskLines draws what homeAsk's run waits on, indented under its row.
func (m *Model) homeAskLines(w int) []string {
	r := m.homeAsk()
	if r == nil {
		return nil
	}
	q := waitsOn(r)
	const indent = "    "
	if q.Kind == agent.RequestQuestion && len(q.Questions) > 0 {
		x := q.Questions[0]
		label := render.OneLine(x.Question)
		if x.Header != "" {
			label = render.OneLine(x.Header) + " · " + label
		}
		m.tasks.askPick = max(0, min(m.tasks.askPick, len(x.Options)-1))
		var opts []string
		for i, o := range x.Options {
			text := strconv.Itoa(i+1) + ") " + render.OneLine(o)
			if i == m.tasks.askPick {
				opts = append(opts, selTitle.Render(text))
			} else {
				opts = append(opts, dimmed.Render(text))
			}
		}
		lines := []string{
			accent.Render(render.Truncate(indent+label, w)),
			render.Truncate(indent+strings.Join(opts, "   "), w),
		}
		if says := optionSays(x, m.tasks.askPick); says != "" {
			lines = append(lines, dimmed.Render(render.Truncate(indent+render.OneLine(says), w)))
		}
		return append(lines, dimmed.Render(render.Truncate(indent+i18n.T("tasks.home_ask_hint"), w)))
	}
	lines := []string{
		accent.Render(render.Truncate(indent+i18n.F("tasks.answer_tool", render.OneLine(q.Tool)), w)),
		dimmed.Render(render.Truncate(indent+render.OneLine(q.Summary), w)),
	}
	if m.tasks.askDeny {
		lines = append(lines, indent+i18n.T("tasks.home_deny_hint")+" "+inputView(m.tasks.askReason))
	} else {
		lines = append(lines, dimmed.Render(render.Truncate(indent+i18n.T("tasks.home_permission_hint"), w)))
	}
	return lines
}

// homeAskKey handles the home layout's inline answer when the cursor sits on its row; ok is false for keys it
// leaves to the common list handling.
func (m *Model) homeAskKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	r := m.homeAsk()
	if r == nil || m.tasks.cursor != 0 {
		return nil, false
	}
	q := waitsOn(r)
	s := msg.String()
	switch {
	case len(s) == 1 && s[0] >= '1' && s[0] <= '9' && q.Kind == agent.RequestQuestion && len(q.Questions) > 0:
		if i := int(s[0] - '1'); i < len(q.Questions[0].Options) {
			m.tasks.askPick = i
		}
		return nil, true
	case s == "n" && q.Kind == agent.RequestPermission && !m.tasks.askDeny:
		m.tasks.askDeny = true
		ti := newInput()
		ti.Placeholder = i18n.T("tasks.answer_reason_hint")
		ti.Focus()
		m.tasks.askReason = ti
		return nil, true
	case s == "enter":
		return m.sendHomeAnswer(r, q, true), true
	}
	return nil, false
}

// homeDenying: the inline permission ask is composing a deny reason; its keys go to that field until Enter sends
// or Esc cancels.
func (m *Model) homeDenying() bool { return m.view == viewTasks && m.tasks.askDeny }

func (m *Model) homeDenyKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.tasks.askDeny = false
		return nil
	case "enter":
		r := m.homeAsk()
		m.tasks.askDeny = false
		if r == nil {
			return nil
		}
		return m.sendHomeAnswer(r, waitsOn(r), false)
	}
	var cmd tea.Cmd
	m.tasks.askReason, cmd = m.tasks.askReason.Update(msg)
	return cmd
}

// sendHomeAnswer answers the home layout's inline request: the highlighted option (or the primary option, allowed),
// or a denial with whatever reason was typed.
func (m *Model) sendHomeAnswer(r *task.Run, q *agent.Request, allow bool) tea.Cmd {
	a := agent.Answer{Request: q.ID, Allow: allow}
	switch {
	case allow && q.Kind == agent.RequestQuestion && len(q.Questions) > 0:
		x := q.Questions[0]
		pick := max(0, min(m.tasks.askPick, len(x.Options)-1))
		ans := ""
		if pick < len(x.Options) {
			ans = x.Options[pick]
		}
		a.Answers = map[string]string{x.Question: ans}
	case !allow:
		a.Message = strings.TrimSpace(m.tasks.askReason.Value())
	}
	m.tasks.askPick, m.tasks.askDeny = 0, false
	return m.write(coord.MRunAnswer, coord.Answer{Run: r.ID, Answer: a}, i18n.F("tasks.answered", r.ID), nil)
}
