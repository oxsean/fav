package migrate

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Limits of a migration: the most a session may hold, the default and the largest chunk.
const (
	MaxSession = 2 << 30
	ChunkSize  = 1 << 20
	MaxChunk   = 4 << 20
)

// sessionFiles are the files of r's session a migration copies, absolute; a symlink among them, or a file outside the
// three directories, is refused.
func sessionFiles(idx *index.Index, r *tend.Rec) ([]string, error) {
	home := capture.ClaudeHome()
	var out []string
	for _, p := range index.SessionFilesOf(idx, r) {
		if p == r.PinnedPath {
			continue // tend's own copy, not Claude's
		}
		err := filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.Type()&fs.ModeSymlink != 0:
				return &wire.Error{Code: wire.CodeBadRequest, Detail: "symlink " + q}
			case d.IsDir():
				return nil
			case !d.Type().IsRegular():
				return &wire.Error{Code: wire.CodeBadRequest, Detail: "not a file " + q}
			}
			if _, ok := paths.Inside(home, q); !ok {
				return &wire.Error{Code: wire.CodeBadRequest, Detail: "outside " + q}
			}
			out = append(out, q)
			return nil
		})
		if errors.Is(err, fs.ErrNotExist) {
			continue // gone since the glob
		}
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// rel is p, under the Claude home, as a manifest names it.
func rel(home, p string) string {
	r, _ := paths.Inside(home, p)
	return filepath.ToSlash(r)
}

// Plan is what a migration of r copies, as this machine holds it now: every file with its size, sha, lines and
// identity, and what the Claude home has of the session elsewhere. Only Claude sessions; idx may be nil (no chain).
func Plan(idx *index.Index, r *tend.Rec) (Manifest, error) {
	if r.Provider != tend.ProviderClaude {
		return Manifest{}, &wire.Error{Code: wire.CodeUnsupported, Detail: r.Provider}
	}
	home := capture.ClaudeHome()
	all := index.SessionIDs(idx, r)
	if err := checkAliases(r.SessionID, all[1:]); err != nil {
		return Manifest{}, err
	}
	man := Manifest{Files: []File{}, Aliases: all[1:]}
	files, err := sessionFiles(idx, r)
	if err != nil {
		return Manifest{}, err
	}
	for _, p := range files {
		name := rel(home, p)
		kind, ok := kindOf(name, all)
		if !ok {
			return Manifest{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "path " + name}
		}
		st, err := os.Stat(p)
		if err != nil {
			return Manifest{}, err
		}
		f := File{Path: name, Kind: kind, Size: st.Size(), ID: fileio.ID(p), ModTime: st.ModTime()}
		if f.SHA, f.Lines, err = sumOf(p, st); err != nil {
			return Manifest{}, err
		}
		man.Files = append(man.Files, f)
		if man.Size() > MaxSession {
			return Manifest{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "too_big"}
		}
	}
	if _, ok := man.Main(r.SessionID); !ok {
		return Manifest{}, &wire.Error{Code: wire.CodeNotFound, Detail: "transcript"}
	}
	man.Left = left(home, all)
	return man, nil
}

// left are the entries of the Claude home's other directories named after one of ids.
func left(home string, ids []string) []string {
	var out []string
	tops, _ := os.ReadDir(home)
	for _, top := range tops {
		if !top.IsDir() || slices.Contains([]string{dirProjects, dirFileHistory, dirTodos}, top.Name()) {
			continue
		}
		ents, _ := os.ReadDir(filepath.Join(home, top.Name()))
		for _, e := range ents {
			if slices.ContainsFunc(ids, func(id string) bool { return strings.HasPrefix(e.Name(), id) }) {
				out = append(out, top.Name()+"/"+e.Name())
			}
		}
	}
	return out
}

// sums keeps sumOf's answers in this process by file identity, size and time: a migration plans one file three
// times, and the copies' checks read the same transcripts again.
var sums sync.Map

type sumKey struct {
	id   string
	size int64
	mod  time.Time
	n    int64 // bytes summed: the whole file, or a prefix
}

type sum struct {
	sha   string
	lines int
}

// sumOf is the sha256 of p's bytes and its lines, a last one without a newline counted.
func sumOf(p string, st os.FileInfo) (string, int, error) {
	return sumN(p, st, st.Size())
}

// sumN is sumOf over p's first n bytes.
func sumN(p string, st os.FileInfo, n int64) (string, int, error) {
	k := sumKey{fileio.ID(p), st.Size(), st.ModTime(), n}
	if v, ok := sums.Load(k); ok && k.id != "" {
		s := v.(sum)
		return s.sha, s.lines, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	sha, lines, err := sumReader(io.LimitReader(f, n))
	if err == nil && k.id != "" {
		sums.Store(k, sum{sha, lines})
	}
	return sha, lines, err
}

func sumReader(r io.Reader) (string, int, error) {
	h := sha256.New()
	lines, last := 0, byte('\n')
	br := bufio.NewReaderSize(r, 1<<20)
	buf := make([]byte, 1<<20)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			for _, c := range buf[:n] {
				if c == '\n' {
					lines++
				}
			}
			last = buf[n-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, err
		}
	}
	if last != '\n' {
		lines++
	}
	return hex.EncodeToString(h.Sum(nil)), lines, nil
}

