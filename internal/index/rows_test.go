package index

import (
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/tend"
)

// TestRowsPlaceEachRowBeforeMatching: Belong names each row's project before the query runs, so project: picks by
// where a session belongs; rows of no project are left with none.
func TestRowsPlaceEachRowBeforeMatching(t *testing.T) {
	s, err := tend.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"/src/shop/web", "/src/notes"} {
		r := &tend.Rec{ID: tend.NewID(), Provider: tend.ProviderClaude, SessionID: d, Title: d, Cwd: d, Project: filepath.Base(d),
			FavoritedAt: new(time.Now()), Status: tend.StatusDefault}
		if err := s.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	unfav := []*tend.Rec{{Provider: tend.ProviderCodex, SessionID: "u", Title: "u", Cwd: "/src/shop", Project: "shop", Turns: 9}}
	live := map[string]capture.Live{"l": {Agent: tend.ProviderClaude, Cwd: "/src/shop/api", Title: "running"}}
	rows := Rows{Belong: func(r *tend.Rec) (string, string) {
		if path.Dir(r.Cwd) == "/src/shop" || r.Cwd == "/src/shop" {
			return "p_shop", "Shop"
		}
		return "", ""
	}}
	idx := &Index{}
	q := tend.Parse("project:p_shop status:all")
	q.All = true
	got, err := rows.List(s, idx, unfav, live, q)
	if err != nil || len(got) != 2 {
		t.Fatalf("project:p_shop lists %d rows (%v), want the stored one and the unfavorited one", len(got), err)
	}
	q = tend.Parse("status:live project:shop")
	q.All = true
	if got, _ := rows.List(s, idx, unfav, live, q); len(got) != 1 || got[0].ProjectID != "p_shop" || got[0].Group() != "Shop" {
		t.Fatalf("the unindexed running session: %+v", got)
	}
	notes := s.Query(tend.Parse("project:notes"))
	if len(notes) != 1 || notes[0].ProjectID != "" {
		t.Fatalf("a session of no project keeps its automatic group: %+v", notes)
	}
	far := &tend.Rec{Host: "mba", Cwd: "/src/shop/x"}
	rows.Place(far)
	if far.ProjectID != "p_shop" {
		t.Errorf("Place left another machine's row without its project")
	}
	rows.Belong = nil
	rows.Place(far)
	if far.ProjectID != "" || far.ProjectName != "" {
		t.Errorf("without Belong a row is in no project")
	}
}

// Copies hangs this machine's migrations on its own rows only: another machine's row keeps the ones its machine sent.
func TestRowsCopiesMarkThisMachinesRows(t *testing.T) {
	away := []tend.Copy{{Migration: "m1", Role: "to", State: "done", Peer: "mba"}}
	rows := Rows{Copies: func(r *tend.Rec) []tend.Copy {
		if r.SessionID == "s1" {
			return away
		}
		return nil
	}}
	here, other := &tend.Rec{SessionID: "s1", Copies: []tend.Copy{{Migration: "old"}}}, &tend.Rec{SessionID: "s2"}
	far := &tend.Rec{SessionID: "s1", Host: "mba", Copies: []tend.Copy{{Migration: "m1", Role: "from"}}}
	rows.Place(here, other, far)
	if len(here.Copies) != 1 || here.Copies[0].Migration != "m1" || other.Copies != nil {
		t.Errorf("this machine's rows: %+v %+v", here.Copies, other.Copies)
	}
	if len(far.Copies) != 1 || far.Copies[0].Role != "from" {
		t.Errorf("another machine's row lost what its machine sent: %+v", far.Copies)
	}
	sent := &tend.Rec{SessionID: "s1", Copies: away}
	(&Rows{}).Place(sent)
	if len(sent.Copies) != 1 {
		t.Errorf("without the hook a row keeps what it carries: %+v", sent.Copies)
	}
}
