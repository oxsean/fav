package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
)

const undoFor = 6 * time.Second

// undoable is the last reversible action and how to take it back while it is fresh; seq is the notice that offers it.
type undoable struct {
	at   time.Time
	seq  int
	back func(*Model) tea.Cmd
}

// offerUndo says note with the undo key; for undoFor, u takes the action back.
func (m *Model) offerUndo(note string, back func(*Model) tea.Cmd) {
	m.flash(i18n.F("undo.offer", note, keyOf(inList, actUndo)))
	m.undo = &undoable{at: time.Now(), seq: m.noticeSeq, back: back}
}

func (m *Model) doUndo() tea.Cmd {
	u := m.undo
	m.undo = nil
	if u == nil || time.Since(u.at) > undoFor {
		m.flash(i18n.T("undo.nothing"))
		return nil
	}
	return u.back(m)
}

// restoreRec takes a record's change back: fields as they were before it.
func (m *Model) restoreRec(r *tend.Rec, back func(*tend.Rec)) func(*Model) tea.Cmd {
	return func(mm *Model) tea.Cmd {
		if mm.editRec(r, back) != nil {
			mm.flash(i18n.F("undo.done", r.Title))
		}
		return nil
	}
}
