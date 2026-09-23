package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/testkit"
)

// brokenMachine: a stale transcript path (Attach repairs it), none, two transcripts gone (same prefix), a cwd gone.
func brokenMachine(t *testing.T) (root string, s *fav.Store) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("FAV_HOME", filepath.Join(root, "fav"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o755)
	tr := filepath.Join(index.ClaudeProjectDir(work), "aaaa0001-real.jsonl")
	os.MkdirAll(filepath.Dir(tr), 0o755)
	os.WriteFile(tr, []byte(`{"type":"user","timestamp":"2026-08-01T01:00:00Z","cwd":`+testkit.JSONString(work)+`,"message":{"content":"real"}}`+"\n"), 0o644)
	s, err := fav.Open()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*fav.Rec{
		{SessionID: "aaaa0001-real", Title: "real", Cwd: work, TranscriptPath: filepath.Join(root, "old", "aaaa0001-real.jsonl")},
		{SessionID: "bbbb0002-nopath", Title: "no path", Cwd: work},
		{SessionID: "cccc0003-gone", Title: "gone one", Cwd: work, TranscriptPath: filepath.Join(root, "old", "c3.jsonl")},
		{SessionID: "cccc0005-gone", Title: "gone two", Cwd: work, TranscriptPath: filepath.Join(root, "old", "c5.jsonl")},
		{SessionID: "dddd0004-dir", Title: "moved", Cwd: filepath.Join(root, "moved", "proj"), TranscriptPath: filepath.Join(root, "old", "d4.jsonl")},
	} {
		r.ID, r.Provider, r.Status = fav.NewID(), fav.ProviderClaude, fav.StatusDone
		if err := s.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	return root, s
}

func sids(list []broken) []string {
	var out []string
	for _, b := range list {
		out = append(out, b.rec.SessionID)
	}
	return out
}

func TestScanBrokenJudgesAttachedRecords(t *testing.T) {
	_, s := brokenMachine(t)
	idx, _ := index.Open()
	idx, _ = idx.Refresh()
	got := sids(scanBroken(s, idx, nil, ""))
	if want := []string{"dddd0004-dir", "cccc0003-gone", "cccc0005-gone"}; !slices.Equal(got, want) {
		t.Fatalf("broken = %v, want %v (a repaired path or no path at all is not broken)", got, want)
	}
}

func TestBrokenArgsPickWhatIsTrashed(t *testing.T) {
	root, _ := brokenMachine(t)
	all := []string{"dddd0004-dir", "cccc0003-gone", "cccc0005-gone"}
	for _, c := range []struct {
		args      []string
		list, sel []string
	}{
		{nil, all, nil},
		{[]string{"cccc0003"}, all, []string{"cccc0003"}},
		{[]string{"cccc"}, nil, nil}, // two hits: a keyword, which matches no title
		{[]string{"gone"}, all[1:], nil},
		{[]string{filepath.Join(root, "moved")}, all[:1], nil},
		{[]string{"2", "all"}, all, []string{"2", "all"}},
	} {
		_, _, list, sel, err := loadPick(c.args)
		if err != nil || !slices.Equal(sids(list), c.list) || !slices.Equal(sel, c.sel) {
			t.Errorf("%v: list %v sel %v err %v, want %v %v", c.args, sids(list), sel, err, c.list, c.sel)
		}
	}
	if _, _, _, _, err := loadPick([]string{"cccc0009"}); err == nil {
		t.Error("a full-length id that matches nothing is a typo, not a keyword")
	}

	_, _, list, _, _ := loadPick(nil)
	for _, c := range []struct {
		sel  []string
		want []string
	}{
		{[]string{"2"}, all[1:2]},
		{[]string{"cccc0005", "1"}, []string{"cccc0005-gone", "dddd0004-dir"}},
		{[]string{list[2].rec.ID}, all[2:]},
		{[]string{"1", "all"}, all},
		{[]string{"2", "2", "cccc0003"}, all[1:2]},
	} {
		if got, err := pickBroken(list, c.sel); err != nil || !slices.Equal(sids(got), c.want) {
			t.Errorf("pick %v = %v %v, want %v", c.sel, sids(got), err, c.want)
		}
	}
	for _, bad := range []string{"4", "0", "cccc", "eeee"} {
		if _, err := pickBroken(list, []string{bad}); err == nil {
			t.Errorf("pick %q must fail", bad)
		}
	}
}

func TestCleanAllTrashesOnlyTheBroken(t *testing.T) {
	brokenMachine(t)
	if out := stdoutOf(t, func() {
		if err := run([]string{"clean", "all", "-y"}); err != nil {
			t.Error(err)
		}
	}); out == "" {
		t.Fatal("clean reports what it did")
	}
	s, _ := fav.Open()
	var kept []string
	for _, r := range s.All() {
		kept = append(kept, r.SessionID)
	}
	slices.Sort(kept)
	if want := []string{"aaaa0001-real", "bbbb0002-nopath"}; !slices.Equal(kept, want) {
		t.Errorf("records left %v, want %v", kept, want)
	}
	if entries, _ := fav.LoadTrash(); len(entries) != 3 {
		t.Errorf("three trash entries: %v", entries)
	}
}

func TestRmWithoutTTYNeedsYes(t *testing.T) {
	brokenMachine(t)
	r, w, _ := os.Pipe()
	w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	if err := run([]string{"rm", "aaaa0001"}); err == nil {
		t.Fatal("no terminal to ask and no -y: an error, not a silent no")
	}
	s, _ := fav.Open()
	if s.BySession(fav.ProviderClaude, "aaaa0001-real") == nil {
		t.Fatal("nothing is trashed")
	}
}
