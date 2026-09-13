package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

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
	title := textinput.New()
	title.SetValue(r.Title)
	title.CharLimit = 120
	title.Prompt = ""
	tags := textinput.New()
	tags.SetValue(strings.Join(r.Tags, " "))
	tags.Placeholder = i18n.T("edit.tags_placeholder")
	tags.CharLimit = 200
	tags.Prompt = ""
	sum := textarea.New()
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

func (m *Model) focusField(i int) tea.Cmd {
	m.ov.field = (i + 3) % 3
	m.ov.edit.Blur()
	m.ov.edit2.Blur()
	m.ov.area.Blur()
	switch m.ov.field {
	case 0:
		m.ov.edit.Focus()
	case 1:
		m.ov.edit2.Focus()
	default:
		return m.ov.area.Focus()
	}
	return textinput.Blink
}

func (m *Model) editDirty() bool {
	r := m.ov.rec
	tags := fav.Normalize(strings.FieldsFunc(m.ov.edit2.Value(), func(r rune) bool { return r == ' ' || r == ',' || r == '，' }))
	return strings.TrimSpace(m.ov.edit.Value()) != r.Title || strings.Join(tags, " ") != strings.Join(r.Tags, " ") || strings.TrimSpace(m.ov.area.Value()) != r.Summary
}

func (m *Model) editKey(msg tea.KeyMsg) tea.Cmd {
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
		return m.focusField(m.ov.field + 1)
	case "shift+tab":
		return m.focusField(m.ov.field - 1)
	case "up", "down":
		if m.ov.field != 2 {
			if msg.String() == "up" {
				return m.focusField(m.ov.field - 1)
			}
			return m.focusField(m.ov.field + 1)
		}
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
		lines := strings.Split(sty.Width(inner-2).Render(view), "\n")
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
	m.ov.edit.Width, m.ov.edit2.Width = inner-4, inner-4
	m.ov.area.SetWidth(inner - 4)
	field(i18n.T("edit.field_title"), m.ov.edit.View(), 0)
	field(i18n.T("label.tags"), m.ov.edit2.View(), 1)
	field(i18n.T("card.summary"), m.ov.area.View(), 2)
	body = append(body, m.buttons(len(body)+1, []btn{
		{i18n.T("edit.btn_save"), true, (*Model).saveEdit},
		{i18n.T("btn.cancel"), false, (*Model).closeOverlay},
	})...)
	return ovRender(body, w)
}
