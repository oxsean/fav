package tend

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
)

// KindMemory is a trashed memory file or directory; a session's entry has no kind.
const KindMemory = "memory"

// TrashEntry is a trashed session or memory: where the files went, plus the record as it was.
type TrashEntry struct {
	Kind      string    `json:"kind,omitempty"`
	Provider  string    `json:"provider"`
	SessionID string    `json:"session_id"`
	Title     string    `json:"title"`
	Cwd       string    `json:"cwd,omitempty"`
	Record    *Rec      `json:"record,omitempty"`
	Files     []Moved   `json:"files"`
	DeletedAt time.Time `json:"deleted_at"`
	Dir       string    `json:"dir,omitempty"`  // a fresh directory every time, so re-trashing the same session never overwrites the last one
	Line      string    `json:"line,omitempty"` // a memory's MEMORY.md line, taken out with it and put back on restore
}

type Moved struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Replaced  string `json:"replaced,omitempty"`  // files a project move rewrote from the originals; deleted on restore
	Stamp     string `json:"stamp,omitempty"`     // size+mtime of Replaced right after the rewrite; a mismatch on restore means it was used since, refuse
	Relocated bool   `json:"relocated,omitempty"` // outside the trash: the new location (Claude's <sid>/ dir); restore moves it back
}

func FileStamp(p string) string {
	st, err := os.Stat(p)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(st.Size(), 10) + ":" + strconv.FormatInt(st.ModTime().UnixNano(), 10)
}

// Transcript is the first .jsonl in the entry; preview and chat read from it.
func (e TrashEntry) Transcript() string {
	for _, f := range e.Files {
		if filepath.Ext(f.To) == ".jsonl" {
			return f.To
		}
	}
	return ""
}

func TrashDir() string      { return filepath.Join(Home(), "trash") }
func trashManifest() string { return filepath.Join(TrashDir(), "manifest.jsonl") }
func (e TrashEntry) dir() string {
	if e.Dir != "" {
		return e.Dir
	}
	return filepath.Join(TrashDir(), e.Provider, e.SessionID)
}

// LoadTrash lists the trashed sessions.
func LoadTrash() ([]TrashEntry, error) {
	return loadTrash(func(e TrashEntry) bool { return e.Kind == "" })
}

// MemoryTrash lists the trashed memories.
func MemoryTrash() ([]TrashEntry, error) {
	return loadTrash(func(e TrashEntry) bool { return e.Kind == KindMemory })
}

// MemoryID names a trashed memory: its directory in the trash.
func (e TrashEntry) MemoryID() string { return filepath.Base(e.Dir) }

func loadTrash(keep func(TrashEntry) bool) ([]TrashEntry, error) {
	f, err := os.Open(trashManifest())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []TrashEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e TrashEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && (e.SessionID != "" || e.Kind == KindMemory && e.Dir != "") && keep(e) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt.After(out[j].DeletedAt) })
	return out, sc.Err()
}

func saveTrash(entries []TrashEntry) error {
	return fileio.WriteAtomic(trashManifest(), 0o600, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		for _, e := range entries {
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
		return nil
	})
}

// errUnchanged from a change: nothing to save.
var errUnchanged = errors.New("unchanged")

