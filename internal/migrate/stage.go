package migrate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// What a target finds of the session it is to take: nothing, an unchanged copy it may replace (forward), a copy both
// sides went on with (diverged), or one it has no relation with (exists).
const (
	ClashNone     = ""
	ClashForward  = "forward"
	ClashDiverged = "diverged"
	ClashExists   = "exists"
)

// StaleAfter: a staging not committed for this long goes at the next begin.
const StaleAfter = 7 * 24 * time.Hour

// Incoming is what a migration brings to this machine, its target.
type Incoming struct {
	Migration string   `json:"migration"`
	From      Peer     `json:"from"`
	Provider  string   `json:"provider"`
	SessionID string   `json:"session_id"`
	Title     string   `json:"title,omitempty"`
	Cwd       string   `json:"cwd"` // on the source
	Dir       string   `json:"dir"` // here
	Pairs     []Pair   `json:"pairs,omitempty"`
	Manifest  Manifest `json:"manifest"`
}

// Began is where a migration stands here: the bytes staged of each file so far, committed already, or a clash.
type Began struct {
	Staged    map[string]int64
	Committed bool
	Source    *Sum // committed: what of the source's main transcript it carried
	Clash     string
}

// staging is the directory a migration's files wait in: state.json, files/<path> as received, out/<path> rewritten
// for the commit, placing.json once the commit started putting them in place.
type staging struct{ dir string }

func importRoot() string { return filepath.Join(tend.Home(), "import") }

func stagingOf(m string) staging { return staging{filepath.Join(importRoot(), m)} }

func (s staging) state() string       { return filepath.Join(s.dir, "state.json") }
func (s staging) placing() string     { return filepath.Join(s.dir, "placing.json") }
func (s staging) raw(p string) string { return filepath.Join(s.dir, "files", filepath.FromSlash(p)) }
func (s staging) out(p string) string { return filepath.Join(s.dir, "out", filepath.FromSlash(p)) }
func (s staging) exists() bool        { return paths.Exists(s.state()) }
func (s staging) load() (Incoming, error) {
	var in Incoming
	return in, fileio.ReadJSON(s.state(), &in)
}

type stagedState struct {
	Incoming
	At time.Time `json:"at"` // the last begin: what the clean-up goes by
}

// lockImports holds every import on this machine to one at a time, across processes (each ssh caller runs its own).
func lockImports() (func(), error) {
	if err := os.MkdirAll(importRoot(), 0o700); err != nil {
		return nil, err
	}
	return filelock.Lock(filepath.Join(importRoot(), ".lock"))
}

// Begin opens migration in here, or finds where it stands: committed, a clash with what is here, or the bytes staged of
// each file (a file whose sha changed in a new plan starts over). The directory must exist here: Claude resumes in it.
func Begin(in Incoming) (Began, error) {
	if err := checkIncoming(in); err != nil {
		return Began{}, err
	}
	unlock, err := lockImports()
	if err != nil {
		return Began{}, err
	}
	defer unlock()
	sweep(time.Now(), in.Migration)
	if rec, ok, err := find(in.Migration); err != nil || ok && rec.Role == RoleFrom && rec.State == StateDone {
		return Began{Committed: err == nil, Source: rec.Source}, err
	}
	s := stagingOf(in.Migration)
	var pl placing
	if fileio.ReadJSON(s.placing(), &pl) == nil { // the commit started: what is in place is this migration's
		return Began{Staged: full(in.Manifest), Clash: pl.Clash}, nil
	}
	clash, err := clashOf(in)
	if err != nil || clash == ClashExists || clash == ClashDiverged {
		return Began{Clash: clash}, err
	}
	var old stagedState
	if fileio.ReadJSON(s.state(), &old) == nil {
		for _, f := range old.Manifest.Files {
			if nf, ok := in.Manifest.File(f.Path); !ok || nf.SHA != f.SHA {
				os.Remove(s.raw(f.Path))
			}
		}
	}
	os.RemoveAll(filepath.Join(s.dir, "out"))
	if err := os.MkdirAll(filepath.Join(s.dir, "files"), 0o700); err != nil {
		return Began{}, err
	}
	if err := fileio.WriteJSON(s.state(), stagedState{in, time.Now()}); err != nil {
		return Began{}, err
	}
	b := Began{Staged: map[string]int64{}, Clash: clash}
	for _, f := range in.Manifest.Files {
		n, err := s.held(f)
		if err != nil {
			return Began{}, err
		}
		if n > 0 {
			b.Staged[f.Path] = n
		}
	}
	proc.CrashAt("migrate.begun")
	return b, nil
}

func full(m Manifest) map[string]int64 {
	out := map[string]int64{}
	for _, f := range m.Files {
		out[f.Path] = f.Size
	}
	return out
}

// held is how many bytes of f are staged and good so far: a whole file whose sha does not match, or one longer than f,
// starts over. An empty file is staged as soon as it is asked about: no chunk comes for it.
func (s staging) held(f File) (int64, error) {
	p := s.raw(f.Path)
	st, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist) && f.Size == 0:
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return 0, err
		}
		return 0, fileio.WriteFile(p, nil, 0o600)
	case errors.Is(err, fs.ErrNotExist):
		return 0, nil
	case err != nil:
		return 0, err
	case st.Size() < f.Size:
		return st.Size(), nil
	case st.Size() == f.Size:
		if sha, _, err := sumOf(p, st); err == nil && sha == f.SHA {
			return f.Size, nil
		}
	}
	return 0, os.Remove(p)
}

