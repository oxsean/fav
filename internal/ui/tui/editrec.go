package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

// e edits title, tags and summary in place: Tab moves between fields; Enter saves in title / tags, inserts a newline in summary
// (Ctrl+S saves); Esc discards. An unsaved session gets a record (not a favorite).

func (m *Model) openEdit() tea.Cmd {
	r := m.current()
	if r == nil {
		return nil
	}
	title := newInput()
	title.SetValue(r.Title)
	title.CharLimit = 120
	title.Prompt = ""
	tags := newInput()
	tags.SetValue(strings.Join(r.Tags, " "))
	tags.Placeholder = i18n.T("edit.tags_placeholder")
	tags.CharLimit = 200
	tags.Prompt = ""
	sum := newArea()
	sum.SetValue(r.Summary)
	sum.Placeholder = i18n.T("edit.summary_placeholder")
	sum.CharLimit = 4000
	sum.ShowLineNumbers = false
	sum.Prompt = ""
	sum.SetHeight(6)
	m.ov = overlay{kind: ovEdit, rec: r, edit: title, edit2: tags, area: sum, focus: -1}
	m.ov.edit.Focus()
	m.ov.edit.CursorEnd()
	return textinput.Blink
}

// editButtons: the field index past the three inputs; Tab walks title, tags, summary, then each button.
const editButtons = 3

func (m *Model) focusField(i int) tea.Cmd {
	stops := editButtons + len(m.ov.btns)
	i = (i + stops) % stops
	m.ov.edit.Blur()
	m.ov.edit2.Blur()
	m.ov.area.Blur()
	m.ov.field, m.ov.focus = min(i, editButtons), -1
	switch {
	case i == 0:
		m.ov.edit.Focus()
	case i == 1:
		m.ov.edit2.Focus()
	case i == 2:
		return m.ov.area.Focus()
	default:
		m.ov.focus = i - editButtons
		return nil
	}
	return textinput.Blink
}

func (m *Model) editStop() int {
	if m.ov.field == editButtons {
		return editButtons + max(0, m.ov.focus)
	}
	return m.ov.field
}

func (m *Model) editDirty() bool {
	r := m.ov.rec
	tags := fav.Normalize(strings.FieldsFunc(m.ov.edit2.Value(), func(r rune) bool { return r == ' ' || r == ',' || r == '，' }))
	return strings.TrimSpace(m.ov.edit.Value()) != r.Title || strings.Join(tags, " ") != strings.Join(r.Tags, " ") || strings.TrimSpace(m.ov.area.Value()) != r.Summary
}

func (m *Model) editKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if m.editDirty() && !m.ov.editing {
			m.ov.editing = true // reused as "warned once"
			m.flash(i18n.T("edit.esc_again"))
			return nil
		}
		m.closeOverlay()
		return nil
	case "ctrl+s":
		m.saveEdit()
		return nil
	case "enter":
		if m.ov.field != 2 {
			if !m.pressFocused() {
				m.saveEdit()
			}
			return nil
		}
	case "tab":
		return m.focusField(m.editStop() + 1)
	case "shift+tab":
		return m.focusField(m.editStop() - 1)
	case "up", "down":
		if m.ov.field != 2 {
			if msg.String() == "up" {
				return m.focusField(m.editStop() - 1)
			}
			return m.focusField(m.editStop() + 1)
		}
	case "left", "right":
		if m.ov.field == editButtons {
			m.moveFocus(map[string]int{"left": -1, "right": 1}[msg.String()])
			return nil
		}
	}
	if m.ov.field == editButtons {
		return nil
	}
	var cmd tea.Cmd
	switch m.ov.field {
	case 0:
		m.ov.edit, cmd = m.ov.edit.Update(msg)
	case 1:
		m.ov.edit2, cmd = m.ov.edit2.Update(msg)
	default:
		m.ov.area, cmd = m.ov.area.Update(msg)
	}
	return cmd
}

func (m *Model) saveEdit() {
	title := strings.TrimSpace(m.ov.edit.Value())
	tags := fav.Normalize(strings.FieldsFunc(m.ov.edit2.Value(), func(r rune) bool { return r == ' ' || r == ',' || r == '，' }))
	summary := strings.TrimSpace(m.ov.area.Value())
	r := m.ov.rec
	if title == "" {
		m.flash(i18n.T("edit.title_empty"))
		return
	}
	if title == r.Title && strings.Join(tags, " ") == strings.Join(r.Tags, " ") && summary == r.Summary {
		m.closeOverlay()
		return
	}
	m.closeOverlay()
	if !m.editRec(r, func(r *fav.Rec) { r.Title, r.Tags, r.Summary = title, tags, summary }) {
		return
	}
	m.flash(i18n.T("edit.saved") + render.Truncate(title, 40))
}

func (m *Model) renderEdit() string {
	w := m.ovWidth()
	inner := w - 4
	body := []string{boldSty.Foreground(cText).Render(i18n.T("edit.title")), dimmed.Render(i18n.T("edit.hint")), ""}
	field := func(label string, view string, i int) {
		sty := panelSty
		if m.ov.field == i {
			sty = panelSty.BorderForeground(cAccent)
		}
		lines := strings.Split(sty.Width(inner).Render(view), "\n")
		body = append(body, dimmed.Render(label))
		m.markRows(len(body), ovPad, inner, len(lines), func(mm *Model) {
			mm.pending = mm.focusField(i)
			switch i {
			case 0:
				mm.placeCursor(&mm.ov.edit, 2)
			case 1:
				mm.placeCursor(&mm.ov.edit2, 2)
			}
		})
		body = append(body, lines...)
		body = append(body, "")
	}
	m.ov.edit.SetWidth(inner - 4)
	m.ov.edit2.SetWidth(inner - 4)
	m.ov.area.SetWidth(inner - 4)
	field(i18n.T("edit.field_title"), inputView(m.ov.edit), 0)
	field(i18n.T("label.tags"), inputView(m.ov.edit2), 1)
	field(i18n.T("card.summary"), m.ov.area.View(), 2)
	body = append(body, m.buttons(len(body)+1, []btn{
		{keyed(keyName("ctrl+s"), i18n.T("edit.btn_save")), true, (*Model).saveEdit},
		cancelBtn(),
	})...)
	return ovRender(body, w)
}