// ⚠️ editTrash loads, changes and saves the manifest under its lock (TUI and CLI both write it); after runs locked.
func editTrash(change func([]TrashEntry) ([]TrashEntry, error), after ...func()) error {
	if err := os.MkdirAll(TrashDir(), 0o700); err != nil {
		return err
	}
	unlock, err := filelock.Lock(trashManifest() + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	entries, err := loadTrash(func(TrashEntry) bool { return true })
	if err != nil {
		return err
	}
	if entries, err = change(entries); err != nil {
		if err == errUnchanged {
			return nil
		}
		return err
	}
	if err := saveTrash(entries); err != nil {
		return err
	}
	for _, f := range after {
		f()
	}
	return nil
}

// MoveToTrash moves paths into the trash under one entry per session: with nothing to move an earlier entry stays;
// otherwise it is replaced and its files move under the new entry, to be purged with it.
func MoveToTrash(e TrashEntry, paths []string) (TrashEntry, error) {
	var earlier, dir string
	var moved []Moved
	err := editTrash(func(entries []TrashEntry) ([]TrashEntry, error) {
		same := slices.IndexFunc(entries, func(x TrashEntry) bool { return x.sameSession(e) })
		if same >= 0 && !slices.ContainsFunc(paths, present) {
			if e.Record != nil {
				entries[same].Record = e.Record
			}
			e = entries[same]
			return entries, nil
		}
		e.DeletedAt = time.Now()
		e.Dir = filepath.Join(TrashDir(), e.Provider, e.SessionID+"-"+strconv.FormatInt(e.DeletedAt.UnixNano(), 36))
		if err := os.MkdirAll(e.Dir, 0o700); err != nil {
			return nil, err
		}
		dir = e.Dir
		for i, p := range paths {
			if !present(p) {
				continue
			}
			to := filepath.Join(e.Dir, strconv.Itoa(i)+"-"+filepath.Base(p))
			if err := moveAny(p, to); err != nil {
				return nil, err
			}
			moved = append(moved, Moved{From: p, To: to})
		}
		e.Files = moved
		if same < 0 {
			return append([]TrashEntry{e}, entries...), nil
		}
		earlier = entries[same].dir()
		return append([]TrashEntry{e}, slices.Delete(entries, same, same+1)...), nil
	}, func() {
		if earlier != "" {
			os.Rename(earlier, filepath.Join(e.Dir, "earlier"))
		}
	})
	if err != nil && dir != "" { // ⚠️ nothing may stay in a directory no entry owns: the purge would delete it
		for _, f := range slices.Backward(moved) {
			moveAny(f.To, f.From)
		}
		os.Remove(dir)
	}
	return e, err
}

// present: ⚠️ Lstat, so a dangling link still counts and moves.
func present(p string) bool { _, err := os.Lstat(p); return err == nil }

// SaveTrashEntry replaces the entry of the same session (a project move calls MoveToTrash first, then records the rewritten files).
func SaveTrashEntry(e TrashEntry) error {
	return editTrash(func(entries []TrashEntry) ([]TrashEntry, error) {
		for i := range entries {
			if entries[i].sameSession(e) {
				entries[i] = e
				return entries, nil
			}
		}
		return append([]TrashEntry{e}, entries...), nil
	})
}

func (e TrashEntry) sameSession(x TrashEntry) bool {
	return e.Kind == "" && x.Kind == "" && e.Provider == x.Provider && e.SessionID == x.SessionID
}

// TrashMemory moves a memory file or directory into the trash under an entry of its own.
func TrashMemory(e TrashEntry, path string) (TrashEntry, error) {
	e.Kind = KindMemory
	err := editTrash(func(entries []TrashEntry) ([]TrashEntry, error) {
		e.DeletedAt = time.Now()
		e.Dir = filepath.Join(TrashDir(), KindMemory, filepath.Base(path)+"-"+strconv.FormatInt(e.DeletedAt.UnixNano(), 36))
		if err := os.MkdirAll(e.Dir, 0o700); err != nil {
			return nil, err
		}
		to := filepath.Join(e.Dir, filepath.Base(path))
		if err := moveAny(path, to); err != nil {
			os.Remove(e.Dir)
			return nil, err
		}
		e.Files = []Moved{{From: path, To: to}}
		return append([]TrashEntry{e}, entries...), nil
	})
	return e, err
}

// RestoreMemory puts a trashed memory back, never over a file that took its place, and drops its entry.
func RestoreMemory(id string) (TrashEntry, error) {
	var e TrashEntry
	err := editTrash(func(entries []TrashEntry) ([]TrashEntry, error) {
		i := slices.IndexFunc(entries, func(x TrashEntry) bool { return x.Kind == KindMemory && x.MemoryID() == id })
		if i < 0 {
			return nil, errors.New("not in trash")
		}
		e = entries[i]
		for _, f := range e.Files {
			if present(f.From) {
				return nil, errors.New(f.From + " exists")
			}
		}
		for _, f := range e.Files {
			if err := os.MkdirAll(filepath.Dir(f.From), 0o700); err != nil {
				return nil, err
			}
			if err := moveAny(f.To, f.From); err != nil {
				return nil, err
			}
		}
		os.Remove(e.Dir)
		return slices.Delete(entries, i, i+1), nil
	})
	return e, err
}

// RestoreTrash puts the session back (recreating the original directory if needed), drops the entry and deletes files a project move rewrote.
func RestoreTrash(provider, sessionID string) (TrashEntry, error) {
	var e TrashEntry
	err := editTrash(func(entries []TrashEntry) ([]TrashEntry, error) {
		i := slices.IndexFunc(entries, func(x TrashEntry) bool { return x.sameSession(TrashEntry{Provider: provider, SessionID: sessionID}) })
		if i < 0 {
			return nil, errors.New("not in trash")
		}
		e = entries[i]
		if err := e.restore(); err != nil {
			return nil, err
		}
		return slices.Delete(entries, i, i+1), nil
	})
	return e, err
}

func (e TrashEntry) restore() error {
	// validate everything first so a failure midway can be retried: a used rewrite refuses; a missing trash copy (restore interrupted last time) counts as restored
	for _, f := range e.Files {
		if f.Replaced != "" {
			if _, err := os.Lstat(f.To); err == nil && FileStamp(f.Replaced) != f.Stamp && FileStamp(f.Replaced) != "" {
				return errors.New("session was used after the move; restore refused")
			}
		}
	}
	moved := false
	for _, f := range e.Files {
		moved = moved || f.Replaced != ""
		if _, err := os.Lstat(f.To); err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.From), 0o700); err != nil {
			return err
		}
		if f.Replaced != "" {
			os.RemoveAll(f.Replaced)
		}
		if err := moveAny(f.To, f.From); err != nil {
			return err
		}
	}
	if r := e.Record; moved && r != nil && r.PinnedPath != "" && r.TranscriptPath != "" { // the move relinked the pin to the rewrite; point it back at the original
		r.Relink(r.TranscriptPath)
	}
	os.Remove(e.dir())
	return nil
}

