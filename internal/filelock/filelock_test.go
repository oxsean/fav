package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockConflictsAcrossHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("TryLock while locked: %v, want ErrLocked", err)
	}
	if !Held(path) {
		t.Fatal("Held false while locked")
	}
	unlock()
	if Held(path) {
		t.Fatal("Held true after unlock")
	}
	unlock2, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after unlock: %v", err)
	}
	unlock2()
}

func TestHeldMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.lock")
	if Held(path) {
		t.Fatal("Held true for a missing file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Held created the file: %v", err)
	}
}
