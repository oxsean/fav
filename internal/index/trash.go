package index

import (
	"maps"

	"github.com/oxsean/fav/internal/fav"
)

// SessionFilesOf is what trashing r moves: the files of every id of its Claude continuation chain (idx may be nil),
// and its pinned copy.
func SessionFilesOf(idx *Index, r *fav.Rec) []string {
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
func Trash(s *fav.Store, r *fav.Rec, files []string) (fav.TrashEntry, error) {
	e := fav.TrashEntry{Provider: r.Provider, SessionID: r.SessionID, Title: r.Title, Cwd: r.Cwd}
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
	e, err := fav.MoveToTrash(e, files)
	if err != nil || r.ID == "" {
		return e, err
	}
	r.Deleted = true
	return e, s.Put(r)
}

// Restore puts a trashed session back; force lists the files a rescan must read from scratch (an undone move).
func Restore(s *fav.Store, provider, sessionID string) (e fav.TrashEntry, force map[string]bool, err error) {
	if e, err = fav.RestoreTrash(provider, sessionID); err != nil {
		return e, nil, err
	}
	if e.Record != nil {
		e.Record.Deleted = false
		err = s.Put(e.Record)
	}
	return e, rescanAfterRestore(e), err
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
