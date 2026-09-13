package fav

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// TrashEntry is a trashed session: where the files went, plus the record as it was.
type TrashEntry struct {
	Provider  string    `json:"provider"`
	SessionID string    `json:"session_id"`
	Title     string    `json:"title"`
	Cwd       string    `json:"cwd,omitempty"`
	Record    *Rec      `json:"record,omitempty"`
	Files     []Moved   `json:"files"`
	DeletedAt time.Time `json:"deleted_at"`
	Dir       string    `json:"dir,omitempty"` // a fresh directory every time, so re-trashing the same session never overwrites the last one
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

func LoadTrash() ([]TrashEntry, error) {
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
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.SessionID != "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt.After(out[j].DeletedAt) })
	return out, sc.Err()
}

func saveTrash(entries []TrashEntry) error {
	if err := os.MkdirAll(TrashDir(), 0o700); err != nil {
		return err
	}
	tmp := trashManifest() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			f.Close()
			return err
		}
		w.Write(b)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, trashManifest())
}

// MoveToTrash moves paths (files or dirs) into the trash and records them; an earlier entry of the same session is replaced.
func MoveToTrash(e TrashEntry, paths []string) (TrashEntry, error) {
	entries, err := LoadTrash()
	if err != nil {
		return e, err
	}
	e.DeletedAt = time.Now()
	e.Dir = filepath.Join(TrashDir(), e.Provider, e.SessionID+"-"+strconv.FormatInt(e.DeletedAt.UnixNano(), 36))
	dst := e.dir()
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return e, err
	}
	e.Files = nil
	for i, p := range paths {
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		to := filepath.Join(dst, strconv.Itoa(i)+"-"+filepath.Base(p))
		if err := moveAny(p, to); err != nil {
			return e, err
		}
		e.Files = append(e.Files, Moved{From: p, To: to})
	}
	kept := entries[:0]
	for _, x := range entries {
		if x.Provider != e.Provider || x.SessionID != e.SessionID {
			kept = append(kept, x)
		}
	}
	return e, saveTrash(append([]TrashEntry{e}, kept...))
}

// SaveTrashEntry replaces the entry of the same session (a project move calls MoveToTrash first, then records the rewritten files).
func SaveTrashEntry(e TrashEntry) error {
	entries, err := LoadTrash()
	if err != nil {
		return err
	}
	for i := range entries {
		if entries[i].Provider == e.Provider && entries[i].SessionID == e.SessionID {
			entries[i] = e
			return saveTrash(entries)
		}
	}
	return saveTrash(append([]TrashEntry{e}, entries...))
}

// RestoreTrash puts the session back (recreating the original directory if needed), drops the entry and deletes files a project move rewrote.
func RestoreTrash(provider, sessionID string) (TrashEntry, error) {
	entries, err := LoadTrash()
	if err != nil {
		return TrashEntry{}, err
	}
	idx := -1
	for i, x := range entries {
		if x.Provider == provider && x.SessionID == sessionID {
			idx = i
		}
	}
	if idx < 0 {
		return TrashEntry{}, errors.New("not in trash")
	}
	e := entries[idx]
	// validate everything first so a failure midway can be retried: a used rewrite refuses; a missing trash copy (restore interrupted last time) counts as restored
	for _, f := range e.Files {
		if f.Replaced != "" {
			if _, err := os.Lstat(f.To); err == nil && FileStamp(f.Replaced) != f.Stamp && FileStamp(f.Replaced) != "" {
				return e, errors.New("session was used after the move; restore refused")
			}
		}
	}
	for _, f := range e.Files {
		if _, err := os.Lstat(f.To); err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.From), 0o700); err != nil {
			return e, err
		}
		if f.Replaced != "" {
			os.RemoveAll(f.Replaced)
		}
		if err := moveAny(f.To, f.From); err != nil {
			return e, err
		}
	}
	moved := false
	for _, f := range e.Files {
		moved = moved || f.Replaced != ""
	}
	if r := e.Record; moved && r != nil && r.PinnedPath != "" && r.TranscriptPath != "" { // the move relinked the pin to the rewrite; point it back at the original
		os.Remove(r.PinnedPath)
		if os.Link(r.TranscriptPath, r.PinnedPath) != nil {
			r.PinnedPath = ""
		}
	}
	os.Remove(e.dir())
	return e, saveTrash(append(entries[:idx:idx], entries[idx+1:]...))
}

// PurgeTrash deletes entries older than days; days <= 0 means everything.
func PurgeTrash(days int) (int, error) {
	entries, err := LoadTrash()
	if err != nil {
		return 0, err
	}
	cut := time.Now().AddDate(0, 0, -days)
	var kept []TrashEntry
	n := 0
	for _, e := range entries {
		if days > 0 && e.DeletedAt.After(cut) {
			kept = append(kept, e)
			continue
		}
		os.RemoveAll(e.dir()) // Relocated.To lives outside the trash and is in use
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, saveTrash(kept)
}

// rename first; copy + delete across volumes
func moveAny(from, to string) error {
	if err := os.Rename(from, to); err == nil {
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
