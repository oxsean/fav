package tui

import (
	"os"
	"sort"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

// sortBy drives list order, date grouping and the time shown per row: one key for all three.
type sortBy int

const (
	sortActive sortBy = iota // transcript mtime; favorited time when the file is gone
	sortStarted
	sortFavorited
	sortTurns // most turns first, ties by last activity; the time column and groups still use last activity
)

func (s sortBy) next() sortBy { return (s + 1) % 4 }

func (s sortBy) label() string {
	switch s {
	case sortStarted:
		return i18n.T("sort.started")
	case sortFavorited:
		return i18n.T("sort.favorited")
	case sortTurns:
		return i18n.T("sort.turns")
	}
	return i18n.T("sort.active")
}

func (s sortBy) at(r *fav.Rec) time.Time {
	switch s {
	case sortStarted:
		return r.When()
	case sortFavorited:
		if r.FavoritedAt != nil {
			return *r.FavoritedAt
		}
		return r.When()
	}
	if !r.LastAt.IsZero() {
		return r.LastAt
	}
	for _, p := range []string{r.PinnedPath, r.TranscriptPath} {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil {
			return st.ModTime()
		}
	}
	if r.FavoritedAt != nil {
		return *r.FavoritedAt
	}
	return r.When()
}

func (s sortBy) sorted(recs []*fav.Rec) ([]*fav.Rec, map[*fav.Rec]time.Time) {
	at := make(map[*fav.Rec]time.Time, len(recs))
	out := make([]*fav.Rec, len(recs))
	for i, r := range recs {
		out[i], at[r] = r, s.at(r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if s == sortTurns && out[i].Turns != out[j].Turns {
			return out[i].Turns > out[j].Turns
		}
		return at[out[i]].After(at[out[j]])
	})
	return out, at
}
