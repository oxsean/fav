package index

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
)

// MovePlan is everything a move of old (and its subdirectories) to new touches; Plan first, show it, then Apply.
type MovePlan struct {
	Old, New string
	Sessions []MoveSession
	Live     []MoveSession // any live session blocks the move
	Records  []*fav.Rec
	Settings bool
	only     [2]string // set by Only: provider, session id
}

type MoveSession struct {
	Provider  string
	SessionID string
	Title     string
	Cwd       string
	NewCwd    string
	Files     []string
	Dirs      []string // dirs that move with it (Claude's <sid>/)
}

func (p *MovePlan) Files() int {
	n := 0
	for _, s := range p.Sessions {
		n += len(s.Files) + len(s.Dirs)
	}
	return n
}

// Touched are the rewritten / new transcripts the index must rescan from scratch.
type MoveReport struct {
	Sessions, Files, Records int
	Settings                 bool
	Trashed                  int
	Touched                  map[string]bool
}

// ClaudeProjectDir: every non-alphanumeric byte of the cwd becomes -.
var claudeEnc = regexp.MustCompile(`[^A-Za-z0-9]`)

func ClaudeProjectDir(cwd string) string {
	return filepath.Join(claudeHome(), "projects", claudeEnc.ReplaceAllString(cwd, "-"))
}

