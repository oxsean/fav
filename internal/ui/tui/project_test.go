package tui

import (
	"path/filepath"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oxsean/fav/internal/fav"
)

func TestAutoOpenProjectOfStartDir(t *testing.T) {
	s := fixture(t)
	dir := t.TempDir()
	for _, r := range s.All() {
		if r.Project == "webapp" {
			r.Cwd = filepath.Join(dir, "webapp")
			if err := s.Put(r); err != nil {
				t.Fatal(err)
			}
		}
	}
	m := New(s, noIndex(t), fav.DefaultConfig(), "")
	m.startDir = filepath.Join(dir, "webapp", "internal", "api")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.setView(viewProjects)
	if !m.open["webapp"] || m.open["notes-api"] {
		t.Fatalf("only the start directory's group should open: %v", m.open)
	}
	if r := m.rows[m.cursor]; r.group != "webapp" {
		t.Fatalf("cursor should sit on the group header, got %+v", r)
	}
	if m.scroll != 0 { // everything fits: no blank space below
		t.Fatalf("short list must not scroll, got %d", m.scroll)
	}
	m.autoOpened, m.open = false, map[string]bool{}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 11}) // list shorter than the rows
	m.setView(viewFavorites)
	m.setView(viewProjects)
	if m.rows[m.cursor].group != "webapp" || m.scroll != m.rowTop(m.cursor) || m.scroll == 0 {
		t.Fatalf("header should be the first visible line: scroll=%d top=%d", m.scroll, m.rowTop(m.cursor))
	}
	m.open["webapp"] = false
	m.refresh()
	if m.open["webapp"] {
		t.Fatal("must not reopen after the user collapsed it")
	}
}

func TestFocusOpensHiddenSession(t *testing.T) {
	m := sized(t, 150, 44)
	r := &fav.Rec{Provider: fav.ProviderClaude, SessionID: "zz-hidden", Title: "sdk agent", Status: fav.StatusDoing}
	m.Focus(r)
	if m.view != viewSessions || m.pane != paneChat {
		t.Fatalf("view=%v pane=%v", m.view, m.pane)
	}
	if m.current() != r {
		t.Fatalf("cursor should sit on the opened session, got %v", m.current())
	}
	m.refresh()
	if m.current() != r {
		t.Fatal("the opened session must survive a refresh")
	}
}

func TestProjectGroupOrder(t *testing.T) {
	recs := []*fav.Rec{
		{Project: "zeta"}, {Project: "alpha"}, {Project: "alpha"}, {Project: "alpha"}, {Project: "mid"}, {Project: "mid"},
	}
	names := func(rows []row) []string {
		var out []string
		for _, r := range rows {
			if r.group != "" {
				out = append(out, r.group)
			}
		}
		return out
	}
	for _, tc := range []struct {
		mode string
		want []string
	}{
		{projSortActive, []string{"zeta", "alpha", "mid"}}, // the record order decides
		{projSortCount, []string{"alpha", "mid", "zeta"}},
		{projSortName, []string{"alpha", "mid", "zeta"}},
	} {
		rows, groups := projectRows(recs, map[string]bool{}, tc.mode)
		if got := names(rows); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v want %v", tc.mode, got, tc.want)
		}
		if len(groups["alpha"]) != 3 {
			t.Errorf("%s: the group map must keep every record", tc.mode)
		}
	}
}
