package fav

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/filelock"
)

func homeIn(t *testing.T) (tend, old string) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("FAV_HOME", "")
	t.Setenv("TEND_HOME", "")
	return DefaultHomes()
}

func put(t *testing.T, p, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func tarNames(t *testing.T, p string) []string {
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var out []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name)
	}
}

func TestMigrateMovesTheOldHomeAndMovesAnOlderTendAside(t *testing.T) {
	tend, old := homeIn(t)
	put(t, filepath.Join(old, "records.jsonl"), "{}\n")
	put(t, filepath.Join(old, "text", "big.txt"), "mirror")
	put(t, filepath.Join(tend, "events.jsonl"), "old tend\n")
	if Home() != old {
		t.Fatalf("before: %s", Home())
	}
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)
	m, err := MigrateHome(now)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(m.OldTend, "events.jsonl")); string(b) != "old tend\n" || filepath.Base(m.OldTend) != "tend-old-20260925" {
		t.Fatalf("the older TEND moved aside intact: %+v", m)
	}
	if b, _ := os.ReadFile(filepath.Join(tend, "records.jsonl")); string(b) != "{}\n" || !exists(filepath.Join(tend, HomeMarker)) {
		t.Fatal("the home moved and is marked")
	}
	if Home() != tend {
		t.Fatalf("after: %s", Home())
	}
	names := tarNames(t, m.Backup)
	if !slices.Contains(names, "fav/records.jsonl") || slices.ContainsFunc(names, func(n string) bool { return filepath.Dir(n) == "fav/text" }) {
		t.Fatalf("backup: %v", names)
	}
	if m.LinkErr == nil {
		if b, _ := os.ReadFile(filepath.Join(old, "records.jsonl")); string(b) != "{}\n" {
			t.Fatal("the old path still reaches the data")
		}
	}
	again, err := MigrateHome(now)
	if err != nil || !again.Done {
		t.Fatalf("a second run does nothing: %+v %v", again, err)
	}
}

func TestMigrateRefusesWhileTheHomeIsInUse(t *testing.T) {
	tend, old := homeIn(t)
	put(t, filepath.Join(old, "records.jsonl"), "{}\n")
	unlock, err := filelock.TryLock(filepath.Join(old, "records.jsonl.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := MigrateHome(time.Now()); !errors.Is(err, ErrHomeBusy) {
		t.Fatalf("%v", err)
	}
	if exists(tend) || !exists(filepath.Join(old, "records.jsonl")) {
		t.Fatal("nothing moved")
	}
}

func TestMigrateOnANewMachineMarksTheHome(t *testing.T) {
	tend, _ := homeIn(t)
	m, err := MigrateHome(time.Now())
	if err != nil || m.Marked != tend || Home() != tend {
		t.Fatalf("%+v %v %s", m, err, Home())
	}
	put(t, filepath.Join(tend, "records.jsonl"), "{}\n")
	os.Remove(filepath.Join(tend, HomeMarker))
	if m, err := MigrateHome(time.Now()); err != nil || m.OldTend != "" || m.Marked != tend {
		t.Fatalf("a home of ours without the marker is marked, not moved aside: %+v %v", m, err)
	}
}
