package node

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/oxsean/fav/internal/fileio"
)

// runBlobs is one run's blobs as the node counts them.
type runBlobs struct {
	dir     string
	bytes   int64
	created time.Time
	ended   bool
}

func (n *Node) blobRuns() []runBlobs {
	ents, _ := os.ReadDir(filepath.Join(n.Dir, "runs"))
	var out []runBlobs
	for _, e := range ents {
		if !runID.MatchString(e.Name()) {
			continue
		}
		dir := n.runDir(e.Name())
		b, count := dirBytes(filepath.Join(dir, blobsDir))
		if count == 0 {
			continue
		}
		var spec Spec
		readJSON(filepath.Join(dir, "spec.json"), &spec)
		s, err := n.Snapshot(e.Name())
		out = append(out, runBlobs{dir: dir, bytes: b, created: spec.Created,
			ended: err == nil && (Terminal(s.State.State) || s.State.State == StateUnknown)})
	}
	return out
}

// BlobUse is how many bytes of blobs the node's runs keep, and in how many runs.
func (n *Node) BlobUse() (int64, int) {
	var total int64
	runs := n.blobRuns()
	for _, r := range runs {
		total += r.bytes
	}
	return total, len(runs)
}

// BlobCaps are the most bytes of blobs a run keeps and all runs on the node keep.
func BlobCaps() (run, node int64) { return maxRunBlobs, maxNodeBlobs }

// TrimBlobs keeps the node's blobs under maxNodeBlobs: the oldest ended runs lose theirs first, marked blobs.gone so
// what needs them answers gone. A running run keeps its own.
func (n *Node) TrimBlobs() {
	runs := n.blobRuns()
	var total int64
	for _, r := range runs {
		total += r.bytes
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].created.Before(runs[j].created) })
	for _, r := range runs {
		if total <= maxNodeBlobs {
			return
		}
		if !r.ended {
			continue
		}
		// ⚠️ the mark first: a read between the two sees gone, never a missing blob it takes for a bad ref
		if fileio.WriteFile(filepath.Join(r.dir, blobsGone), nil, 0o600) != nil {
			continue
		}
		if os.RemoveAll(filepath.Join(r.dir, blobsDir)) == nil {
			total -= r.bytes
		}
	}
}
