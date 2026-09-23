package tui

import (
	"path/filepath"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/render"
)

func (m *Model) inTrash() bool { return fav.Parse(m.search.Value()).Status == fav.StatusTrash }

func (m *Model) pickStatus() {
	if m.view == viewLive {
		return
	}
	q := fav.Parse(m.search.Value())
	items := []item{
		{name: fav.StatusOpen, label: i18n.T("status.open")},
		{name: fav.StatusActive, label: i18n.T("status.active")},
		{name: fav.StatusDone, label: render.StatusLabel(fav.StatusDone)},
		{name: "archived", label: i18n.T("status.archived")},
		{name: "all", label: i18n.T("label.all")},
		{name: fav.StatusTrash, label: i18n.T("status.trash")},
		{name: fav.StatusAgent, label: i18n.T("status.agent")},
	}
	m.openPicker(i18n.T("picker.status_title"), "", items, false, []string{q.Status},
		func(m *Model, chosen []string) {
			var toks []string
			if len(chosen) > 0 && chosen[0] != fav.StatusOpen {
				toks = []string{"status:" + chosen[0]}
			}
			m.setQuery(toks, hasPrefix("status:"))
		})
}

// trashRecs turns the trash manifest into cards; objects are reused per session so the cursor can follow them.
// agentRecs: one-shot SDK / exec / sub-agent sessions, which no other listing shows; objects are reused across refreshes.
func (m *Model) agentRecs(q fav.Query) []*fav.Rec {
	if m.agents == nil {
		m.agents = map[string]*fav.Rec{}
	}
	q.Status, q.All, q.Turns = "all", true, 0
	var out []*fav.Rec
	for _, ss := range m.idx.AgentSessions() {
		r := m.agents[ss.Key()]
		if r == nil {
			r = ss.Rec()
			m.agents[ss.Key()] = r
		}
		if q.Match(r) {
			out = append(out, r)
		}
	}
	return out
}

func (m *Model) trashRecs(q fav.Query) []*fav.Rec {
	entries, err := fav.LoadTrash()
	if err != nil {
		m.flash(i18n.T("flash.trash_read_failed") + err.Error())
	}
	if m.trashed == nil {
		m.trashed = map[string]*fav.Rec{}
	}
	q.Status, q.All, q.Turns = "all", true, 0
	var out []*fav.Rec
	for _, e := range entries {
		k := e.Provider + ":" + e.SessionID
		r := m.trashed[k]
		if r == nil {
			r = trashRec(e)
			m.trashed[k] = r
		}
		if q.Match(r) {
			out = append(out, r)
		}
	}
	return out
}

func trashRec(e fav.TrashEntry) *fav.Rec {
	r := &fav.Rec{Provider: e.Provider, SessionID: e.SessionID, Title: e.Title, Cwd: e.Cwd, Status: fav.StatusDefault}
	if e.Record != nil {
		cp := *e.Record
		r = &cp
	}
	r.TranscriptPath, r.PinnedPath = e.Transcript(), ""
	if r.Project == "" && e.Cwd != "" {
		r.Project = filepath.Base(e.Cwd)
	}
	r.UpdatedAt = e.DeletedAt
	return r
}

func (m *Model) askDelete() {
	r := m.current()
	if r == nil {
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
	files := index.SessionFiles(r.Provider, r.SessionID)
	if r.PinnedPath != "" {
		files = append(files, r.PinnedPath)
	}
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
	m.ov = overlay{kind: ovConfirm, title: i18n.T("trash.title"), lines: body, focus: 1, okLabel: i18n.T("trash.btn_delete"),
		confirm: func(m *Model) { m.deleteSession(r, files) }}
}

func (m *Model) deleteSession(r *fav.Rec, files []string) {
	e := fav.TrashEntry{Provider: r.Provider, SessionID: r.SessionID, Title: r.Title, Cwd: r.Cwd}
	if r.ID != "" {
		if cur := m.store.Get(r.ID); cur != nil {
			r = cur
		}
		cp := *r
		e.Record = &cp
	}
	if e.SessionID == "" {
		e.SessionID = r.ID
	}
	if _, err := fav.MoveToTrash(e, files); err != nil {
		m.flash(i18n.T("flash.delete_failed") + err.Error())
		return
	}
	if r.ID != "" {
		r.Deleted = true
		if err := m.store.Put(r); err != nil {
			m.flash(i18n.T("flash.write_failed") + err.Error())
		}
	}
	m.dropUnfav(r)
	if m.gone == nil {
		m.gone = map[string]bool{}
	}
	m.gone[r.Provider+":"+r.SessionID] = true
	delete(m.trashed, r.Provider+":"+e.SessionID)
	m.probes, m.probeWant = nil, nil
	m.recount()
	m.refresh()
	m.flash(i18n.T("trash.moved") + render.Truncate(r.Title, 40))
}

func (m *Model) restoreTrash(r *fav.Rec) {
	e, err := fav.RestoreTrash(r.Provider, r.SessionID)
	if err != nil {
		m.flash(i18n.T("flash.restore_failed") + err.Error())
		return
	}
	if e.Record != nil {
		e.Record.Deleted = false
		if err := m.store.Put(e.Record); err != nil {
			m.flash(i18n.T("flash.write_failed") + err.Error())
		}
	}
	delete(m.gone, r.Provider+":"+r.SessionID)
	delete(m.trashed, r.Provider+":"+r.SessionID)
	if force := index.RescanAfterRestore(e); len(force) > 0 { // undoing a move puts the original back; the index must rescan
		m.rescan(force)
	}
	m.recount()
	m.refresh()
	m.flash(i18n.F("trash.restored", render.Truncate(e.Title, 40)))
}

func (m *Model) confirmKey(key string) {
	switch keyAct(inConfirm, key) {
	case actEnter:
		if m.pressFocused() {
			return
		}
		if m.ov.focus == 1 { // focus is on Cancel (also before the buttons are rendered)
			m.cancelConfirm()
			return
		}
		m.doConfirm()
	case actConfirm:
		m.doConfirm()
	case actFocusPrev:
		m.moveFocus(-1)
	case actFocusNext:
		m.moveFocus(1)
	case actClose:
		m.cancelConfirm()
	}
}

func (m *Model) cancelConfirm() {
	back := m.ov.back
	m.closeOverlay()
	if back != nil {
		back(m)
	}
}

func (m *Model) doConfirm() {
	f := m.ov.confirm
	m.closeOverlay()
	if f != nil {
		f(m)
	}
}

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
		{keyed(keyOf(inConfirm, actConfirm), m.ov.okLabel), m.ov.focus < 0, (*Model).doConfirm},
		{m.ov.cancelLabel(), false, (*Model).cancelConfirm},
	})...)
	return ovRender(body, w)
}
