package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Committed is a commit's outcome: the target's record, how many files it put in place, the cwd values it left as
// they were (no pair or home mapped them), and the transcripts the index must read from scratch.
type Committed struct {
	Record   Record
	Files    int
	Unmapped []string
	Touched  map[string]bool
}

// placing is what a commit puts where (written before the first file goes in place), so that a commit run again, a
// begin or an abort after a crash knows what in the Claude home is this migration's.
type placing struct {
	Clash    string    `json:"clash,omitempty"`
	Files    []placed  `json:"files"`
	Unmapped []string  `json:"unmapped,omitempty"`
	Trash    string    `json:"trash,omitempty"` // forward: the trash entry the replaced copy went to
	At       time.Time `json:"at"`
}

type placed struct {
	Path  string `json:"path"`  // the manifest's
	Final string `json:"final"` // where it goes here
	SHA   string `json:"sha"`   // of the file as it goes there
}

// finalPath is where manifest path p of a session going to dir is put here: its project directory is dir's.
func finalPath(p, dir string) string {
	home := capture.ClaudeHome()
	segs := strings.Split(p, "/")
	if segs[0] == dirProjects {
		segs[1] = index.ClaudeProjectName(dir)
	}
	return filepath.Join(home, filepath.Join(segs...))
}

// Commit puts migration m's staged files in place here, its target: each transcript with its cwd values carried over
// (pairs first, then home to home), the index refreshed through rescan, the note written, then the record that says it
// is done; here is this machine as its hello describes it. A copy it replaces (forward) goes to the trash first; a commit that fails takes back what it put in place,
// and running it again after a crash finishes what is left. Already committed answers its record.
func Commit(s *tend.Store, m, note string, here pathmap.End, rescan func(force map[string]bool) error) (Committed, error) {
	if err := validID(m); err != nil {
		return Committed{}, err
	}
	unlock, err := lockImports()
	if err != nil {
		return Committed{}, err
	}
	defer unlock()
	if rec, ok, err := find(m); err != nil || ok && rec.Role == RoleFrom && rec.State == StateDone {
		return Committed{Record: rec, Files: rec.Files}, err
	}
	st := stagingOf(m)
	in, err := st.load()
	if err != nil {
		return Committed{}, &wire.Error{Code: wire.CodeNotFound, Detail: "migration " + m}
	}
	var pl placing
	if fileio.ReadJSON(st.placing(), &pl) != nil {
		if pl, err = prepare(in, st, here); err != nil {
			return Committed{}, err
		}
	}
	if err := pl.put(s, in, st); err != nil {
		return Committed{}, err
	}
	touched := map[string]bool{}
	for _, f := range pl.Files {
		if isJSONL(f.Path) {
			touched[f.Final] = true
		}
	}
	if err := rescan(touched); err != nil {
		return Committed{}, err
	}
	proc.CrashAt("migrate.placed")
	rec := Record{Migration: m, Role: RoleFrom, Provider: in.Provider, SessionID: in.SessionID, Peer: in.From, State: StateDone,
		At: time.Now(), Files: len(pl.Files), Dir: in.Dir}
	mf, _ := in.Manifest.Main(in.SessionID)
	rec.Source = &Sum{Size: mf.Size, SHA: mf.SHA}
	for _, f := range pl.Files {
		if f.Path == mf.Path {
			st, err := os.Stat(f.Final)
			if err != nil {
				return Committed{}, err
			}
			rec.Size, rec.SHA = st.Size(), f.SHA
		}
	}
	if text := noteText(note, pl.Unmapped); text != "" {
		rec.Note = NotePath(m)
		if err := fileio.WriteFile(rec.Note, []byte(text), 0o600); err != nil {
			return Committed{}, err
		}
	}
	rec, err = change(m, func(Record, bool) (*Record, error) { return &rec, nil })
	if err != nil {
		return Committed{}, err
	}
	os.RemoveAll(st.dir)
	return Committed{Record: rec, Files: len(pl.Files), Unmapped: pl.Unmapped, Touched: touched}, nil
}

