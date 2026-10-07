package index

import (
	"strings"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/tend"
)

// MatchRef finds the items whose session id starts with ref or whose record id is ref: the last hit and how many.
func MatchRef[T any](items []T, ref string, keys func(T) (sid, id string)) (hit T, n int) {
	for _, it := range items {
		if sid, id := keys(it); strings.HasPrefix(sid, ref) || id != "" && id == ref {
			hit, n = it, n+1
		}
	}
	return hit, n
}

func RecKeys(r *tend.Rec) (string, string) { return r.SessionID, r.ID }

// Find resolves ref, a record id or session id prefix, on this machine: the favorites, the indexed sessions, the
// running ones, then any file the index has (one-shot runs, silent sessions, older ids of a chain); the first of these
// with a hit answers, with its last hit and how many it has. A session never favorited comes back with an empty ID.
// open is called only when the favorites have no hit.
func Find(s *tend.Store, open func() (*Index, error), ref string) (*tend.Rec, int, error) {
	if r := s.Get(ref); r != nil {
		return r, 1, nil
	}
	if r, n := MatchRef(s.All(), ref, RecKeys); n > 0 {
		return r, n, nil
	}
	idx, err := open()
	if err != nil {
		return nil, 0, err
	}
	sessionKeys := func(ss *Session) (string, string) { return ss.SessionID, "" }
	if ss, n := MatchRef(idx.Sessions(), ref, sessionKeys); n > 0 {
		return ss.Rec(), n, nil
	}
	var rows Rows
	running, _ := rows.List(s, idx, nil, capture.LiveSessions(), tend.Query{Status: tend.StatusLive, All: true})
	if r, n := MatchRef(running, ref, RecKeys); n > 0 {
		return r, n, nil
	}
	f, n := idx.FileByPrefix(ref)
	if n != 1 {
		return nil, n, nil
	}
	return f.Rec(), 1, nil
}
