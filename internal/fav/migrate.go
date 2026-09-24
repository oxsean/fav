package fav

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
)

// Migration is what MigrateHome did.
type Migration struct {
	Done    bool   // already migrated: nothing changed
	OldTend string // where an older TEND's ~/.agent/tend went
	Backup  string // the backup of ~/.agent/fav
	Moved   string // ~/.agent/fav's new place
	Marked  string // a directory marked as this program's home without moving anything
	LinkErr error  // ~/.agent/fav could not be left as a link to the new place (Home still finds it)
}

// ErrHomeBusy: another process holds a lock in the home.
var ErrHomeBusy = errors.New("in use")

// MigrateHome moves ~/.agent/fav to ~/.agent/tend: an unmarked ~/.agent/tend (an older TEND) is renamed out of the
// way first, the old home is backed up to a tar.gz (without the rebuildable text mirror and ssh sockets), renamed,
// marked, and left behind as a link. It refuses while a lock in the old home is held.
func MigrateHome(now time.Time) (Migration, error) {
	var m Migration
	tend, old := DefaultHomes()
	marked := exists(filepath.Join(tend, HomeMarker))
	oldReal := realDir(old)
	switch {
	case marked && oldReal:
		return m, fmt.Errorf("both %s and %s hold data", tend, old)
	case marked:
		m.Done = true
		return m, nil
	}
	if exists(tend) {
		if !oldReal && ours(tend) {
			return m, markHome(tend, &m, "")
		}
		m.OldTend = unique(filepath.Join(filepath.Dir(tend), "tend-old-"+now.Format("20060102")))
		if err := os.Rename(tend, m.OldTend); err != nil {
			return m, err
		}
	}
	if !oldReal {
		if err := os.MkdirAll(tend, 0o700); err != nil {
			return m, err
		}
		return m, markHome(tend, &m, "")
	}
	if busy := heldLocks(old); len(busy) > 0 {
		return m, fmt.Errorf("%w: %s", ErrHomeBusy, strings.Join(busy, ", "))
	}
	m.Backup = unique(filepath.Join(filepath.Dir(old), "fav-backup-"+now.Format("20060102-150405")+".tar.gz"))
	if err := backup(old, m.Backup); err != nil {
		os.Remove(m.Backup)
		m.Backup = ""
		return m, err
	}
	if err := os.Rename(old, tend); err != nil {
		return m, err
	}
	m.Moved = tend
	if err := markHome(tend, &m, old); err != nil {
		return m, err
	}
	m.Marked = ""
	m.LinkErr = fileio.LinkDir(tend, old)
	return m, nil
}

func markHome(dir string, m *Migration, from string) error {
	note := "tend home\n"
	if from != "" {
		note = fmt.Sprintf("tend home, moved from %s at %s\n", from, time.Now().Format(time.RFC3339))
	}
	m.Marked = dir
	return fileio.WriteFile(filepath.Join(dir, HomeMarker), []byte(note), 0o600)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// realDir: p is a directory itself, not a link or junction to one.
func realDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir() && fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0
}

// ours: dir holds this program's files (a home made before the marker existed), not an older TEND's.
func ours(dir string) bool {
	for _, n := range []string{"records.jsonl", "sessions.jsonl", "config.json", "coord", "node", "hosts"} {
		if exists(filepath.Join(dir, n)) {
			return true
		}
	}
	return false
}

func unique(p string) string {
	if !exists(p) {
		return p
	}
	base, ext := p, ""
	if strings.HasSuffix(p, ".tar.gz") {
		base, ext = strings.TrimSuffix(p, ".tar.gz"), ".tar.gz"
	}
	for i := 2; ; i++ {
		if q := fmt.Sprintf("%s-%d%s", base, i, ext); !exists(q) {
			return q
		}
	}
}

// heldLocks are the locks in home another process holds.
func heldLocks(home string) []string {
	cands := []string{filepath.Join(home, "records.jsonl.lock"), filepath.Join(home, "text", ".lock"),
		filepath.Join(home, "coord", "lock"), filepath.Join(home, "trash", "manifest.jsonl.lock")}
	runs, _ := filepath.Glob(filepath.Join(home, "node", "runs", "*", "lock"))
	var out []string
	for _, p := range append(cands, runs...) {
		if filelock.Held(p) {
			out = append(out, p)
		}
	}
	return out
}

// skipInBackup: the full-text mirror is rebuilt from the transcripts; ssh control sockets are not files.
func skipInBackup(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range []string{"text", "hosts/ssh"} {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// backup writes dir's regular files and directories to a gzipped tar at dst.
func backup(dir, dst string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	tw := tar.NewWriter(zw)
	root := filepath.Base(dir)
	werr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel != "." && skipInBackup(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil // sockets, links
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		h, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(filepath.Join(root, rel))
		if d.IsDir() {
			h.Name += "/"
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.CopyN(tw, src, h.Size) // ⚠️ exactly the header's size: a file growing meanwhile would break the archive
		return err
	})
	for _, c := range []io.Closer{tw, zw, f} {
		if err := c.Close(); err != nil && werr == nil {
			werr = err
		}
	}
	return werr
}
