package tend

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestPatchSetsEachFieldAndAgainChangesNothing(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	later := now.Add(time.Hour)
	for name, c := range map[string]struct {
		p     Patch
		check func(r *Rec) bool
	}{
		"favorite":   {Patch{Favorite: new(true)}, func(r *Rec) bool { return r.FavoritedAt != nil && r.FavoritedAt.Equal(now) }},
		"unfavorite": {Patch{Favorite: new(false)}, func(r *Rec) bool { return r.FavoritedAt == nil }},
		"archive":    {Patch{Archived: new(true)}, func(r *Rec) bool { return r.ArchivedAt != nil && r.ArchivedAt.Equal(now) }},
		"unarchive":  {Patch{Archived: new(false)}, func(r *Rec) bool { return r.ArchivedAt == nil }},
		"status":     {Patch{Status: new(StatusTodo)}, func(r *Rec) bool { return r.Status == StatusTodo }},
		"title":      {Patch{Title: new("  new title ")}, func(r *Rec) bool { return r.Title == "new title" }},
		"tags":       {Patch{Tags: new([]string{"#B", "a", "b"})}, func(r *Rec) bool { return reflect.DeepEqual(r.Tags, []string{"b", "a"}) }},
		"no tags":    {Patch{Tags: new([]string{})}, func(r *Rec) bool { return len(r.Tags) == 0 }},
		"summary":    {Patch{Summary: new("")}, func(r *Rec) bool { return r.Summary == "" }},
		"label":      {Patch{Label: new("tab")}, func(r *Rec) bool { return r.Label == "tab" }},
		"project":    {Patch{Project: new("shop")}, func(r *Rec) bool { return r.Project == "shop" }},
		"work type":  {Patch{WorkType: new("debug")}, func(r *Rec) bool { return r.WorkType == "debug" }},
	} {
		r := &Rec{ID: "x", Title: "t", Summary: "s", Status: StatusDoing, Tags: []string{"old"}}
		if name == "unfavorite" || name == "unarchive" {
			r.FavoritedAt, r.ArchivedAt = new(now.Add(-time.Hour)), new(now.Add(-time.Hour))
		}
		c.p.Apply(r, now)
		if !c.check(r) {
			t.Errorf("%s: %+v", name, r)
		}
		once := *r
		c.p.Apply(r, later)
		if !reflect.DeepEqual(*r, once) {
			t.Errorf("%s applied twice changed the record: %+v → %+v", name, once, *r)
		}
	}
}

func TestPatchKeepsAFavoriteTimeAndTakesAGivenOne(t *testing.T) {
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	now := then.AddDate(0, 1, 0)
	r := &Rec{ID: "x", FavoritedAt: new(then)}
	Patch{Favorite: new(true)}.Apply(r, now)
	if !r.FavoritedAt.Equal(then) {
		t.Errorf("favoriting a favorite moved its time to %v", r.FavoritedAt)
	}
	Patch{Favorite: new(true), FavoritedAt: new(now)}.Apply(r, then)
	if !r.FavoritedAt.Equal(now) {
		t.Errorf("a given time is the one recorded: %v", r.FavoritedAt)
	}
	Patch{FavoritedAt: new(then)}.Apply(r, now)
	if !r.FavoritedAt.Equal(now) {
		t.Errorf("a time without favorite changes nothing: %v", r.FavoritedAt)
	}
}

func TestPatchUndoPutsTheFieldsItSetBack(t *testing.T) {
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	before := &Rec{ID: "x", Title: "old", Summary: "sum", Tags: []string{"a"}, Status: StatusDone, FavoritedAt: new(then), Label: "l"}
	r := *before
	r.Tags = []string{"a"}
	p := Patch{Favorite: new(false), Archived: new(true), Status: new(StatusDoing), Title: new("new"), Tags: new([]string{"b"})}
	p.Apply(&r, then.AddDate(0, 0, 1))
	p.Undo(before).Apply(&r, then.AddDate(0, 0, 2))
	if r.Title != "old" || r.Status != StatusDone || !reflect.DeepEqual(r.Tags, []string{"a"}) || r.ArchivedAt != nil ||
		r.FavoritedAt == nil || !r.FavoritedAt.Equal(then) {
		t.Fatalf("undo left %+v", r)
	}
	if u := p.Undo(before); u.Summary != nil || u.Label != nil || u.Project != nil || u.WorkType != nil {
		t.Errorf("undo touches fields the patch did not: %+v", u)
	}
	fresh := &Rec{Provider: ProviderClaude, SessionID: "s"}
	fav := fresh.ToggleFavorite()
	if u := fav.Undo(fresh); u.Favorite == nil || *u.Favorite || u.FavoritedAt != nil {
		t.Errorf("undoing a first favorite unfavorites: %+v", u)
	}
}

func TestPatchEmptyAndJSON(t *testing.T) {
	if !(Patch{}).Empty() || !(Patch{FavoritedAt: new(time.Now())}).Empty() || (Patch{Summary: new("")}).Empty() {
		t.Error("Empty: a patch that sets nothing, a time alone sets nothing")
	}
	b, _ := json.Marshal(Patch{Favorite: new(false), Tags: new([]string{})})
	if string(b) != `{"favorite":false,"tags":[]}` {
		t.Errorf("a false and an empty list are sent, unset fields are not: %s", b)
	}
	var p Patch
	if err := json.Unmarshal([]byte(`{"title":"x","archived":true}`), &p); err != nil || *p.Title != "x" || !*p.Archived || p.Favorite != nil {
		t.Errorf("%+v %v", p, err)
	}
}

func TestToggles(t *testing.T) {
	r := &Rec{ID: "x", Status: StatusDone, FavoritedAt: new(time.Now())}
	if p := r.ToggleFavorite(); *p.Favorite {
		t.Error("a favorite toggles off")
	}
	if p := r.ToggleArchived(); !*p.Archived {
		t.Error("unarchived toggles on")
	}
	if p := r.ToggleStatus(StatusDone); *p.Status != StatusDoing {
		t.Error("done again goes back to doing")
	}
	if p := r.ToggleStatus(StatusTodo); *p.Status != StatusTodo {
		t.Error("another status is set")
	}
}

// TestPatchUndoSurvivesJSON: an undo sent over the wire (put) puts back empty values too, a list of no tags
// among them.
func TestPatchUndoSurvivesJSON(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	before := &Rec{ID: "x"}
	r := *before
	p := Patch{Favorite: new(true), Archived: new(true), Status: new(StatusDone), Title: new("t"), Tags: new([]string{"a"}),
		Summary: new("s"), Label: new("l"), Project: new("p"), WorkType: new("w")}
	p.Apply(&r, now)
	b, err := json.Marshal(p.Undo(before))
	if err != nil {
		t.Fatal(err)
	}
	var back Patch
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back.Apply(&r, now.Add(time.Hour))
	if len(r.Tags) != 0 {
		t.Errorf("tags after the undo: %v (sent %s)", r.Tags, b)
	}
	r.Tags = nil
	if !reflect.DeepEqual(r, *before) {
		t.Errorf("undo over JSON left %+v (sent %s)", r, b)
	}
}
