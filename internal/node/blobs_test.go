package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
)

// fakeBlobs makes run id with n bytes of blobs, created at created, in state.
func fakeBlobs(t *testing.T, n *Node, id string, created time.Time, state string, size int) string {
	t.Helper()
	dir := n.runDir(id)
	if err := os.MkdirAll(filepath.Join(dir, blobsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	fileio.WriteJSON(filepath.Join(dir, "spec.json"), Spec{Run: id, Created: created})
	fileio.WriteJSON(filepath.Join(dir, "state.json"), State{State: state})
	os.WriteFile(filepath.Join(dir, blobsDir, strings.Repeat("a", 64)), make([]byte, size), 0o600)
	return dir
}

// Past the node's cap the oldest ended runs lose their blobs, marked gone, until the rest fits; a running run keeps
// its own however old.
func TestNodeBlobCap(t *testing.T) {
	defer func(c int64) { maxNodeBlobs = c }(maxNodeBlobs)
	maxNodeBlobs = 1000
	n := New(t.TempDir())
	now := time.Now()
	running := fakeBlobs(t, n, NewRunID(), now.Add(-3*time.Hour), StateRunning, 500)
	unlock, err := filelock.Lock(filepath.Join(running, "lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	oldest := fakeBlobs(t, n, NewRunID(), now.Add(-2*time.Hour), StateExited, 600)
	newer := fakeBlobs(t, n, NewRunID(), now.Add(-time.Hour), StateExited, 300)

	n.TrimBlobs()
	if fileExists(filepath.Join(oldest, blobsDir)) || !fileExists(filepath.Join(oldest, blobsGone)) {
		t.Fatal("the oldest ended run's blobs go")
	}
	for _, d := range []string{running, newer} {
		if !fileExists(filepath.Join(d, blobsDir)) || fileExists(filepath.Join(d, blobsGone)) {
			t.Fatalf("%s keeps its blobs", filepath.Base(d))
		}
	}
	if used, runs := n.BlobUse(); used != 800 || runs != 2 {
		t.Fatalf("use %d in %d runs", used, runs)
	}
}
