package tui

import (
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fav"
)

func TestSortByOrders(t *testing.T) {
	now := time.Now()
	at := func(h int) *time.Time { x := now.Add(-time.Duration(h) * time.Hour); return &x }
	a := &fav.Rec{Title: "a", FavoritedAt: ptr(now.Add(-1 * time.Hour)), SessionStartedAt: at(30)}
	b := &fav.Rec{Title: "b", FavoritedAt: ptr(now.Add(-2 * time.Hour)), SessionStartedAt: at(10)}
	c := &fav.Rec{Title: "c", FavoritedAt: ptr(now.Add(-3 * time.Hour)), SessionStartedAt: at(20)}
	in := []*fav.Rec{c, a, b}
	for s, want := range map[sortBy]string{sortFavorited: "abc", sortStarted: "bca", sortActive: "abc"} {
		got := ""
		recs, _ := s.sorted(in)
		for _, r := range recs {
			got += r.Title
		}
		if got != want {
			t.Errorf("%s 排出 %s，应为 %s", s.label(), got, want)
		}
	}
	if in[0] != c {
		t.Error("sorted 不该改动输入切片")
	}
	a.Turns, b.Turns, c.Turns = 5, 9, 5
	if recs, _ := sortTurns.sorted(in); recs[0] != b || recs[1] != a || recs[2] != c {
		t.Errorf("轮数排：多的在前，同轮数按最后活跃，got %s%s%s", recs[0].Title, recs[1].Title, recs[2].Title)
	}
	if s := sortActive.next().next().next().next(); s != sortActive {
		t.Errorf("三次切换应回到起点，得到 %s", s.label())
	}
}
