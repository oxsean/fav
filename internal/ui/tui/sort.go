package tui

import (
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
)

// sortBy drives list order, date grouping and the time shown per row: one key for all three (tend.SortBy).
type sortBy tend.SortBy

const (
	sortActive    = sortBy(tend.SortActive)
	sortStarted   = sortBy(tend.SortStarted)
	sortFavorited = sortBy(tend.SortFavorited)
	sortTurns     = sortBy(tend.SortTurns) // breaks date order: the time column and groups still use last activity
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

func (s sortBy) at(r *tend.Rec) time.Time { return tend.SortBy(s).At(r) }

func (s sortBy) sorted(recs []*tend.Rec) ([]*tend.Rec, map[*tend.Rec]time.Time) {
	out := make([]*tend.Rec, len(recs))
	copy(out, recs)
	tend.SortBy(s).Sort(out)
	at := make(map[*tend.Rec]time.Time, len(out))
	for _, r := range out {
		at[r] = s.at(r)
	}
	return out, at
}
