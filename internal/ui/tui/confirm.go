package tui

import "github.com/oxsean/fav/internal/render"

// ⚠️ openConfirm focuses Cancel so Enter backs out; back is where Cancel goes (nil: just close).
func (m *Model) openConfirm(title, okLabel string, lines []string, confirm, back func(*Model)) {
	m.ov = overlay{kind: ovConfirm, title: title, okLabel: okLabel, lines: lines, focus: 1, confirm: confirm, back: back}
}

func (m *Model) confirmKey(key string) {
	switch a := keyAct(inConfirm, key); a {
	case actConfirm:
		m.doConfirm()
	case actClose:
		m.cancelConfirm()
	default:
		m.dialogKey(a, func() { // before the first frame draws the buttons: focus 0 is OK, 1 Cancel
			if m.ov.focus == 0 {
				m.doConfirm()
			} else {
				m.cancelConfirm()
			}
		})
	}
}

func (m *Model) cancelConfirm() { m.closeThen(m.ov.back) }

func (m *Model) doConfirm() { m.closeThen(m.ov.confirm) }

func (m *Model) renderConfirm() string {
	w := m.ovWidth()
	inner := w - 4
	var body []string
	body = append(body, boldSty.Foreground(cText).Render(m.ov.title), "")
	for _, l := range m.ov.lines {
		body = append(body, render.Wrap(l, inner)...)
	}
	body = append(body, "")
	body = append(body, m.buttons(len(body)+1, []btn{
		{keyed(keyOf(inConfirm, actConfirm), m.ov.okLabel), false, (*Model).doConfirm},
		{m.ov.cancelLabel(), false, (*Model).cancelConfirm},
	})...)
	return ovRender(body, w)
}
