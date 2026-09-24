package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
)

// The task form (w new, e edit): title, directory, machine, agent, brief. Tab walks the fields and then the buttons;
// ← / → change a choice; Enter saves outside the brief, where it starts a new line (Ctrl+S saves); Esc discards.
const (
	formTitle = iota
	formDir
	formMachine
	formAgent
	formBrief
	formButtons
)

// The run dialog: machine and agent, then the buttons.
const (
	runMachine = iota
	runAgent
	runButtons
)

func choice(opts []string, want string) int {
	if i := slices.Index(opts, want); i >= 0 {
		return i
	}
	return 0
}

func (m *Model) openTaskForm(x *task.Task) tea.Cmd {
	if m.tasks.cl == nil {
		m.flash(i18n.T("tasks.unavailable"))
		return nil
	}
	title := newInput()
	title.CharLimit = 200
	title.Prompt = ""
	title.Placeholder = i18n.T("tasks.title_placeholder")
	dir := newInput()
	dir.CharLimit = 1000
	dir.Prompt = ""
	brief := newArea()
	brief.CharLimit = 20000
	brief.ShowLineNumbers = false
	brief.Prompt = ""
	brief.Placeholder = i18n.T("tasks.brief_placeholder")
	brief.SetHeight(6)
	machines, agents := m.machineNames(), m.agentNames()
	ov := overlay{kind: ovTaskForm, focus: -1, opts: [][]string{machines, agents}}
	if x != nil {
		ov.taskID = x.ID
		title.SetValue(x.Title)
		dir.SetValue(x.Dir)
		brief.SetValue(x.Brief)
		ov.pick = []int{choice(machines, firstNonEmpty(x.Machine, coord.Local)), choice(agents, x.Agent)}
	} else {
		dir.SetValue(m.startDir)
		ov.pick = []int{choice(machines, coord.Local), choice(agents, "claude")}
	}
	ov.edit, ov.edit2, ov.area = title, dir, brief
	m.ov = ov
	m.ov.edit.Focus()
	m.ov.edit.CursorEnd()
	return textinput.Blink
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func (m *Model) pickNext(i, d int) {
	if i < len(m.ov.opts) && len(m.ov.opts[i]) > 0 {
		n := len(m.ov.opts[i])
		m.ov.pick[i] = (m.ov.pick[i] + d + n) % n
	}
}

func (m *Model) picked(i int) string {
	if i < len(m.ov.opts) && m.ov.pick[i] < len(m.ov.opts[i]) {
		return m.ov.opts[i][m.ov.pick[i]]
	}
	return ""
}

// formField focuses stop i of the form: a field, or a button past formButtons.
func (m *Model) formField(i int) tea.Cmd {
	stops := formButtons + len(m.ov.btns)
	i = (i + stops) % stops
	m.ov.edit.Blur()
	m.ov.edit2.Blur()
	m.ov.area.Blur()
	m.ov.field, m.ov.focus = min(i, formButtons), -1
	switch i {
	case formTitle:
		m.ov.edit.Focus()
	case formDir:
		m.ov.edit2.Focus()
	case formBrief:
		return m.ov.area.Focus()
	case formMachine, formAgent:
		return nil
	default:
		m.ov.focus = i - formButtons
		return nil
	}
	return textinput.Blink
}

func (m *Model) formStop() int {
	if m.ov.field == formButtons {
		return formButtons + max(0, m.ov.focus)
	}
	return m.ov.field
}

func (m *Model) taskFormKey(msg tea.KeyPressMsg) tea.Cmd {
	f := m.ov.field
	switch k := msg.String(); k {
	case "esc":
		m.closeOverlay()
		return nil
	case "ctrl+s":
		return m.saveTaskForm()
	case "enter":
		if f != formBrief {
			if !m.pressFocused() {
				return m.saveTaskForm()
			}
			return nil
		}
	case "tab":
		return m.formField(m.formStop() + 1)
	case "shift+tab":
		return m.formField(m.formStop() - 1)
	case "up", "down":
		if f != formBrief {
			return m.formField(m.formStop() + map[string]int{"up": -1, "down": 1}[k])
		}
	case "left", "right", "h", "l":
		d := map[string]int{"left": -1, "h": -1, "right": 1, "l": 1}[k]
		switch f {
		case formMachine, formAgent:
			m.pickNext(f-formMachine, d)
			return nil
		case formButtons:
			m.moveFocus(d)
			return nil
		}
	case "space":
		if f == formMachine || f == formAgent {
			m.pickNext(f-formMachine, 1)
			return nil
		}
	}
	var cmd tea.Cmd
	switch f {
	case formTitle:
		m.ov.edit, cmd = m.ov.edit.Update(msg)
	case formDir:
		m.ov.edit2, cmd = m.ov.edit2.Update(msg)
	case formBrief:
		m.ov.area, cmd = m.ov.area.Update(msg)
	}
	return cmd
}

func (m *Model) saveTaskForm() tea.Cmd {
	title := strings.TrimSpace(m.ov.edit.Value())
	if title == "" {
		m.flash(i18n.T("edit.title_empty"))
		return nil
	}
	dir := strings.TrimSpace(m.ov.edit2.Value())
	machine, agentName := m.picked(0), m.picked(1)
	brief := strings.TrimSpace(m.ov.area.Value())
	id := m.ov.taskID
	m.closeOverlay()
	if machine == coord.Local {
		machine = ""
	}
	if id == "" {
		return m.write(coord.MTaskCreate, coord.TaskCreate{Title: title, Brief: brief, Dir: dir, Machine: machine, Agent: agentName},
			i18n.F("tasks.created", render.Truncate(title, 40)), func(mm *Model) tea.Cmd {
				mm.tasks.cursor = 0 // newest first
				return nil
			})
	}
	return m.write(coord.MTaskEdit, task.TaskEdit{ID: id, Title: &title, Brief: &brief, Dir: &dir, Machine: &machine, Agent: &agentName},
		i18n.F("edit.saved", render.Truncate(title, 40)), nil)
}

// selector draws a choice as "< value >"; a click moves to the next value.
func (m *Model) selector(body *[]string, label string, field, i, inner int, focused bool) {
	v := m.picked(i)
	sty := dimmed
	if focused {
		sty = accent.Bold(true)
	}
	*body = append(*body, dimmed.Render(label))
	m.mark(len(*body)+1, ovPad, inner, func(mm *Model) {
		if mm.ov.kind == ovTaskForm {
			mm.pending = mm.formField(field)
		} else {
			mm.ov.field, mm.ov.focus = field, -1
		}
		mm.pickNext(i, 1)
	})
	*body = append(*body, sty.Render("<")+" "+render.Truncate(v, inner-6)+" "+sty.Render(">"))
	*body = append(*body, "")
}

func (m *Model) renderTaskForm() string {
	w := m.ovWidth()
	inner := w - 4
	head := i18n.T("tasks.new_title")
	if m.ov.taskID != "" {
		head = i18n.T("tasks.edit_title")
	}
	body := []string{boldSty.Foreground(cText).Render(head), dimmed.Render(i18n.T("tasks.form_hint")), ""}
	field := func(label, view string, i int, input *textinput.Model) {
		sty := panelSty
		if m.ov.field == i {
			sty = panelSty.BorderForeground(cAccent)
		}
		lines := strings.Split(sty.Width(inner).Render(view), "\n")
		body = append(body, dimmed.Render(label))
		m.markRows(len(body)+1, ovPad, inner, len(lines), func(mm *Model) {
			mm.pending = mm.formField(i)
			if input != nil {
				if i == formTitle {
					mm.placeCursor(&mm.ov.edit, 2)
				} else {
					mm.placeCursor(&mm.ov.edit2, 2)
				}
			}
		})
		body = append(body, lines...)
		body = append(body, "")
	}
	m.ov.edit.SetWidth(inner - 4)
	m.ov.edit2.SetWidth(inner - 4)
	m.ov.area.SetWidth(inner - 4)
	field(i18n.T("tasks.field_title"), inputView(m.ov.edit), formTitle, &m.ov.edit)
	field(i18n.T("tasks.field_dir"), inputView(m.ov.edit2), formDir, &m.ov.edit2)
	m.selector(&body, i18n.T("tasks.field_machine"), formMachine, 0, inner, m.ov.field == formMachine)
	m.selector(&body, i18n.T("tasks.field_agent"), formAgent, 1, inner, m.ov.field == formAgent)
	field(i18n.T("tasks.field_brief"), m.ov.area.View(), formBrief, nil)
	body = append(body, m.buttons(len(body)+1, []btn{
		{keyed(keyName("ctrl+s"), i18n.T("edit.btn_save")), true, func(mm *Model) { mm.pending = mm.saveTaskForm() }},
		cancelBtn(),
	})...)
	return ovRender(body, w)
}

func (m *Model) openRunDialog() {
	x := m.selectedTask()
	if x == nil {
		return
	}
	machines, agents := m.machineNames(), m.agentNames()
	m.ov = overlay{kind: ovTaskRun, focus: -1, taskID: x.ID, opts: [][]string{machines, agents},
		pick: []int{choice(machines, firstNonEmpty(x.Machine, coord.Local)), choice(agents, firstNonEmpty(x.Agent, "claude"))}}
}

func (m *Model) runTask() tea.Cmd {
	id, machine, agentName := m.ov.taskID, m.picked(0), m.picked(1)
	m.closeOverlay()
	return m.write(coord.MRunDispatch, coord.Dispatch{Task: id, Machine: machine, Agent: agentName},
		i18n.F("tasks.queued", machine, agentName), nil)
}

func (m *Model) taskRunKey(msg tea.KeyPressMsg) tea.Cmd {
	f := m.ov.field
	switch k := msg.String(); k {
	case "esc", "q":
		m.closeOverlay()
	case "enter":
		if !m.pressFocused() {
			return m.runTask()
		}
	case "tab", "down", "j":
		m.runStop(1)
	case "shift+tab", "up", "k":
		m.runStop(-1)
	case "left", "right", "h", "l", "space":
		d := map[string]int{"left": -1, "h": -1}[k]
		if d == 0 {
			d = 1
		}
		if f < runButtons {
			m.pickNext(f, d)
		} else if k != "space" {
			m.moveFocus(d)
		}
	}
	return nil
}

// runStop moves among the two choices and the buttons.
func (m *Model) runStop(d int) {
	stops := runButtons + len(m.ov.btns)
	cur := m.ov.field
	if cur == runButtons {
		cur += max(0, m.ov.focus)
	}
	i := (cur + d + stops) % stops
	m.ov.field, m.ov.focus = min(i, runButtons), -1
	if i >= runButtons {
		m.ov.focus = i - runButtons
	}
}

func (m *Model) renderTaskRun() string {
	w := m.ovWidth()
	inner := w - 4
	x := m.selectedTask()
	title := ""
	if x != nil {
		title = x.Title
	}
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(i18n.F("tasks.run_title", title), inner)),
		dimmed.Render(i18n.T("tasks.run_hint")), ""}
	m.selector(&body, i18n.T("tasks.field_machine"), runMachine, 0, inner, m.ov.field == runMachine)
	m.selector(&body, i18n.T("tasks.field_agent"), runAgent, 1, inner, m.ov.field == runAgent)
	if x != nil && x.Dir != "" {
		body = append(body, dimmed.Render(render.Truncate(i18n.F("tasks.run_dir", x.Dir), inner)), "")
	}
	body = append(body, m.buttons(len(body)+1, []btn{
		{keyed(enterKey, i18n.T("tasks.btn_run")), true, func(mm *Model) { mm.pending = mm.runTask() }},
		cancelBtn(),
	})...)
	return ovRender(body, w)
}
