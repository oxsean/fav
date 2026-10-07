package index

import (
	"path/filepath"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
)

// Rows lists the sessions a query shows; the zero value is ready. ⚠️ Rows it makes up (trash, agent runs, unindexed
// live sessions) are reused per session key, because the TUI keys cursor, pin and probes by pointer.
type Rows struct {
	// Belong names the project a row's directory belongs to ("" for none); the caller brings the projects and the
	// rule (task.ProjectOf), so this package does not depend on them. nil: every row is in none.
	Belong func(r *tend.Rec) (id, name string)
	// Copies are a row's migrations to and from other machines as this machine recorded them; the caller brings them
	// (migrate.Marks), so this package does not depend on it. nil, and for another machine's row: a row keeps what it
	// carries (a coordinator's rows carry what their node sent).
	Copies func(r *tend.Rec) []tend.Copy

	trash, agents, live map[string]*tend.Rec
}

// Place sets the project of each of recs, and this machine's own their migrations, as List does before it matches:
// rows listed elsewhere (another machine's) go through it too.
func (rs *Rows) Place(recs ...*tend.Rec) {
	for _, r := range recs {
		r.ProjectID, r.ProjectName = "", ""
		if rs.Belong != nil {
			r.ProjectID, r.ProjectName = rs.Belong(r)
		}
		if rs.Copies != nil && r.Host == "" {
			r.Copies = rs.Copies(r)
		}
	}
}

// List: status:trash the trash manifest, status:agent the one-shot runs, else the store (plus unfav when q.All, plus
// unindexed running sessions for status:live). Unsorted; a manifest read error still returns the rows.
func (rs *Rows) List(s *tend.Store, idx *Index, unfav []*tend.Rec, live map[string]capture.Live, q tend.Query) ([]*tend.Rec, error) {
	if q.Live == nil && live != nil {
		q.Live = func(id string) bool { _, ok := live[id]; return ok }
	}
	switch q.Status {
	case tend.StatusTrash:
		return rs.trashed(q)
	case tend.StatusAgent:
		return rs.agentRuns(s, idx, q), nil
	}
	rs.Place(s.All()...)
	out := s.Query(q)
	if q.All {
		rs.Place(unfav...)
		for _, r := range unfav {
			if q.Match(r) {
				out = append(out, r)
			}
		}
	}
	if q.Status == tend.StatusLive {
		out = append(out, rs.running(s, idx, unfav, live, q)...)
	}
	return out, nil
}

func anyRow(q tend.Query) tend.Query {
	q.Status, q.All, q.Turns = "all", true, 0
	return q
}

func (rs *Rows) trashed(q tend.Query) ([]*tend.Rec, error) {
	entries, err := tend.LoadTrash()
	q = anyRow(q)
	next := make(map[string]*tend.Rec, len(entries))
	var out []*tend.Rec
	for _, e := range entries {
		k := tend.SessionKey(e.Provider, e.SessionID)
		r := rs.trash[k]
		if r == nil || !r.UpdatedAt.Equal(e.DeletedAt) {
			r = trashRec(e)
		}
		next[k] = r
		rs.Place(r)
		if q.Match(r) {
			out = append(out, r)
		}
	}
	rs.trash = next
	return out, err
}

func trashRec(e tend.TrashEntry) *tend.Rec {
	r := &tend.Rec{Provider: e.Provider, SessionID: e.SessionID, Title: e.Title, Cwd: e.Cwd, Status: tend.StatusDefault}
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

func (rs *Rows) agentRuns(s *tend.Store, idx *Index, q tend.Query) []*tend.Rec {
	stored := byKey(s.All())
	q = anyRow(q)
	next := map[string]*tend.Rec{}
	var out []*tend.Rec
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
		rs.Place(r)
		if q.Match(r) {
			out = append(out, r)
		}
	}
	rs.agents = next
	return out
}

func (rs *Rows) running(s *tend.Store, idx *Index, unfav []*tend.Rec, live map[string]capture.Live, q tend.Query) []*tend.Rec {
	known := map[string]bool{}
	for _, recs := range [][]*tend.Rec{s.All(), unfav} {
		for _, r := range recs {
			known[r.SessionID] = true
		}
	}
	next := map[string]*tend.Rec{}
	var out []*tend.Rec
	for id, l := range live {
		if known[id] {
			continue
		}
		k := tend.SessionKey(l.Agent, id)
		r := rs.live[k]
		if r == nil || r.ID != "" {
			r = &tend.Rec{Provider: l.Agent, SessionID: id}
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
		rs.Place(r)
		if q.Match(r) {
			out = append(out, r)
		}
	}
	rs.live = next
	return out
}

func byKey(recs []*tend.Rec) map[string]*tend.Rec {
	m := make(map[string]*tend.Rec, len(recs))
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
