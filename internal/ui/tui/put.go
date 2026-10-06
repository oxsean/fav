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

// Another machine's row is written through that machine's put, in the background: the row keeps what it showed until
// the answer, which is copied into it in place; then come the flash and the undo offer, as for this machine's rows.

type putMsg struct {
	row   *tend.Rec
	host  string
	saved *tend.Rec
	err   error
	done  func(*Model, *tend.Rec)
}

// putActs are the record edits put carries; every other action stays this machine's.
func putAct(a act) bool {
	switch a {
	case actFavorite, actDone, actArchive, actEdit:
		return true
	}
	return false
}

// putRec sends p for r off the main loop; the command reads copies, never the row.
func (m *Model) putRec(r *tend.Rec, p tend.Patch, expect *time.Time, done func(*Model, *tend.Rec)) {
	if why := m.unwritable(r.Host); why != "" {
		m.flash(why)
		return
	}
	if expect != nil {
		e := *expect
		expect = &e
	}
	h, name, ref := m.hosts, r.Host, remote.Ref{Provider: r.Provider, SessionID: r.SessionID}
	m.pending = tea.Batch(m.pending, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		saved, err := h.Put(ctx, name, ref, p, expect)
		return putMsg{row: r, host: name, saved: saved, err: err, done: done}
	})
}

func (msg putMsg) apply(m *Model) tea.Cmd {
	if msg.err != nil {
		if hr := m.remote[msg.host]; hr != nil && wire.Code(msg.err) == wire.CodeUnknownMethod {
			hr.old = true
		}
		m.flash(putFailed(msg.host, msg.err))
		return nil
	}
	r := msg.row
	if hr := m.remote[msg.host]; hr != nil {
		r = hr.put(msg.saved)
	} else {
		*r = *msg.saved
	}
	m.pin, m.pinKey = r, m.pinContext()
	m.refresh()
	if msg.done != nil {
		msg.done(m, r)
	}
	return nil
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

// unwritable is why host's rows cannot be written now, "" when they can (or it is not known yet that they cannot).
func (m *Model) unwritable(host string) string {
	switch {
	case m.shared(host):
		return i18n.F("remote.shared_read_only", m.ownerName(host))
	case m.serverDown() != nil:
		return i18n.F("remote.put_server_down", remote.Reason(m.serverDown()))
	case m.remote[host] != nil && m.remote[host].old:
		return i18n.F("remote.put_old", host, host)
	}
	return ""
}

// putFailed says why a put to host did not land.
func putFailed(host string, err error) string {
	switch wire.Code(err) {
	case wire.CodeUnknownMethod:
		return i18n.F("remote.put_old", host, host)
	case wire.CodeStale:
		return i18n.F("remote.put_stale", host)
	}
	return i18n.F("remote.put_failed", host, remote.Reason(err))
}

// refusal is what a key refused on another machine's row r says: why put cannot carry it now, or that only reading
// and resuming are offered for it.
func (m *Model) refusal(r *tend.Rec, a act) string {
	if r != nil && putAct(a) {
		if why := m.unwritable(r.Host); why != "" {
			return why
		}
	}
	return m.readOnlyNote(r)
}

// putsOn: a goes through put on r's machine, which takes it now.
func (m *Model) putsOn(r *tend.Rec, a act) bool {
	return r != nil && r.Host != "" && putAct(a) && m.unwritable(r.Host) == ""
}
