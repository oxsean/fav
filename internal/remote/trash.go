package remote

import (
	"context"
	"slices"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Trash moves the session ref on name into that machine's trash, as the TUI's delete does there, and drops it from the
// cached list. A running session answers wire.CodeBusy; a tend whose hello lists no trash is not sent it.
func (h *Hosts) Trash(ctx context.Context, name string, ref Ref) (TrashResult, error) {
	var res TrashResult
	if err := h.callIf(ctx, name, MTrash, TrashParams{ref}, &res); err != nil {
		return TrashResult{}, err
	}
	h.putMu.Lock() // one rewrite of a cached list at a time
	defer h.putMu.Unlock()
	if c := h.load(name); !c.At.IsZero() {
		c.Sessions = slices.DeleteFunc(c.Sessions, func(s Session) bool { return s.Provider == ref.Provider && s.SessionID == ref.SessionID })
		h.save(name, c)
	}
	return res, nil
}

// Restore puts the session ref in name's trash back; the next fetch of name's list brings it.
func (h *Hosts) Restore(ctx context.Context, name string, ref Ref) (RestoreResult, error) {
	var res RestoreResult
	if err := h.callIf(ctx, name, MRestore, RestoreParams{ref}, &res); err != nil {
		return RestoreResult{}, err
	}
	return res, nil
}

// Trashed is name's trash, newest first, each row's UpdatedAt the time it was moved there; days is name's trash_days.
func (h *Hosts) Trashed(ctx context.Context, name string) (recs []*tend.Rec, days int, err error) {
	p := QueryParams{Q: "status:" + tend.StatusTrash, All: true, Limit: maxQueryLimit}
	for {
		var res QueryResult
		if err := h.callIf(ctx, name, MQuery, p, &res); err != nil {
			return nil, 0, err
		}
		for _, row := range res.Rows {
			r := row.Rec(name)
			if row.DeletedAt != nil {
				r.UpdatedAt = row.DeletedAt.Local()
			}
			recs = append(recs, r)
		}
		days = res.TrashDays
		if res.Next == nil || len(res.Rows) == 0 {
			return recs, days, nil
		}
		p.After = res.Next
	}
}

// callIf is callIfKnown for the trash methods: name's hello lists trash too.
func (h *Hosts) callIf(ctx context.Context, name, method string, params, out any) error {
	return h.callIfKnown(ctx, name, method, params, out, MTrash)
}

// TooOld says host's tend is too old for method.
func TooOld(host, method string) string {
	switch method {
	case MPut:
		return i18n.F("remote.put_old", host, host)
	case MTrash, MRestore:
		return i18n.F("remote.trash_old", host, host)
	}
	return i18n.F("remote.method_old", host, host)
}

// Refused says why method (put, trash or restore) on host did not land.
func Refused(host, method string, err error) string {
	switch wire.Code(err) {
	case wire.CodeUnknownMethod:
		return TooOld(host, method)
	case wire.CodeStale:
		return i18n.F("remote.put_stale", host)
	case wire.CodeBusy:
		return i18n.F("remote.trash_busy", host)
	}
	switch method {
	case MTrash:
		return i18n.F("remote.trash_failed", host, Reason(err))
	case MRestore:
		return i18n.F("remote.restore_failed", host, Reason(err))
	}
	return i18n.F("remote.put_failed", host, Reason(err))
}
