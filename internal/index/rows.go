package index

import (
	"path/filepath"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

// Rows lists the sessions a query shows; the zero value is ready. ⚠️ Rows it makes up (trash, agent runs, unindexed
// live sessions) are reused per session key, because the TUI keys cursor, pin and probes by pointer.
type Rows struct {
	trash, agents, live map[string]*fav.Rec
}

// List: status:trash the trash manifest, status:agent the one-shot runs, else the store (plus unfav when q.All, plus
// unindexed running sessions for status:live). Unsorted; a manifest read error still returns the rows.
func (rs *Rows) List(s *fav.Store, idx *Index, unfav []*fav.Rec, live map[string]capture.Live, q fav.Query) ([]*fav.Rec, error) {
	if q.Live == nil && live != nil {
		q.Live = func(id string) bool { _, ok := live[id]; return ok }
	}
	switch q.Status {
	case fav.StatusTrash:
		return rs.trashed(q)
	case fav.StatusAgent:
		return rs.agentRuns(s, idx, q), nil
	}
	out := s.Query(q)
	if q.All {
		for _, r := range unfav {
			if q.Match(r) {
				out = append(out, r)
			}
		}
	}
	if q.Status == fav.StatusLive {
		out = append(out, rs.running(s, idx, unfav, live, q)...)
	}
	return out, nil
}

func anyRow(q fav.Query) fav.Query {
	q.Status, q.All, q.Turns = "all", true, 0
	return q
}

func (rs *Rows) trashed(q fav.Query) ([]*fav.Rec, error) {
	entries, err := fav.LoadTrash()
	q = anyRow(q)
	next := make(map[string]*fav.Rec, len(entries))
	var out []*fav.Rec
	for _, e := range entries {
		k := fav.SessionKey(e.Provider, e.SessionID)
		r := rs.trash[k]
		if r == nil || !r.UpdatedAt.Equal(e.DeletedAt) {
			r = trashRec(e)
		}
		next[k] = r
		if q.Match(r) {
			out = append(out, r)
		}
	}
	rs.trash = next
	return out, err
}

func trashRec(e fav.TrashEntry) *fav.Rec {
	r := &fav.Rec{Provider: e.Provider, SessionID: e.SessionID, Title: e.Title, Cwd: e.Cwd, Status: fav.StatusDefault}
	if e.Record != nil {
		cp := *e.Record
		r = &cp
	}
	r.TranscriptPath, r.PinnedPath = e.Transcript(), ""
	if r.Project == "" {
		r.Project = projectOf(e.Cwd)
	}
	r.UpdatedAt = e.DeletedAt
	r.Prepare()
	return r
}

func (rs *Rows) agentRuns(s *fav.Store, idx *Index, q fav.Query) []*fav.Rec {
	stored := byKey(s.All())
	q = anyRow(q)
	next := map[string]*fav.Rec{}
	var out []*fav.Rec
	for _, ss := range idx.AgentSessions() {
		r := stored[ss.Key()]
		if r != nil {
			r.Attach(ss.Turns, ss.Turns+ss.Replies, ss.LastAt, ss.Prompts)
		} else {
			r = rs.agents[ss.Key()]
			if fresh := ss.Rec(); r == nil || r.ID != "" {
				r = fresh
			} else {
				*r = *fresh
			}
			next[ss.Key()] = r
		}
		if q.Match(r) {
			out = append(out, r)
		}
	}
	rs.agents = next
	return out
}

func (rs *Rows) running(s *fav.Store, idx *Index, unfav []*fav.Rec, live map[string]capture.Live, q fav.Query) []*fav.Rec {
	known := map[string]bool{}
	for _, recs := range [][]*fav.Rec{s.All(), unfav} {
		for _, r := range recs {
			known[r.SessionID] = true
		}
	}
	next := map[string]*fav.Rec{}
	var out []*fav.Rec
	for id, l := range live {
		if known[id] {
			continue
		}
		k := fav.SessionKey(l.Agent, id)
		r := rs.live[k]
		if r == nil || r.ID != "" {
			r = &fav.Rec{Provider: l.Agent, SessionID: id}
		}
		r.Title, r.Cwd, r.Project = l.Title, l.Cwd, projectOf(l.Cwd)
		if r.Title == "" {
			r.Title = i18n.F("live.just_started", id[:min(8, len(id))])
		}
		if r.TranscriptPath == "" {
			r.TranscriptPath = idx.Transcript(id)
		}
		r.Prepare()
		next[k] = r
		if q.Match(r) {
			out = append(out, r)
		}
	}
	rs.live = next
	return out
}

func byKey(recs []*fav.Rec) map[string]*fav.Rec {
	m := make(map[string]*fav.Rec, len(recs))
	for _, r := range recs {
		m[r.Key()] = r
	}
	return m
}

func projectOf(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}
