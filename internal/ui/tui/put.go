package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Another machine's row is written through that machine's node methods, in the background: put for record edits,
// trash and restore for delete. The row keeps what it showed until the answer, which is applied on the main loop; then
// come the flash and the undo offer, as for this machine's rows.

// farCall runs a write against machine name off the main loop and returns how to apply its answer.
type farCall func(ctx context.Context, h *remote.Hosts, name string, ref remote.Ref) (func(*Model) tea.Cmd, error)

type farMsg struct {
	host   string
	method string
	err    error
	ok     func(*Model) tea.Cmd
}

// farMethod is the node method that carries a on another machine's row, "" when none does: record edits go through
// put, delete through trash (restore in the trash view).
func (m *Model) farMethod(a act) string {
	switch a {
	case actFavorite, actDone, actArchive, actEdit, actTitle:
		return remote.MPut
	case actDelete:
		if m.inTrash() {
			return remote.MRestore
		}
		return remote.MTrash
	}
	return ""
}

// farWrite sends call for r's machine; the command reads copies, never the row.
func (m *Model) farWrite(r *tend.Rec, method string, call farCall) {
	if why := m.unwritable(r.Host, method); why != "" {
		m.flash(why)
		return
	}
	h, name, ref := m.hosts, r.Host, remote.Ref{Provider: r.Provider, SessionID: r.SessionID}
	m.pending = tea.Batch(m.pending, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		ok, err := call(ctx, h, name, ref)
		return farMsg{host: name, method: method, err: err, ok: ok}
	})
}

func (msg farMsg) apply(m *Model) tea.Cmd {
	if msg.err != nil {
		if hr := m.remote[msg.host]; hr != nil && wire.Code(msg.err) == wire.CodeUnknownMethod {
			hr.lack(msg.method)
		}
		m.flash(remote.Refused(msg.host, msg.method, msg.err))
		return nil
	}
	return msg.ok(m)
}

// putRec writes p to r through its machine's put; the answer is copied into the row in place.
func (m *Model) putRec(r *tend.Rec, p tend.Patch, expect *time.Time, done func(*Model, *tend.Rec)) {
	if expect != nil {
		e := *expect
		expect = &e
	}
	m.farWrite(r, remote.MPut, func(ctx context.Context, h *remote.Hosts, name string, ref remote.Ref) (func(*Model) tea.Cmd, error) {
		saved, err := h.Put(ctx, name, ref, p, expect)
		return func(m *Model) tea.Cmd {
			row := r
			if hr := m.remote[name]; hr != nil {
				row = hr.put(saved)
			} else {
				*row = *saved
			}
			m.pin, m.pinKey = row, m.pinContext()
			m.refresh()
			if done != nil {
				done(m, row)
			}
			return nil
		}, err
	})
}

// put copies saved into the row of its session, kept by pointer, or adds it.
func (hr *hostRows) put(saved *tend.Rec) *tend.Rec {
	k := saved.Key()
	if r := hr.byKey[k]; r != nil {
		*r = *saved
		return r
	}
	if hr.byKey == nil {
		hr.byKey = map[string]*tend.Rec{}
	}
	hr.byKey[k] = saved
	hr.recs = append(hr.recs, saved)
	return saved
}

// lack notes that the machine's tend has no method.
func (hr *hostRows) lack(method string) {
	if hr.lacks == nil {
		hr.lacks = map[string]bool{}
	}
	hr.lacks[method] = true
}

// unwritable is why host's rows cannot take method now, "" when they can (or it is not known yet that they cannot).
func (m *Model) unwritable(host, method string) string {
	switch {
	case m.shared(host):
		return i18n.F("remote.shared_read_only", m.ownerName(host))
	case m.serverDown() != nil:
		return i18n.F("remote.put_server_down", remote.Reason(m.serverDown()))
	case m.remote[host] != nil && m.remote[host].lacks[method]:
		return remote.TooOld(host, method)
	}
	return ""
}

// refusal is what a key refused on another machine's row r says: why its machine cannot take it now, or that only
// reading and resuming are offered for it.
func (m *Model) refusal(r *tend.Rec, a act) string {
	if method := m.farMethod(a); r != nil && method != "" {
		if why := m.unwritable(r.Host, method); why != "" {
			return why
		}
	}
	return m.readOnlyNote(r)
}

// writesOn: a goes to r's machine, which takes it now.
func (m *Model) writesOn(r *tend.Rec, a act) bool {
	method := m.farMethod(a)
	return r != nil && r.Host != "" && method != "" && m.unwritable(r.Host, method) == ""
}
