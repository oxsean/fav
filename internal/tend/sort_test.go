package tend

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSortOrdersAndTies(t *testing.T) {
	now := time.Now()
	at := func(h int) *time.Time { return new(now.Add(-time.Duration(h) * time.Hour)) }
	a := &Rec{Provider: "claude", SessionID: "a", FavoritedAt: at(4), SessionStartedAt: at(30), LastAt: *at(1), ID: "1"}
	b := &Rec{Provider: "claude", SessionID: "b", FavoritedAt: at(5), SessionStartedAt: at(10), LastAt: *at(2), ID: "2"}
	c := &Rec{Provider: "claude", SessionID: "c", FavoritedAt: at(6), SessionStartedAt: at(20), LastAt: *at(3), ID: "3"}
	order := func(s SortBy, recs ...*Rec) string {
		s.Sort(recs)
		out := ""
		for _, r := range recs {
			out += r.SessionID
		}
		return out
	}
	for s, want := range map[SortBy]string{SortActive: "abc", SortStarted: "bca", SortFavorited: "abc"} {
		if got := order(s, c, a, b); got != want {
			t.Errorf("%s: %s, want %s", s, got, want)
		}
	}
	a.Turns, b.Turns, c.Turns = 5, 9, 5
	if got := order(SortTurns, c, a, b); got != "bac" {
		t.Errorf("turns: most first, then last active: %s", got)
	}
	b.LastAt, c.LastAt, b.SessionStartedAt, c.SessionStartedAt = a.LastAt, a.LastAt, a.SessionStartedAt, a.SessionStartedAt
	if got := order(SortActive, c, b, a); got != "abc" {
		t.Errorf("equal times go by key: %s", got)
	}
	for _, name := range []string{"", "active", "started", "favorited", "turns"} {
		if s, ok := ParseSort(name); !ok || (name != "" && s.String() != name) {
			t.Errorf("ParseSort(%q) = %v %v", name, s, ok)
		}
	}
	if _, ok := ParseSort("size"); ok {
		t.Error("an unknown order")
	}
}

func TestCursorSurvivesJSON(t *testing.T) {
	r := &Rec{Provider: "codex", SessionID: "x", LastAt: time.Now(), Turns: 4}
	c := SortTurns.Cursor(r)
	b, _ := json.Marshal(c)
	var back Cursor
	if err := json.Unmarshal(b, &back); err != nil || Compare(SortTurns, c, back) != 0 || Less(SortTurns, c, back) {
		t.Errorf("%s → %+v %v", b, back, err)
	}
}