// both sides Cleaned so forward slashes in records match on Windows
func under(p, dir string) bool {
	if p == "" {
		return false
	}
	p = filepath.Clean(p)
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

func rebase(p, old, new string) string {
	p = filepath.Clean(p)
	if p == old {
		return new
	}
	return new + p[len(old):]
}

// PlanMove lists the files to change for each session under old; live is the set of sessions running now.
func (idx *Index) PlanMove(store *fav.Store, live map[string]capture.Live, old, new string) (*MovePlan, error) {
	old, new = filepath.Clean(old), filepath.Clean(new)
	if !filepath.IsAbs(old) || !filepath.IsAbs(new) {
		return nil, errors.New("paths must be absolute")
	}
	if old == new || under(new, old) {
		return nil, errors.New("target must not be the source or inside it")
	}
	plan := &MovePlan{Old: old, New: new}
	byKey := map[string]*MoveSession{}
	var order []string
	for _, f := range idx.files {
		if f.SessionID == "" || !under(f.Cwd, old) {
			continue
		}
		k := f.Provider + ":" + f.SessionID
		s := byKey[k]
		if s == nil {
			title := f.Title
			if title == "" {
				title = truncate(strings.Join(strings.Fields(f.First), " "), 60)
			}
			s = &MoveSession{Provider: f.Provider, SessionID: f.SessionID, Title: title, Cwd: f.Cwd, NewCwd: rebase(f.Cwd, old, new)}
			byKey[k] = s
			order = append(order, k)
		}
		s.Files = append(s.Files, f.Path)
		if f.Provider == fav.ProviderClaude {
			if d := strings.TrimSuffix(f.Path, ".jsonl"); dirExists(d) {
				s.Dirs = append(s.Dirs, d)
			}
		}
	}
	for _, r := range store.All() {
		if under(r.Cwd, old) || under(r.GitRoot, old) {
			plan.Records = append(plan.Records, r)
			if under(r.Cwd, old) && r.SessionID != "" && byKey[r.Provider+":"+r.SessionID] == nil && r.TranscriptPath != "" && dirExists(filepath.Dir(r.TranscriptPath)) {
				if _, err := os.Stat(r.TranscriptPath); err == nil {
					s := &MoveSession{Provider: r.Provider, SessionID: r.SessionID, Title: r.Title, Cwd: r.Cwd, NewCwd: rebase(r.Cwd, old, new), Files: []string{r.TranscriptPath}}
					byKey[r.Provider+":"+r.SessionID] = s
					order = append(order, r.Provider+":"+r.SessionID)
				}
			}
		}
	}
	sort.Strings(order)
	for _, k := range order {
		s := byKey[k]
		sort.Strings(s.Files)
		if _, ok := live[s.SessionID]; ok {
			plan.Live = append(plan.Live, *s)
			continue
		}
		plan.Sessions = append(plan.Sessions, *s)
	}
	for k := range claudeSettings() {
		if under(k, old) {
			plan.Settings = true
		}
	}
	return plan, nil
}

// Only narrows the plan to one session: M on a session in the TUI moves just that one.
func (p *MovePlan) Only(provider, sessionID string) {
	p.only = [2]string{provider, sessionID}
	keep := func(ss []MoveSession) []MoveSession {
		var out []MoveSession
		for _, s := range ss {
			if s.Provider == provider && s.SessionID == sessionID {
				out = append(out, s)
			}
		}
		return out
	}
	p.Sessions, p.Live = keep(p.Sessions), keep(p.Live)
	var recs []*fav.Rec
	for _, r := range p.Records {
		if r.Provider == provider && r.SessionID == sessionID {
			recs = append(recs, r)
		}
	}
	p.Records, p.Settings = recs, false
}

// Replan recomputes with the latest live set; a narrowed plan stays narrowed.
func (p *MovePlan) Replan(idx *Index, store *fav.Store, live map[string]capture.Live) (*MovePlan, error) {
	fresh, err := idx.PlanMove(store, live, p.Old, p.New)
	if err != nil {
		return nil, err
	}
	if p.only[1] != "" {
		fresh.Only(p.only[0], p.only[1])
	}
	return fresh, nil
}

// RescanAfterRestore: after a trash restore of a moved entry the original is back at the same path,
// possibly with the same size and mtime as the rewrite, so the index must rescan it from scratch.
func RescanAfterRestore(e fav.TrashEntry) map[string]bool {
	force := map[string]bool{}
	for _, f := range e.Files {
		if f.Replaced != "" {
			force[f.From] = true
		}
	}
	return force
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Apply rewrites cwd in each transcript into its new location (originals go to trash), moves Claude session dirs into the new
// project dir, sweeps what is left of the old project dir (memory/ …), and rebases store records and ~/.claude.json.
func (p *MovePlan) Apply(store *fav.Store) (MoveReport, error) {
	rep := MoveReport{Touched: map[string]bool{}}
	if len(p.Live) > 0 {
		return rep, fmt.Errorf("%d sessions still running", len(p.Live))
	}
	// re-check: the plan was computed earlier and a session may have started while the dialog was open
	live := capture.LiveSessions()
	for _, s := range p.Sessions {
		if _, ok := live[s.SessionID]; ok {
			return rep, fmt.Errorf("session %s started running: %s", s.SessionID, s.Title)
		}
	}
	// a file already at the target (restored from trash and moved again) aborts the whole move; never overwritten
	for _, s := range p.Sessions {
		if s.Provider != fav.ProviderClaude {
			continue
		}
		for _, f := range append(append([]string{}, s.Files...), s.Dirs...) {
			if dst := filepath.Join(ClaudeProjectDir(s.NewCwd), filepath.Base(f)); dst != f {
				if _, err := os.Lstat(dst); err == nil {
					return rep, fmt.Errorf("destination already exists: %s", dst)
				}
			}
		}
	}
	oldDirs := map[string]bool{}
	for _, s := range p.Sessions {
		// Originals go to trash first and the rewrite reads from there: Codex rewrites in place and would clobber the original.
		// The trash entry records the rewritten files, the moved dirs and the pre-move record so the whole move can be undone.
		e := fav.TrashEntry{Provider: s.Provider, SessionID: s.SessionID, Title: s.Title + " (moved " + p.Old + " → " + p.New + ")", Cwd: s.Cwd}
		for _, r := range p.Records {
			if r.Provider == s.Provider && r.SessionID == s.SessionID {
				cp := *r
				e.Record = &cp
			}
		}
		e, err := fav.MoveToTrash(e, s.Files)
		if err != nil {
			return rep, err
		}
		rep.Trashed += len(e.Files)
		var moved []string
		for i, f := range e.Files {
			dst := f.From
			if s.Provider == fav.ProviderClaude {
				dst = filepath.Join(ClaudeProjectDir(s.NewCwd), filepath.Base(f.From))
				oldDirs[filepath.Dir(f.From)] = true
			}
			if err := rewriteCwd(f.To, dst, p.Old, p.New); err != nil {
				return rep, fmt.Errorf("%s: %w", f.From, err)
			}
			e.Files[i].Replaced, e.Files[i].Stamp = dst, fav.FileStamp(dst)
			moved = append(moved, dst)
			rep.Touched[dst] = true
			rep.Files++
		}
		for _, d := range s.Dirs {
			dst := filepath.Join(ClaudeProjectDir(s.NewCwd), filepath.Base(d))
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return rep, err
			}
			if err := os.Rename(d, dst); err != nil {
				return rep, fmt.Errorf("%s: %w", d, err)
			}
			e.Files = append(e.Files, fav.Moved{From: d, To: dst, Relocated: true})
			rep.Files++
		}
		if err := fav.SaveTrashEntry(e); err != nil {
			return rep, err
		}
		rep.Sessions++
		for _, r := range p.Records {
			if r.Provider == s.Provider && r.SessionID == s.SessionID && len(moved) > 0 {
				r.TranscriptPath = moved[0]
				if r.PinnedPath != "" {
					os.Remove(r.PinnedPath)
					if err := os.Link(moved[0], r.PinnedPath); err != nil {
						r.PinnedPath = ""
					}
				}
			}
		}
	}
	for _, r := range p.Records {
		if under(r.Cwd, p.Old) {
			r.Cwd = rebase(r.Cwd, p.Old, p.New)
		}
		if under(r.GitRoot, p.Old) {
			r.GitRoot = rebase(r.GitRoot, p.Old, p.New)
		}
		if r.Project == filepath.Base(p.Old) {
			r.Project = filepath.Base(p.New)
		}
		if err := store.Put(r); err != nil {
			return rep, err
		}
		rep.Records++
	}
	for d := range oldDirs {
		sweepProjectDir(d, ClaudeProjectDir(rebase(decodeHint(d, p), p.Old, p.New)))
	}
	if p.Settings {
		if err := moveClaudeSettings(p.Old, p.New); err != nil {
			return rep, err
		}
		rep.Settings = true
	}
	return rep, nil
}

// the project dir name is a one-way encoding; the cwd is recovered from the plan
func decodeHint(dir string, p *MovePlan) string {
	for _, s := range p.Sessions {
		if s.Provider == fav.ProviderClaude && len(s.Files) > 0 && filepath.Dir(s.Files[0]) == dir {
			return s.Cwd
		}
	}
	return p.Old
}

// sweepProjectDir: once the old project dir has no transcripts left, move the rest (memory/ …) over and remove it when empty.
func sweepProjectDir(old, new string) {
	if hits, _ := filepath.Glob(filepath.Join(old, "*.jsonl")); len(hits) > 0 {
		return
	}
	entries, err := os.ReadDir(old)
	if err != nil {
		return
	}
	os.MkdirAll(new, 0o700)
	for _, e := range entries {
		dst := filepath.Join(new, e.Name())
		if _, err := os.Lstat(dst); err == nil {
			continue // keep the existing one at the target
		}
		os.Rename(filepath.Join(old, e.Name()), dst)
	}
	os.Remove(old) // non-empty stays
}

var cwdKey = []byte(`"cwd":"`)

// rewriteCwd rewrites "cwd":"<old…>" and "cwd":"file://<old…>" line by line into dst (tmp + rename).
// ⚠️ Only the cwd field: paths in message bodies are history and must match what happened.
func rewriteCwd(src, dst, old, new string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".fav-mv"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(out, 1<<20)
	oldQ, newQ := jsonPath(old), jsonPath(new)
	r := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := w.Write(replaceCwd(line, oldQ, newQ)); werr != nil {
				out.Close()
				os.Remove(tmp)
				return werr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil { // a read error must not look like EOF: renaming a partial file loses data
			out.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if st, err := os.Stat(src); err == nil {
		os.Chtimes(tmp, time.Now(), st.ModTime())
	}
	return os.Rename(tmp, dst)
}

// Windows backslashes are escaped inside JSON strings
func jsonPath(p string) string {
	b, _ := json.Marshal(p)
	return string(b[1 : len(b)-1])
}

func replaceCwd(line []byte, old, new string) []byte {
	if !bytes.Contains(line, cwdKey) {
		return line
	}
	for _, prefix := range []string{`"cwd":"`, `"cwd":"file://`} {
		from := []byte(prefix + old)
		i := 0
		for {
			j := bytes.Index(line[i:], from)
			if j < 0 {
				break
			}
			j += i
			end := j + len(from)
			if end < len(line) && line[end] != '"' && line[end] != '/' && line[end] != '\\' {
				i = end
				continue
			}
			line = append(append(append([]byte{}, line[:j]...), []byte(prefix+new)...), line[end:]...)
			i = j + len(prefix) + len(new)
		}
	}
	return line
}

func claudeSettingsPath() string {
	if h := os.Getenv("CLAUDE_CONFIG_DIR"); h != "" {
		return filepath.Join(h, ".claude.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

func claudeSettings() map[string]json.RawMessage {
	b, err := os.ReadFile(claudeSettingsPath())
	if err != nil {
		return nil
	}
	var top struct {
		Projects map[string]json.RawMessage `json:"projects"`
	}
	json.Unmarshal(b, &top)
	return top.Projects
}

// moveClaudeSettings renames projects[old…] keys to new… in ~/.claude.json, every other byte untouched; writes a .bak first.
func moveClaudeSettings(old, new string) error {
	path := claudeSettingsPath()
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return err
	}
	var projects map[string]json.RawMessage
	if err := json.Unmarshal(top["projects"], &projects); err != nil {
		return err
	}
	changed := false
	for k, v := range projects {
		if !under(k, old) {
			continue
		}
		nk := rebase(k, old, new)
		if _, exists := projects[nk]; exists {
			continue
		}
		projects[nk] = v
		delete(projects, k)
		changed = true
	}
	if !changed {
		return nil
	}
	pb, err := json.Marshal(projects)
	if err != nil {
		return err
	}
	top["projects"] = pb
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".bak-"+time.Now().Format("20060102-150405"), b, 0o600); err != nil {
		return err
	}
	tmp := path + ".fav-mv"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Missing is a cwd no longer on disk, with its session count and the recorded remote.
type Missing struct {
	Dir      string
	Sessions int
	Remote   string
	Found    []string // guessed new location; auto-fix only when there is exactly one
}

// FindMissing lists cwds from sessions / records that no longer exist and looks for the basename under the parents of
// existing cwds and next to the old dir; a recorded remote must match origin. only limits it to one dir (glob + git exec once).
func (idx *Index) FindMissing(store *fav.Store, only string) []Missing {
	dead := map[string]*Missing{}
	parents := map[string]bool{}
	exists := map[string]bool{} // hundreds of sessions share a cwd: stat once
	note := func(cwd, remote string) {
		if cwd == "" {
			return
		}
		ok, seen := exists[cwd]
		if !seen {
			ok = dirExists(cwd)
			exists[cwd] = ok
		}
		if ok {
			parents[filepath.Dir(cwd)] = true
			return
		}
		if only != "" && cwd != only {
			return
		}
		m := dead[cwd]
		if m == nil {
			m = &Missing{Dir: cwd}
			dead[cwd] = m
		}
		m.Sessions++
		if remote != "" {
			m.Remote = remote
		}
	}
	for _, s := range idx.Sessions() {
		note(s.Cwd, "")
	}
	for _, r := range store.All() {
		note(r.Cwd, r.GitRemote)
	}
	var out []Missing
	for _, m := range dead {
		base := filepath.Base(m.Dir)
		cands := map[string]bool{}
		for parent := range parents {
			cands[filepath.Join(parent, base)] = true
		}
		// siblings of the old dir too (~/work/x → ~/dev/x)
		siblings, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(m.Dir)), "*", base))
		for _, c := range siblings {
			cands[c] = true
		}
		for cand := range cands {
			if !dirExists(cand) || cand == m.Dir || under(cand, m.Dir) {
				continue
			}
			if m.Remote != "" && capture.GitOut(cand, "remote", "get-url", "origin") != m.Remote {
				continue
			}
			m.Found = append(m.Found, cand)
		}
		sort.Strings(m.Found)
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out
}