// prepare checks every file is staged whole, writes out/ (transcripts with their cwd carried over, line counts kept)
// and placing.json; nothing here is touched yet.
func prepare(in Incoming, st staging, here pathmap.End) (placing, error) {
	clash, err := clashOf(in)
	switch {
	case err != nil:
		return placing{}, err
	case clash == ClashExists || clash == ClashDiverged:
		return placing{}, &wire.Error{Code: wire.CodeConflict, Detail: clash}
	case clash == ClashForward:
		if state, _ := capture.LiveState(in.Provider, in.SessionID); state != capture.LiveNo {
			return placing{}, &wire.Error{Code: wire.CodeBusy, Detail: "session " + in.SessionID}
		}
	}
	from, to := in.From.End.Map(), here
	unmapped := map[string]bool{}
	mapCwd := func(cwd string) (string, bool) {
		for _, p := range in.Pairs {
			if d, ok := pathmap.Rebase(cwd, p.From, p.To, from, to); ok {
				return d, true
			}
		}
		if d, ok := pathmap.Map(cwd, from, to); ok {
			return d, true
		}
		unmapped[cwd] = true
		return cwd, false
	}
	pl := placing{Clash: clash, At: time.Now()}
	for _, f := range in.Manifest.Files {
		if n, err := st.held(f); err != nil || n != f.Size {
			return placing{}, &wire.Error{Code: wire.CodeConflict, Detail: "incomplete " + f.Path}
		}
		sha, err := rewrite(st.raw(f.Path), st.out(f.Path), f, mapCwd)
		if err != nil {
			return placing{}, err
		}
		pl.Files = append(pl.Files, placed{Path: f.Path, Final: finalPath(f.Path, in.Dir), SHA: sha})
	}
	for cwd := range unmapped {
		pl.Unmapped = append(pl.Unmapped, cwd)
	}
	slices.Sort(pl.Unmapped)
	if err := fileio.WriteJSON(st.placing(), pl); err != nil {
		return placing{}, err
	}
	proc.CrashAt("migrate.prepared")
	return pl, nil
}

