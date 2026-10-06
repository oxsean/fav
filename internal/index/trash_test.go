package index

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/testkit"
)

func sessionIDs(idx *Index) []string {
	var out []string
	for _, s := range idx.Sessions() {
		out = append(out, s.SessionID)
	}
	return out
}

// TestTrashSessionMovesItsFilesAndForgetsThem: the files go to the trash, the record is tombstoned, the index handed
// back no longer has them while the one passed in is left as it was; restoring brings them back for the next refresh,
// and the trash keeps the session readable meanwhile.
func TestTrashSessionMovesItsFilesAndForgetsThem(t *testing.T) {
	claude, _ := setup(t)
	t.Setenv("TEND_HOME", t.TempDir())
	path := filepath.Join(claude, "projects", "-Users-me-work-webapp", "s-trash.jsonl")
	write(t, path, claudeLines("first", "second", "third"))
	write(t, filepath.Join(claude, "projects", "-Users-me-work-webapp", "s-kept.jsonl"), claudeLines("a", "b", "c"))
	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	store, _ := tend.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	r := &tend.Rec{ID: tend.NewID(), Provider: tend.ProviderClaude, SessionID: "s-trash", Title: "to trash", Cwd: "/Users/me/work/webapp",
		FavoritedAt: new(time.Now()), Status: tend.StatusDefault}
	if err := store.Put(r); err != nil {
		t.Fatal(err)
	}

	e, next, err := TrashSession(store, idx, r)
	if err != nil || len(e.Files) != 1 || e.Title != "to trash" {
		t.Fatalf("trashed: %+v %v", e, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the transcript left its place: %v", err)
	}
	if got := store.Get(r.ID); got != nil && !got.Deleted {
		t.Fatalf("the record is tombstoned: %+v", got)
	}
	if ids := sessionIDs(next); !slices.Equal(ids, []string{"s-kept"}) {
		t.Fatalf("the index handed back forgot the session: %v", ids)
	}
	if ids := sessionIDs(idx); !slices.Contains(ids, "s-trash") {
		t.Fatalf("the index passed in is unchanged: %v", ids)
	}
	tr, err := Trashed(tend.ProviderClaude, "s-trash")
	if err != nil || tr == nil || tr.Title != "to trash" || tr.TranscriptPath != e.Transcript() || tr.Deleted {
		t.Fatalf("the trashed record reads from the trash: %+v %v", tr, err)
	}
	if tr, err := Trashed(tend.ProviderClaude, "s-kept"); tr != nil || err != nil {
		t.Fatalf("a session not in the trash: %+v %v", tr, err)
	}

	back, again, err := RestoreSession(store, next, tend.ProviderClaude, "s-trash")
	if err != nil || back.Title != "to trash" || again != next {
		t.Fatalf("restored: %+v %v (nothing to force: the index is the one passed in)", back, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the transcript is back: %v", err)
	}
	if got := store.BySession(tend.ProviderClaude, "s-trash"); got == nil || got.Deleted {
		t.Fatalf("the record is live again: %+v", got)
	}
	if refreshed, _ := again.Refresh(); !slices.Contains(sessionIDs(refreshed), "s-trash") {
		t.Fatalf("the next refresh lists it: %v", sessionIDs(refreshed))
	}
	if _, _, err := RestoreSession(store, again, tend.ProviderClaude, "s-trash"); err == nil {
		t.Fatal("a session no longer in the trash")
	}
}

// TestTrashSessionWithoutARecordOrIndex: a session the store has no record of moves its files and leaves no record;
// without an index there is none to hand back.
func TestTrashSessionWithoutARecordOrIndex(t *testing.T) {
	claude, _ := setup(t)
	t.Setenv("TEND_HOME", t.TempDir())
	path := filepath.Join(claude, "projects", "-Users-me-work-webapp", "s-bare.jsonl")
	write(t, path, claudeLines("one", "two", "three"))
	store, _ := tend.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	r := &tend.Rec{Provider: tend.ProviderClaude, SessionID: "s-bare", Title: "bare"}
	e, next, err := TrashSession(store, nil, r)
	if err != nil || next != nil || len(e.Files) != 1 || len(store.All()) != 0 {
		t.Fatalf("%+v %v %v %d", e, next, err, len(store.All()))
	}
	if _, _, err := RestoreSession(store, nil, tend.ProviderClaude, "s-bare"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("back: %v", err)
	}
}

// TestRestoreSessionForgetsWhatItMustReadAgain: a file restored over one a project move rewrote may keep its size and
// time, so the index handed back forgets it and the next plain refresh reads it from scratch (rescanAfterRestore).
func TestRestoreSessionForgetsWhatItMustReadAgain(t *testing.T) {
	testkit.PosixOnly(t)
	_, codex := setup(t)
	t.Setenv("TEND_HOME", t.TempDir())
	old, moved := "/Users/me/work/p", "/Users/me/work/q"
	c1 := rolloutPath(codex, 11, "c1c1")
	write(t, c1, codexLines("c1c1", old, "codex_cli_rs", "活"))
	idx, _ := OpenAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	idx, _ = idx.Refresh()
	store, _ := tend.OpenAt(filepath.Join(t.TempDir(), "records.jsonl"))
	plan, _ := idx.PlanMove(store, nil, old, moved)
	rep, err := plan.Apply(store)
	if err != nil {
		t.Fatal(err)
	}
	idx, _ = idx.Rescan(rep.Touched)
	_, next, err := RestoreSession(store, idx, tend.ProviderCodex, "c1c1")
	if err != nil || next == idx {
		t.Fatalf("a forced file makes a new index: %v", err)
	}
	if refreshed, _ := next.Refresh(); refreshed.files[c1] == nil || refreshed.files[c1].Cwd != old {
		t.Fatalf("read again from scratch: %+v", refreshed.files[c1])
	}
}
