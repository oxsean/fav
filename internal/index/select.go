package index

import (
	"sort"

	"github.com/oxsean/fav/internal/tend"
)

// Page is a window of a sorted list: Limit rows (0: all) after After (nil: from the top), in Sort's order.
type Page struct {
	Sort  tend.SortBy
	After *tend.Cursor
	Limit int
}

// Facets count the rows of a query's scope (Query.Scope) by what each picker offers.
type Facets struct {
	Projects  map[string]int `json:"projects,omitempty"` // project id, "" = none
	Groups    map[string]int `json:"groups,omitempty"`   // automatic group (Rec.Project) of the rows in no project
	Tags      map[string]int `json:"tags,omitempty"`
	Providers map[string]int `json:"providers,omitempty"`
}

type Selected struct {
	Rows    []*tend.Rec
	Next    *tend.Cursor // the last row's, when more follow
	Total   int          // rows in q.Scope()
	Matched int          // rows q picks
	Running int          // rows q picks that q.Live says run
	Facets  Facets
}

// Select places recs (rows nil: they are placed already), keeps those q picks, sorts them and cuts page; Total and
// Facets count q.Scope(). Trash and one-shot agent rows are List's only.
func Select(rows *Rows, recs []*tend.Rec, q tend.Query, page Page) Selected {
	if rows != nil {
		rows.Place(recs...)
	}
	scope := q.Scope()
	sel := Selected{Facets: Facets{Projects: map[string]int{}, Groups: map[string]int{}, Tags: map[string]int{}, Providers: map[string]int{}}}
	var picked []*tend.Rec
	for _, r := range recs {
		if !scope.Match(r) {
			continue
		}
		sel.Total++
		sel.Facets.add(r)
		if !q.Match(r) {
			continue
		}
		picked = append(picked, r)
		if q.Live != nil && q.Live(r.SessionID) {
			sel.Running++
		}
	}
	sel.Matched = len(picked)
	page.Sort.Sort(picked)
	from := 0
	if page.After != nil {
		from = sort.Search(len(picked), func(i int) bool { return tend.Less(page.Sort, *page.After, page.Sort.Cursor(picked[i])) })
	}
	to := len(picked)
	if page.Limit > 0 && from+page.Limit < to {
		to = from + page.Limit
		sel.Next = new(page.Sort.Cursor(picked[to-1]))
	}
	sel.Rows = picked[from:to]
	return sel
}

func (f Facets) add(r *tend.Rec) {
	f.Projects[r.ProjectID]++
	if r.ProjectID == "" && r.Project != "" {
		f.Groups[r.Project]++
	}
	for _, t := range r.Tags {
		f.Tags[t]++
	}
	if r.Provider != "" {
		f.Providers[r.Provider]++
	}
}