// rewrite writes f's staged copy src into dst (index.RewriteFile): a transcript with its cwd values mapped, its lines
// counted against the manifest's; anything else as it is. The answer is the sha of what it wrote.
func rewrite(src, dst string, f File, mapCwd func(string) (string, bool)) (string, error) {
	if f.Kind == KindCopy {
		mapCwd = nil
	}
	h := sha256.New()
	lines, err := index.RewriteFile(src, dst, h, mapCwd)
	switch {
	case err != nil:
		return "", err
	case mapCwd != nil && f.Lines > 0 && lines != f.Lines:
		return "", &wire.Error{Code: wire.CodeConflict, Detail: "lines " + f.Path}
	}
	if !f.ModTime.IsZero() {
		os.Chtimes(dst, time.Now(), f.ModTime)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// put puts every file of pl in place: one already there with the sha this commit gives it counts as put (a commit
// run again), any other one there stops it. A forward commit first moves the copy it replaces to the trash. On
// failure what this call put goes back to out/ and the replaced copy comes back.
func (pl *placing) put(s *tend.Store, in Incoming, st staging) error {
	if pl.Clash == ClashForward && pl.Trash == "" {
		e, err := replaced(s, in, pl)
		if err != nil {
			return err
		}
		pl.Trash = e
		if err := fileio.WriteJSON(st.placing(), pl); err != nil {
			return err
		}
	}
	var mine []placed
	fail := func(err error) error {
		for _, f := range slices.Backward(mine) {
			if fileio.Move(f.Final, st.out(f.Path)) != nil {
				os.Remove(f.Final)
			}
		}
		if uerr := pl.undo(in, st, true); uerr != nil {
			return errors.Join(err, uerr)
		}
		return err
	}
	for _, f := range pl.Files {
		if _, err := os.Lstat(f.Final); err == nil {
			if ours(f) {
				continue
			}
			return fail(&wire.Error{Code: wire.CodeConflict, Detail: "exists " + f.Final})
		}
		if err := os.MkdirAll(filepath.Dir(f.Final), 0o700); err != nil {
			return fail(err)
		}
		if err := fileio.Move(st.out(f.Path), f.Final); err != nil {
			return fail(err)
		}
		mine = append(mine, f)
		proc.CrashAt("migrate.placing")
	}
	if pl.Trash != "" {
		return stampReplaced(in, pl)
	}
	return nil
}

// ours: the file at f.Final is the one this migration put there.
func ours(f placed) bool {
	st, err := os.Stat(f.Final)
	if err != nil {
		return false
	}
	sha, _, err := sumOf(f.Final, st)
	return err == nil && sha == f.SHA
}

// undo takes out of the Claude home what pl put there and is this migration's, back into out/ when keep, and brings
// back the copy a forward commit replaced.
func (pl *placing) undo(in Incoming, st staging, keep bool) error {
	for _, f := range pl.Files {
		if !ours(f) {
			continue
		}
		if keep {
			if err := fileio.Move(f.Final, st.out(f.Path)); err != nil {
				return err
			}
		} else if err := os.Remove(f.Final); err != nil {
			return err
		}
	}
	if pl.Trash == "" {
		return nil
	}
	if _, err := tend.RestoreTrash(in.Provider, in.SessionID); err != nil {
		return err
	}
	pl.Trash = ""
	if keep {
		return fileio.WriteJSON(st.placing(), pl)
	}
	return nil
}

// replaced moves the copy a forward commit replaces into the trash, its record kept: the answer is its entry's
// directory. After a crash between the two, the entry made then is taken.
func replaced(s *tend.Store, in Incoming, pl *placing) (string, error) {
	var files []string
	for _, id := range append([]string{in.SessionID}, in.Manifest.Aliases...) {
		files = append(files, index.SessionFiles(in.Provider, id)...)
	}
	if !slices.ContainsFunc(files, paths.Exists) {
		entries, err := tend.LoadTrash()
		if i := slices.IndexFunc(entries, func(e tend.TrashEntry) bool {
			return e.Kind == "" && e.Provider == in.Provider && e.SessionID == in.SessionID && !e.DeletedAt.Before(pl.At)
		}); i >= 0 {
			return entries[i].Dir, nil
		}
		return "", err
	}
	title := in.Title
	e := tend.TrashEntry{Provider: in.Provider, SessionID: in.SessionID, Cwd: in.Dir}
	if r := s.BySession(in.Provider, in.SessionID); r != nil {
		cp := *r
		e.Record, title = &cp, r.Title
	}
	e.Title = i18n.F("migrate.trash_replaced", title, in.From.Name)
	e, err := tend.MoveToTrash(e, files)
	return e.Dir, err
}

// stampReplaced marks each file of the replaced copy with its successor, the same manifest path in the commit's
// directory, as a project move does: a restore refuses once the new one was used, and removes it first otherwise.
func stampReplaced(in Incoming, pl *placing) error {
	entries, err := tend.LoadTrash()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(entries, func(e tend.TrashEntry) bool { return e.Dir == pl.Trash })
	if i < 0 {
		return nil
	}
	e, home := entries[i], capture.ClaudeHome()
	for j, f := range e.Files {
		rel, err := filepath.Rel(home, f.From)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		next := finalPath(filepath.ToSlash(rel), in.Dir)
		if slices.ContainsFunc(pl.Files, func(p placed) bool { return paths.Under(p.Final, next) }) {
			e.Files[j].Replaced, e.Files[j].Stamp = next, tend.FileStamp(next)
		}
	}
	return tend.SaveTrashEntry(e)
}

// NotePath is where the note of migration m is kept here, its target.
func NotePath(m string) string { return filepath.Join(tend.Home(), "migrations", m, "note.md") }

// noteText is the note the initiator wrote with what only the target knows added: the cwd values left unmapped.
func noteText(note string, unmapped []string) string {
	if len(unmapped) == 0 {
		return note
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(note, "\n"))
	b.WriteString("\n\n## " + i18n.T("migrate.note.unmapped") + "\n\n")
	b.WriteString(i18n.F("migrate.note.unmapped_count", len(unmapped)) + "\n")
	for _, p := range unmapped[:min(len(unmapped), 10)] {
		b.WriteString("- " + p + "\n")
	}
	return b.String()
}