// Intend records on this machine, the source, that migration m copies r to peer: pending, with man's main transcript.
// Recording it again (a retry with a new plan) updates it; a migration already ended is not reopened.
func Intend(m string, r *tend.Rec, peer Peer, man Manifest) error {
	if err := validID(m); err != nil {
		return err
	}
	main, _ := man.Main(r.SessionID)
	_, err := change(m, func(cur Record, ok bool) (*Record, error) {
		if ok && (cur.State != StatePending || cur.Role != RoleTo || cur.Provider != r.Provider || cur.SessionID != r.SessionID) {
			return nil, &wire.Error{Code: wire.CodeConflict, Detail: "migration " + cur.State}
		}
		return &Record{Migration: m, Role: RoleTo, Provider: r.Provider, SessionID: r.SessionID, Peer: peer, State: StatePending,
			At: time.Now(), Files: len(man.Files), Size: main.Size, SHA: main.SHA}, nil
	})
	return err
}

// Pending: migration m of provider's session sid is pending here, its source.
func Pending(m, provider, sid string) error {
	cur, ok, err := find(m)
	switch {
	case err != nil:
		return err
	case !ok || cur.Role != RoleTo || cur.Provider != provider || cur.SessionID != sid:
		return &wire.Error{Code: wire.CodeNotFound, Detail: "migration " + m}
	case cur.State != StatePending:
		return &wire.Error{Code: wire.CodeConflict, Detail: "migration " + cur.State}
	}
	return nil
}

// Read is n bytes at off of file, a manifest path of r's session, while its identity is still id: another one
// answers stale. eof: the read reached the file's end.
func Read(idx *index.Index, r *tend.Rec, file string, off int64, n int, id string) ([]byte, bool, error) {
	if n <= 0 || n > MaxChunk || off < 0 {
		return nil, false, &wire.Error{Code: wire.CodeBadRequest, Detail: "n"}
	}
	if _, ok := kindOf(file, index.SessionIDs(idx, r)); !ok {
		return nil, false, &wire.Error{Code: wire.CodeBadRequest, Detail: "file"}
	}
	files, err := sessionFiles(idx, r)
	if err != nil {
		return nil, false, err
	}
	p := filepath.Join(capture.ClaudeHome(), filepath.FromSlash(file))
	if !slices.Contains(files, p) {
		return nil, false, &wire.Error{Code: wire.CodeNotFound, Detail: file}
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, false, &wire.Error{Code: wire.CodeStale, Detail: file}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || fileio.IDOf(f) != id {
		return nil, false, &wire.Error{Code: wire.CodeStale, Detail: file}
	}
	buf := make([]byte, min(int64(n), max(st.Size()-off, 0)))
	k, err := f.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	return buf[:k], off+int64(k) >= st.Size(), nil
}

// Finish records migration m of provider's session sid ended on this machine, its source: done with the main
// transcript the target committed (committed; nil keeps the last plan's), or aborted. Done again stays done; a done
// migration is not aborted.
func Finish(m, provider, sid, state string, committed *Sum) (Record, error) {
	if state != StateDone && state != StateAborted {
		return Record{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "state"}
	}
	return change(m, func(cur Record, ok bool) (*Record, error) {
		switch {
		case !ok || cur.Role != RoleTo || cur.Provider != provider || cur.SessionID != sid:
			return nil, &wire.Error{Code: wire.CodeNotFound, Detail: "migration " + m}
		case cur.State == state:
			return nil, nil
		case cur.State != StatePending:
			return nil, &wire.Error{Code: wire.CodeConflict, Detail: "migration " + cur.State}
		}
		cur.State, cur.At = state, time.Now()
		if state == StateDone && committed != nil {
			cur.Size, cur.SHA = committed.Size, committed.SHA
		}
		return &cur, nil
	})
}

// MoveAway puts the original of r, migrated by done migration rec, into this machine's trash (index.TrashSession)
// under a title naming where it went, and records it moved; a session moved already moves nothing.
func MoveAway(s *tend.Store, idx *index.Index, r *tend.Rec, rec Record) (int, *index.Index, error) {
	if rec.Moved {
		return 0, idx, nil
	}
	e, next, err := index.TrashSession(s, idx, r)
	if err != nil {
		return 0, next, err
	}
	e.Title = i18n.F("migrate.trash_moved", r.Title, rec.Peer.Name)
	if err := tend.SaveTrashEntry(e); err != nil {
		return len(e.Files), next, err
	}
	_, err = change(rec.Migration, func(cur Record, ok bool) (*Record, error) {
		cur.Moved = true
		return &cur, nil
	})
	return len(e.Files), next, err
}

// mainTranscript is provider's session sid's own transcript on this machine, the newest of them; "" when there is none.
func mainTranscript(sid string) string {
	best, at := "", time.Time{}
	for _, p := range capture.ClaudeTranscripts(sid) {
		if st, err := os.Stat(p); err == nil && (best == "" || st.ModTime().After(at)) {
			best, at = p, st.ModTime()
		}
	}
	return best
}

// isJSONL: a transcript-shaped file name.
func isJSONL(p string) bool { return path.Ext(p) == ".jsonl" }
