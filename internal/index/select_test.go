package index

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/tend"
)

func selRec(id string, mod func(*tend.Rec)) *tend.Rec {
	r := &tend.Rec{ID: "r" + id, Provider: tend.ProviderClaude, SessionID: id, Title: id, Status: tend.StatusDoing,
		FavoritedAt: new(time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)), Turns: 5}
	if mod != nil {
		mod(r)
	}
	r.Prepare()
	return r
}

func ids(recs []*tend.Rec) string {
	out := ""
	for _, r := range recs {
		out += r.SessionID
	}
	return out
}

func TestSelectSortsEachWay(t *testing.T) {
	day := func(d int) *time.Time { return new(time.Date(2026, 10, d, 0, 0, 0, 0, time.Local)) }
	a := selRec("a", func(r *tend.Rec) { r.SessionStartedAt, r.FavoritedAt, r.LastAt, r.Turns = day(1), day(9), *day(5), 3 })
	b := selRec("b", func(r *tend.Rec) { r.SessionStartedAt, r.FavoritedAt, r.LastAt, r.Turns = day(3), day(2), *day(4), 30 })
	c := selRec("c", func(r *tend.Rec) { r.SessionStartedAt, r.FavoritedAt, r.LastAt, r.Turns = day(2), day(7), *day(6), 3 })
	for s, want := range map[tend.SortBy]string{tend.SortActive: "cab", tend.SortStarted: "bca", tend.SortFavorited: "acb", tend.SortTurns: "bca"} {
		if got := ids(Select(nil, []*tend.Rec{a, b, c}, tend.Parse(""), Page{Sort: s}).Rows); got != want {
			t.Errorf("%s: %s, want %s", s, got, want)
		}
	}
}

// TestSelectPagesAfterACursor: rows of equal time page by key without a gap or a repeat; a row that appears between
// two pages and sorts before the cursor shows only when the list is read from the top again.
func TestSelectPagesAfterACursor(t *testing.T) {
	same := time.Date(2026, 10, 2, 0, 0, 0, 0, time.Local)
	var recs []*tend.Rec
	for i := range 7 {
		recs = append(recs, selRec(fmt.Sprint(i), func(r *tend.Rec) { r.LastAt = same }))
	}
	q := tend.Parse("")
	first := Select(nil, recs, q, Page{Limit: 3})
	if ids(first.Rows) != "012" || first.Next == nil || first.Next.Key != recs[2].Key() || first.Matched != 7 {
		t.Fatalf("first page %s next %+v", ids(first.Rows), first.Next)
	}
	recs = append(recs, selRec("00", func(r *tend.Rec) { r.LastAt = same }), selRec("9", func(r *tend.Rec) { r.LastAt = same.Add(time.Hour) }))
	slices.Reverse(recs)
	second := Select(nil, recs, q, Page{After: first.Next, Limit: 3})
	if ids(second.Rows) != "345" || second.Next == nil {
		t.Fatalf("second page %s", ids(second.Rows))
	}
	last := Select(nil, recs, q, Page{After: second.Next, Limit: 3})
	if ids(last.Rows) != "6" || last.Next != nil {
		t.Fatalf("last page %s next %+v", ids(last.Rows), last.Next)
	}
	if top := Select(nil, recs, q, Page{Limit: 3}); ids(top.Rows) != "9000" {
		t.Fatalf("from the top again: %s", ids(top.Rows))
	}
}

// TestSelectCountsTheScope: Total and Facets count the rows the query's scope keeps, whatever its tag, project, source
// and keyword filters; Matched and Running the rows the query picks.
func TestSelectCountsTheScope(t *testing.T) {
	recs := []*tend.Rec{
		selRec("a", func(r *tend.Rec) { r.Tags, r.Cwd, r.Project = []string{"api", "bug"}, "/w/shop/api", "api" }),
		selRec("b", func(r *tend.Rec) {
			r.Tags, r.Cwd, r.Project, r.Provider = []string{"api"}, "/w/notes", "notes", tend.ProviderCodex
		}),
		selRec("c", func(r *tend.Rec) { r.Cwd, r.Project = "/w/notes", "notes" }),
		selRec("d", func(r *tend.Rec) { r.ArchivedAt, r.Tags = new(time.Now()), []string{"api"} }),
		selRec("e", func(r *tend.Rec) { r.FavoritedAt, r.ID, r.Turns = nil, "", 1 }),
	}
	rows := Rows{Belong: func(r *tend.Rec) (string, string) {
		if r.Cwd == "/w/shop/api" {
			return "p_shop", "Shop"
		}
		return "", ""
	}}
	q := tend.Parse("#api provider:claude")
	q.All, q.Live = true, func(id string) bool { return id == "a" }
	sel := Select(&rows, recs, q, Page{})
	if ids(sel.Rows) != "a" || sel.Matched != 1 || sel.Running != 1 || sel.Total != 3 {
		t.Fatalf("rows %s matched %d running %d total %d", ids(sel.Rows), sel.Matched, sel.Running, sel.Total)
	}
	f := sel.Facets
	if f.Projects["p_shop"] != 1 || f.Projects[""] != 2 || f.Groups["notes"] != 2 || f.Groups["api"] != 0 ||
		f.Tags["api"] != 2 || f.Tags["bug"] != 1 || f.Providers[tend.ProviderCodex] != 1 || f.Providers[tend.ProviderClaude] != 2 {
		t.Errorf("facets %+v", f)
	}
	if recs[0].ProjectID != "p_shop" {
		t.Error("Select places the rows it is given")
	}
	q = tend.Parse("project:none")
	if got := Select(&rows, recs, q, Page{}); ids(got.Rows) != "cb" { // equal times: by key, claude:c before codex:b
		t.Errorf("project:none: %s", ids(got.Rows))
	}
}