// PurgeTrash deletes entries and unowned directories older than days; days <= 0 means everything.
func PurgeTrash(days int) (int, error) {
	if !present(TrashDir()) {
		return 0, nil
	}
	cut := time.Now().AddDate(0, 0, -days)
	due := func(t time.Time) bool { return days <= 0 || !t.After(cut) }
	n := 0
	err := editTrash(func(entries []TrashEntry) ([]TrashEntry, error) {
		var kept []TrashEntry
		owned := map[string]bool{} // by group/name: the manifest's absolute paths go stale when TEND_HOME moves
		for _, e := range entries {
			if !due(e.DeletedAt) {
				kept = append(kept, e)
				owned[filepath.Join(filepath.Base(filepath.Dir(e.dir())), filepath.Base(e.dir()))] = true
				continue
			}
			os.RemoveAll(e.dir()) // Relocated.To lives outside the trash and is in use
			n++
		}
		for _, p := range []string{ProviderClaude, ProviderCodex, KindMemory} {
			ds, _ := os.ReadDir(filepath.Join(TrashDir(), p))
			for _, d := range ds {
				if info, err := d.Info(); err == nil && !owned[filepath.Join(p, d.Name())] && due(info.ModTime()) {
					os.RemoveAll(filepath.Join(TrashDir(), p, d.Name()))
				}
			}
		}
		if n == 0 {
			return nil, errUnchanged
		}
		return kept, nil
	})
	return n, err
}

var rename = os.Rename

func moveAny(from, to string) error {
	if err := rename(from, to); err == nil {
		return nil
	}
	st, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if st.IsDir() {
		if err := copyDir(from, to); err != nil {
			return err
		}
	} else if err := copyFile(from, to, st.Mode()); err != nil {
		return err
	}
	return os.RemoveAll(from)
}

func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyDir(from, to string) error {
	return filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		return copyFile(p, dst, info.Mode())
	})
}
