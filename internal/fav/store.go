package fav

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/i18n"
)

// Store is an append-only JSONL file: one full record per line, last line per id wins, Deleted is a tombstone; fully loaded, scanned linearly.
type Store struct {
	Path string
	recs []*Rec
	raw  int
	seen string // mtime+size after the last read or write
}

func Home() string {
	if h := os.Getenv("FAV_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agent", "fav")
}

func Open() (*Store, error) { return OpenAt(filepath.Join(Home(), "records.jsonl")) }

func OpenAt(path string) (*Store, error) {
	s := &Store{Path: path}
	return s, s.load()
}

func (s *Store) Changed() bool { return s.stamp() != s.seen }

func (s *Store) Reload() error { return s.load() }

func (s *Store) stamp() string {
	st, err := os.Stat(s.Path)
	if err != nil {
		return ""
	}
	return st.ModTime().String() + "/" + strconv.FormatInt(st.Size(), 10)
}

func (s *Store) load() error {
	s.recs, s.raw, s.seen = nil, 0, s.stamp()
	f, err := os.Open(s.Path)
	if os.IsNotExist(err) {
		s.recs = nil
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	latest := map[string]*Rec{}
	order := []string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.raw++
		var r Rec
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			fmt.Fprint(os.Stderr, i18n.F("store.skipped_line", n, err))
			continue
		}
		if !ValidStatus(r.Status) {
			r.Status = StatusDefault
		}
		if _, seen := latest[r.ID]; !seen {
			order = append(order, r.ID)
		}
		latest[r.ID] = &r
	}
	if err := sc.Err(); err != nil {
		return err
	}

	s.recs = make([]*Rec, 0, len(order))
	for _, id := range order {
		r := latest[id]
		if r.Deleted {
			continue
		}
		r.buildHay()
		s.recs = append(s.recs, r)
	}
	sort.SliceStable(s.recs, func(i, j int) bool {
		return s.recs[i].When().After(s.recs[j].When())
	})
	return nil
}

// ⚠️ callers must not modify the returned slice
func (s *Store) All() []*Rec { return s.recs }

func (s *Store) Get(id string) *Rec {
	for _, r := range s.recs {
		if r.ID == id {
			return r
		}
	}
	var hit *Rec
	for _, r := range s.recs {
		if strings.HasPrefix(r.ID, id) {
			if hit != nil {
				return nil // ambiguous prefix
			}
			hit = r
		}
	}
	return hit
}

// BySession: the session id is the idempotency key.
func (s *Store) BySession(provider, sessionID string) *Rec {
	for _, r := range s.recs {
		if r.Provider == provider && r.SessionID == sessionID {
			return r
		}
	}
	return nil
}

func (s *Store) Put(r *Rec) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return s.put(r)
}

// ⚠️ put: the caller holds the lock.
func (s *Store) put(r *Rec) error {
	if !ValidStatus(r.Status) {
		r.Status = StatusDefault
	}
	r.UpdatedAt = time.Now()
	if r.Schema == 0 {
		r.Schema = Schema
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	stale := s.Changed() // ⚠️ another process wrote since the last load: leave it for the next reload to see
	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	if !stale {
		s.seen = s.stamp()
	}

	r.buildHay()
	s.raw++
	for i, old := range s.recs {
		if old.ID != r.ID {
			continue
		}
		if r.Deleted {
			s.recs = append(s.recs[:i], s.recs[i+1:]...)
		} else {
			s.recs[i] = r
		}
		return nil
	}
	if !r.Deleted {
		s.recs = append([]*Rec{r}, s.recs...)
	}
	return nil
}

// Update reloads under the lock, applies change to the stored copy of r (by ID, else session) and saves it.
// ⚠️ A reload replaces every record object: callers holding records re-resolve them (Changed tells).
func (s *Store) Update(r *Rec, change func(*Rec)) (*Rec, error) {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return r, err
	}
	unlock, err := s.lock()
	if err != nil {
		return r, err
	}
	defer unlock()
	if s.Changed() {
		if err := s.load(); err != nil {
			return r, err
		}
	}
	if r.ID != "" {
		if r = s.Get(r.ID); r == nil {
			return nil, i18n.E("store.record_deleted")
		}
	} else if cur := s.BySession(r.Provider, r.SessionID); cur != nil {
		r = cur
	}
	fresh := r.ID == ""
	if fresh {
		r.ID = NewID()
	}
	change(r)
	if err := s.put(r); err != nil {
		if fresh {
			r.ID = ""
		}
		return r, err
	}
	return r, nil
}

func (s *Store) NeedsCompact() bool { return s.raw > 2*len(s.recs)+50 }

func (s *Store) RawLines() int { return s.raw }

// lock takes records.jsonl.lock, ⚠️ never the data file itself: Compact replaces its inode.
func (s *Store) lock() (func(), error) {
	return filelock.Lock(s.Path + ".lock")
}

// Compact rewrites atomically; ⚠️ hold the lock and reload first, or rows another process just appended are lost.
func (s *Store) Compact() error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.load(); err != nil {
		return err
	}
	err = fileio.WriteAtomic(s.Path, 0o600, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		for _, v := range slices.Backward(s.recs) {
			if err := enc.Encode(v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.raw = len(s.recs)
	s.seen = s.stamp()
	return nil
}
