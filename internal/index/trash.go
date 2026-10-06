package index

import (
	"maps"
	"slices"

	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/tend"
)

// SessionFilesOf is what trashing r moves: the files of every id of its Claude continuation chain (idx may be nil),
// and its pinned copy.
func SessionFilesOf(idx *Index, r *tend.Rec) []string {
	ids := []string{r.SessionID}
	if idx != nil {
		for _, s := range idx.Sessions() {
			if s.Provider == r.Provider && s.SessionID == r.SessionID {
				ids = append(ids, s.Aliases...)
			}
		}
	}
	var files []string
	for _, id := range ids {
		files = append(files, SessionFiles(r.Provider, id)...)
	}
	if r.PinnedPath != "" {
		files = append(files, r.PinnedPath)
	}
	return files
}

// Trash moves files into the trash with a copy of r's record (the reloaded one if newer) and tombstones the record.
func Trash(s *tend.Store, r *tend.Rec, files []string) (tend.TrashEntry, error) {
	e := tend.TrashEntry{Provider: r.Provider, SessionID: r.SessionID, Title: r.Title, Cwd: r.Cwd}
	if r.ID != "" {
		if cur := s.Get(r.ID); cur != nil {
			r = cur
		}
		cp := *r
		e.Record = &cp
	}
	if e.SessionID == "" {
		e.SessionID = r.ID
	}
	e, err := tend.MoveToTrash(e, files)
	if err != nil || r.ID == "" {
		return e, err
	}
	r.Deleted = true
	return e, s.Put(r)
}

// Restore puts a trashed session back; force lists the files a rescan must read from scratch (an undone move).
func Restore(s *tend.Store, provider, sessionID string) (e tend.TrashEntry, force map[string]bool, err error) {
	if e, err = tend.RestoreTrash(provider, sessionID); err != nil {
		return e, nil, err
	}
	if e.Record != nil {
		e.Record.Deleted = false
		err = s.Put(e.Record)
	}
	return e, rescanAfterRestore(e), err
}

// TrashSession moves r's session files into the trash (SessionFilesOf, Trash); next is idx without the files no longer
// there, also after a failed move (nil for a nil idx). The caller refuses a running session first.
func TrashSession(s *tend.Store, idx *Index, r *tend.Rec) (e tend.TrashEntry, next *Index, err error) {
	files := SessionFilesOf(idx, r)
	e, err = Trash(s, r, files)
	if idx != nil {
		next = idx.Forget(slices.DeleteFunc(files, paths.Exists))
	}
	return e, next, err
}

// RestoreSession puts a trashed session back; next is idx without the files the next refresh must read from scratch
// (Restore's force), idx itself when there are none.
func RestoreSession(s *tend.Store, idx *Index, provider, sessionID string) (e tend.TrashEntry, next *Index, err error) {
	e, force, err := Restore(s, provider, sessionID)
	next = idx
	if idx != nil && len(force) > 0 {
		next = idx.Forget(slices.Collect(maps.Keys(force)))
	}
	return e, next, err
}

// Trashed is the trash's row of a session (as Rows lists it, its transcript the copy in the trash), nil when it is not
// there.
func Trashed(provider, sessionID string) (*tend.Rec, error) {
	entries, err := tend.LoadTrash()
	if i := slices.IndexFunc(entries, func(e tend.TrashEntry) bool { return e.Provider == provider && e.SessionID == sessionID }); i >= 0 {
		return trashRec(entries[i]), nil
	}
	return nil, err
}

// Forget drops files just moved away from the index in memory; the next Refresh agrees without rescanning them.
func (idx *Index) Forget(files []string) *Index {
	next := *idx
	next.files, next.dirty = maps.Clone(idx.files), nil
	for _, f := range files {
		delete(next.files, f)
	}
	return &next
}

func (idx *Index) RescanSave(force map[string]bool) (*Index, error) {
	next, _ := idx.Rescan(force)
	return next, next.Save()
}
