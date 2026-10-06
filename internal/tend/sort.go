package tend

import (
	"slices"
	"strings"
	"time"
)

// SortBy is a list order; the time it sorts by is also the time a row shows.
type SortBy int

const (
	SortActive    SortBy = iota // Rec.ActiveAt
	SortStarted                 // Rec.When
	SortFavorited               // FavoritedAt, else When
	SortTurns                   // most turns first, ties by ActiveAt
)

var sortNames = [...]string{"active", "started", "favorited", "turns"}

// ParseSort reads an order's name; "" is SortActive.
func ParseSort(s string) (SortBy, bool) {
	if s == "" {
		return SortActive, true
	}
	i := slices.Index(sortNames[:], s)
	return SortBy(max(i, 0)), i >= 0
}

func (s SortBy) String() string { return sortNames[s] }

// At is the time r sorts by: ActiveAt for SortTurns too.
func (s SortBy) At(r *Rec) time.Time {
	switch s {
	case SortStarted:
		return r.When()
	case SortFavorited:
		if r.FavoritedAt != nil {
			return *r.FavoritedAt
		}
		return r.When()
	}
	return r.ActiveAt()
}

// Cursor is where r falls in s.
func (s SortBy) Cursor(r *Rec) Cursor {
	c := Cursor{Key: r.Key(), At: s.At(r)}
	if s == SortTurns {
		c.N = r.Turns
	}
	return c
}

// Sort orders recs by s.
func (s SortBy) Sort(recs []*Rec) {
	type keyed struct {
		c Cursor
		r *Rec
	}
	ks := make([]keyed, len(recs))
	for i, r := range recs {
		ks[i] = keyed{s.Cursor(r), r}
	}
	slices.SortFunc(ks, func(a, b keyed) int { return Compare(s, a.c, b.c) })
	for i, k := range ks {
		recs[i] = k.r
	}
}

// Cursor is where a row falls in an order: keyset paging resumes after one, and lists from several machines merge by
// comparing them.
type Cursor struct {
	Key string    `json:"key"` // provider:session_id
	At  time.Time `json:"at"`
	N   int       `json:"n,omitempty"` // turns, for SortTurns
}

// Compare orders a and b in s: the sort value descending, then the key ascending.
func Compare(s SortBy, a, b Cursor) int {
	if s == SortTurns && a.N != b.N {
		if a.N > b.N {
			return -1
		}
		return 1
	}
	if c := b.At.Compare(a.At); c != 0 {
		return c
	}
	return strings.Compare(a.Key, b.Key)
}

// Less: a comes before b in s.
func Less(s SortBy, a, b Cursor) bool { return Compare(s, a, b) < 0 }
