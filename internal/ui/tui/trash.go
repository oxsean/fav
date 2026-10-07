package tui

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
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
	if m.inTrash() {
		m.restoreTrash(r)
		return
	}
	if r.Host != "" {
		m.askTrashFar(r)
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
	m.openConfirm(i18n.T("trash.title"), i18n.T("trash.btn_delete"), body, func(m *Model) { m.deleteSession(r) }, nil)
}

func (m *Model) deleteSession(r *tend.Rec) {
	_, next, err := index.TrashSession(m.store, m.idx, r)
	m.adopt(next)
	if err != nil {
		m.flash(i18n.F("flash.delete_failed", err))
		return
	}
	m.flash(i18n.F("trash.moved", render.Truncate(r.Title, 40)))
}

func (m *Model) restoreTrash(r *tend.Rec) {
	if r.Host != "" {
		m.restoreFar(r)
		return
	}
	e, next, err := index.RestoreSession(m.store, m.idx, r.Provider, r.SessionID)
	if next != m.idx {
		m.adopt(next)
	}
	m.pending = tea.Batch(m.pending, m.reindex(nil))
	m.recount()
	m.refresh()
	if err != nil {
		m.flash(i18n.F("flash.restore_failed", err))
		return
	}
	m.flash(i18n.F("trash.restored", render.Truncate(e.Title, 40)))
}

// Another machine's session goes into that machine's trash through its trash method and comes back through restore;
// the trash view lists each machine's trash as its query answers status:trash, read once per visit.

// askTrashFar asks before r's machine moves its files into its trash; what those files are is that machine's to know.
func (m *Model) askTrashFar(r *tend.Rec) {
	if why := m.unwritable(r.Host, remote.MTrash); why != "" {
		m.flash(why)
		return
	}
	hr := m.remote[r.Host]
	if hr == nil {
		return
	}
	if _, ok := hr.live[r.SessionID]; ok {
		m.flash(i18n.F("remote.trash_busy", r.Host))
		return
	}
	body := []string{render.Truncate(r.Title, 70), "", i18n.F("trash.confirm_far", r.Host, keyOf(inList, actDelete))}
	if hr.days > 0 {
		body = append(body, i18n.F("trash.confirm_purge", hr.days))
	}
	m.openConfirm(i18n.T("trash.title"), i18n.T("trash.btn_delete"), body, func(m *Model) { m.trashFar(r) }, nil)
}

func (m *Model) trashFar(r *tend.Rec) {
	m.farWrite(r, remote.MTrash, func(ctx context.Context, h *remote.Hosts, name string, ref remote.Ref) (func(*Model) tea.Cmd, error) {
		res, err := h.Trash(ctx, name, ref)
		return func(m *Model) tea.Cmd {
			if hr := m.remote[name]; hr != nil {
				hr.drop(r)
			}
			m.refresh()
			m.flash(i18n.F("remote.trash_moved", name, render.Truncate(res.Title, 40)))
			return nil
		}, err
	})
}

func (m *Model) restoreFar(r *tend.Rec) {
	m.farWrite(r, remote.MRestore, func(ctx context.Context, h *remote.Hosts, name string, ref remote.Ref) (func(*Model) tea.Cmd, error) {
		res, err := h.Restore(ctx, name, ref)
		return func(m *Model) tea.Cmd {
			hr := m.remote[name]
			if hr == nil {
				return nil
			}
			hr.trash = slices.DeleteFunc(hr.trash, func(x *tend.Rec) bool { return x == r })
			m.refresh()
			m.flash(i18n.F("remote.restored", name, render.Truncate(res.Title, 40)))
			return m.fetchHost(name)
		}, err
	})
}

// drop takes r out of the list; the trash view reads the trash again next time.
func (hr *hostRows) drop(r *tend.Rec) {
	hr.recs = slices.DeleteFunc(hr.recs, func(x *tend.Rec) bool { return x == r })
	delete(hr.byKey, r.Key())
	hr.trashed = false
}

type trashMsg struct {
	name string
	recs []*tend.Rec
	days int
	err  error
}

// readTrash reads the trash of each machine q shows in the trash view, once per visit; leaving the view forgets it.
func (m *Model) readTrash(q tend.Query) tea.Cmd {
	var cmds []tea.Cmd
	for name, hr := range m.remote {
		switch {
		case q.Status != tend.StatusTrash:
			hr.trashed = false
		case hr.trashed || !hostSelected(q, name) || m.unwritable(name, remote.MTrash) != "" || m.far.nc != nil && m.tasks.cl == nil:
		default:
			hr.trashed = true
			h := m.hosts
			cmds = append(cmds, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
				defer cancel()
				recs, days, err := h.Trashed(ctx, name)
				return trashMsg{name: name, recs: recs, days: days, err: err}
			})
		}
	}
	return tea.Batch(cmds...)
}

func (msg trashMsg) apply(m *Model) tea.Cmd {
	hr := m.remote[msg.name]
	if hr == nil {
		return nil
	}
	if msg.err != nil {
		if wire.Code(msg.err) == wire.CodeUnknownMethod {
			hr.lack(remote.MTrash)
			m.flash(remote.TooOld(msg.name, remote.MTrash))
			return nil
		}
		m.flash(i18n.F("remote.trash_unread", msg.name, remote.Reason(msg.err)))
		return nil
	}
	hr.trash, hr.days = msg.recs, msg.days
	m.refresh()
	return nil
}
