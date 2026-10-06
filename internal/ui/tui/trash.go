package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/tend"
)

func (m *Model) inTrash() bool { return tend.Parse(m.search.Value()).Status == tend.StatusTrash }

func (m *Model) pickStatus() {
	if m.view == viewLive {
		return
	}
	q := tend.Parse(m.search.Value())
	items := []item{
		{name: tend.StatusOpen, label: i18n.T("status.open")},
		{name: tend.StatusActive, label: i18n.T("status.active")},
		{name: tend.StatusDone, label: render.StatusLabel(tend.StatusDone)},
		{name: "archived", label: i18n.T("status.archived")},
		{name: "all", label: i18n.T("label.all")},
		{name: tend.StatusTrash, label: i18n.T("status.trash")},
		{name: tend.StatusAgent, label: i18n.T("status.agent")},
	}
	m.openPicker(i18n.T("picker.status_title"), "", items, false, []string{q.Status},
		func(m *Model, chosen []string) {
			var toks []string
			if len(chosen) > 0 && chosen[0] != tend.StatusOpen {
				toks = []string{"status:" + chosen[0]}
			}
			m.setQuery(toks, tend.HasPrefix("status:"))
		})
}

func (m *Model) askDelete(r *tend.Rec) {
	if r == nil {
		return
	}
	if r.Host != "" {
		m.flash(m.readOnlyNote(r))
		return
	}
	if m.inTrash() {
		m.restoreTrash(r)
		return
	}
	if m.isLive(r.SessionID) {
		m.flash(i18n.T("trash.running"))
		return
	}
	files := index.SessionFilesOf(m.idx, r)
	var body []string
	body = append(body, render.Truncate(r.Title, 70), "")
	switch {
	case len(files) > 0:
		body = append(body, i18n.F("trash.confirm_files", len(files)))
	case r.SessionID == "":
		body = append(body, i18n.T("trash.confirm_no_session"))
	default:
		body = append(body, i18n.T("trash.confirm_files_missing"))
	}
	if d := m.cfg.TrashDays; d > 0 {
		body = append(body, i18n.F("trash.confirm_purge", d))
	}
	m.openConfirm(i18n.T("trash.title"), i18n.T("trash.btn_delete"), body, func(m *Model) { m.deleteSession(r, files) }, nil)
}

func (m *Model) deleteSession(r *tend.Rec, files []string) {
	_, err := index.Trash(m.store, r, files)
	m.adopt(m.idx.Forget(slices.DeleteFunc(files, paths.Exists)))
	if err != nil {
		m.flash(i18n.F("flash.delete_failed", err))
		return
	}
	m.flash(i18n.F("trash.moved", render.Truncate(r.Title, 40)))
}

func (m *Model) restoreTrash(r *tend.Rec) {
	e, force, err := index.Restore(m.store, r.Provider, r.SessionID)
	m.pending = tea.Batch(m.pending, m.reindex(force))
	m.recount()
	m.refresh()
	if err != nil {
		m.flash(i18n.F("flash.restore_failed", err))
		return
	}
	m.flash(i18n.F("trash.restored", render.Truncate(e.Title, 40)))
}