func checkIncoming(in Incoming) error {
	if err := validID(in.Migration); err != nil {
		return err
	}
	switch {
	case in.Provider != tend.ProviderClaude:
		return &wire.Error{Code: wire.CodeUnsupported, Detail: in.Provider}
	case !capture.SafeID(in.SessionID):
		return &wire.Error{Code: wire.CodeBadRequest, Detail: "session_id"}
	case !filepath.IsAbs(in.Dir):
		return &wire.Error{Code: wire.CodeBadRequest, Detail: "dir"}
	case !paths.IsDir(in.Dir):
		return &wire.Error{Code: wire.CodeNotFound, Detail: "dir " + in.Dir}
	case len(in.Manifest.Files) == 0 || in.Manifest.Size() > MaxSession:
		return &wire.Error{Code: wire.CodeBadRequest, Detail: "manifest"}
	}
	if _, ok := in.Manifest.Main(in.SessionID); !ok {
		return &wire.Error{Code: wire.CodeBadRequest, Detail: "manifest"}
	}
	if err := checkAliases(in.SessionID, in.Manifest.Aliases); err != nil {
		return err
	}
	all := append([]string{in.SessionID}, in.Manifest.Aliases...)
	seen := map[string]bool{}
	for _, f := range in.Manifest.Files {
		kind, ok := kindOf(f.Path, all)
		switch {
		case !ok || kind != f.Kind || !filepath.IsLocal(filepath.FromSlash(f.Path)) || seen[f.Path] || f.Size < 0:
			return &wire.Error{Code: wire.CodeBadRequest, Detail: "file " + f.Path}
		}
		seen[f.Path] = true
	}
	return nil
}

// clashOf is what this machine has of in's session: by the latest migration between it and in's source, whether its
// copy moved on since.
func clashOf(in Incoming) (string, error) {
	ids := append([]string{in.SessionID}, in.Manifest.Aliases...)
	var files []string
	for _, id := range ids {
		files = append(files, index.SessionFiles(in.Provider, id)...)
	}
	if len(files) == 0 {
		return ClashNone, nil
	}
	recs, err := Of(in.Provider, in.SessionID)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(recs, func(r Record) bool {
		return r.State == StateDone && r.Peer.Endpoint != "" && r.Peer.Endpoint == in.From.Endpoint
	})
	if i < 0 {
		return ClashExists, nil
	}
	if changed := Changed(recs[i]); changed == nil || *changed {
		return ClashDiverged, nil
	}
	return ClashForward, nil
}

// Chunk adds data at off to the staged copy of file, answering how many bytes are staged now: off must be that many
// (a chunk sent again after its answer was lost is taken as staged). Bytes past the planned size, or a file staged
// whole whose sha does not match (it starts over), answer stale: the source changed since its plan.
func Chunk(m, file string, off int64, data []byte) (int64, error) {
	if err := validID(m); err != nil {
		return 0, err
	}
	unlock, err := lockImports()
	if err != nil {
		return 0, err
	}
	defer unlock()
	s := stagingOf(m)
	in, err := s.load()
	if err != nil {
		return 0, &wire.Error{Code: wire.CodeNotFound, Detail: "migration " + m}
	}
	if paths.Exists(s.placing()) {
		return 0, &wire.Error{Code: wire.CodeConflict, Detail: "committing"}
	}
	f, ok := in.Manifest.File(file)
	if !ok {
		return 0, &wire.Error{Code: wire.CodeBadRequest, Detail: "file"}
	}
	p := s.raw(file)
	var held int64
	if st, err := os.Stat(p); err == nil {
		held = st.Size()
	}
	switch {
	case off < held && off+int64(len(data)) <= held:
		return held, nil
	case off != held:
		return held, &wire.Error{Code: wire.CodeConflict, Detail: "off"}
	case held+int64(len(data)) > f.Size:
		return held, &wire.Error{Code: wire.CodeStale, Detail: file}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return held, err
	}
	w, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return held, err
	}
	n, err := w.Write(data)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	held += int64(n)
	if err != nil {
		return held, err
	}
	if held < f.Size {
		proc.CrashAt("migrate.half")
		return held, nil
	}
	if got, err := s.held(f); err != nil || got != f.Size {
		return 0, &wire.Error{Code: wire.CodeStale, Detail: file}
	}
	return held, nil
}

// Abort drops migration m here: what its commit put in place goes (a replaced copy comes back from the trash), then
// its staging. A committed migration is not undone.
func Abort(m string) error {
	if err := validID(m); err != nil {
		return err
	}
	unlock, err := lockImports()
	if err != nil {
		return err
	}
	defer unlock()
	if rec, ok, err := find(m); err != nil || ok && rec.Role == RoleFrom && rec.State == StateDone {
		if err == nil {
			err = &wire.Error{Code: wire.CodeConflict, Detail: "committed"}
		}
		return err
	}
	s := stagingOf(m)
	var pl placing
	if fileio.ReadJSON(s.placing(), &pl) == nil {
		in, _ := s.load()
		if err := pl.undo(in, s, false); err != nil {
			return err
		}
	}
	return os.RemoveAll(s.dir)
}

// sweep drops stagings untouched for StaleAfter, but the one of keep and any whose commit started.
func sweep(now time.Time, keep string) {
	ents, _ := os.ReadDir(importRoot())
	for _, e := range ents {
		if !e.IsDir() || e.Name() == keep {
			continue
		}
		s := staging{filepath.Join(importRoot(), e.Name())}
		var st stagedState
		if fileio.ReadJSON(s.state(), &st) != nil {
			if info, err := e.Info(); err != nil || now.Sub(info.ModTime()) < StaleAfter {
				continue
			}
		} else if now.Sub(st.At) < StaleAfter || paths.Exists(s.placing()) {
			continue
		}
		os.RemoveAll(s.dir)
	}
}
