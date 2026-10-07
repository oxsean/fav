package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/wire"
)

// Node methods reading what a run changed (feature-free: an older node has none of them, hello.methods tells).
const (
	MRunChanges = "run.changes" // the files a run changed (ChangesParams)
	MRunDiff    = "run.diff"    // a file's hunks (DiffParams)
	MRunBlob    = "run.blob"    // a blob's content (BlobParams)
)

// FeatureIgnoreSpace: run.diff takes ignore_space; a node without it would answer the plain diff.
const FeatureIgnoreSpace = "ignore_space"

const (
	changesFile   = "changes.json"
	changesPage   = 500       // files a run.changes page lists
	diffPage      = 256 << 10 // bytes of lines a run.diff page holds, and a run.blob page
	bigLines      = 400       // a file's change past this many lines starts folded
	bigBytes      = 256 << 10 // so does a file past this size
	defaultHunks  = 20
	defaultCtx    = 3
	maxCtx        = 10000
	maxDiffSource = 64 << 20 // a file outside git bigger than this is counted as binary
)

type ChangesParams struct {
	Run      string `json:"run"`
	After    string `json:"after,omitempty"`    // the last path of the page before
	Snapshot string `json:"snapshot,omitempty"` // the page before's: another one answers snapshot_changed
	All      bool   `json:"all,omitempty"`      // the machine's owner asks: a directory run lists every file, not only the agent's
}

// FileChange is one file a run changed.
type FileChange struct {
	Path      string `json:"path"` // relative to the run's directory, / separated
	Op        string `json:"op"`   // add | modify | delete | rename
	From      string `json:"from,omitempty"`
	Add       int    `json:"add"`
	Del       int    `json:"del"`
	Bytes     int64  `json:"bytes"`               // its size now
	OldBytes  int64  `json:"old_bytes,omitempty"` // binary: its size before
	Binary    bool   `json:"binary,omitempty"`
	Generated bool   `json:"generated,omitempty"`
	Big       bool   `json:"big,omitempty"`
	Agent     bool   `json:"agent,omitempty"` // the agent's tools edited it or its commands named it
}

type ChangeTotal struct {
	Files int `json:"files"`
	Add   int `json:"add"`
	Del   int `json:"del"`
}

type Changes struct {
	Files    []FileChange `json:"files"`
	Total    ChangeTotal  `json:"total"`
	Snapshot string       `json:"snapshot,omitempty"`
	Git      bool         `json:"git"`
	Hidden   int          `json:"hidden,omitempty"` // files only the machine's owner sees (All)
	Next     string       `json:"next,omitempty"`   // After for the next page; none on the last
}

type DiffParams struct {
	Run      string `json:"run"`
	Path     string `json:"path"`
	Snapshot string `json:"snapshot,omitempty"`
	Hunk     int    `json:"hunk,omitempty"`    // the first hunk of the page
	Line     int    `json:"line,omitempty"`    // within it, the first line
	N        int    `json:"n,omitempty"`       // hunks at most (default 20)
	Context  *int   `json:"context,omitempty"` // lines around each change (default 3)
	All      bool   `json:"all,omitempty"`
	// IgnoreSpace compares lines as git diff -w does: a file changed only in its whitespace has no hunks. The list
	// (run.changes) counts every change.
	IgnoreSpace bool `json:"ignore_space,omitempty"`
}

type DiffNext struct {
	Hunk int `json:"hunk"`
	Line int `json:"line,omitempty"`
}

type Diff struct {
	Hunks []hunk    `json:"hunks"`
	Of    int       `json:"of"` // the file's hunks in all
	Next  *DiffNext `json:"next,omitempty"`
}

type BlobParams struct {
	Run string `json:"run"`
	Sha string `json:"sha"`
	Off int64  `json:"off,omitempty"`
	N   int    `json:"n,omitempty"`
}

type Blob struct {
	Text string `json:"text"`
	Off  int64  `json:"off"`
	Size int64  `json:"size"`
	Next int64  `json:"next,omitempty"` // Off for the next page; none at the end
}

// changeSet is what changes.json holds: every file, the agent's marked.
type changeSet struct {
	Git      bool          `json:"git"`
	Snapshot string        `json:"snapshot"` // git: the tree compared with the base
	Files    []changedFile `json:"files"`
}

type changedFile struct {
	FileChange
	Base string `json:"base,omitempty"` // outside git: the blob of the file before and after
	End  string `json:"end,omitempty"`
	Lost bool   `json:"lost,omitempty"` // outside git: what it was before is not known
}

var (
	errGone     = &wire.Error{Code: wire.CodeGone}
	errSnapshot = &wire.Error{Code: wire.CodeSnapshotChanged}
	shaHex      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// gitChanges compares tree with the base: every file under the run's directory, the agent's marked.
func gitChanges(tr trees, dir, tree string, touched []touch) ([]changedFile, error) {
	var spec []string
	if tr.Prefix != "" {
		spec = []string{"--", tr.Prefix}
	}
	g := tr.repo()
	raw, err := gitInput(g, "", append([]string{"diff-tree", "-r", "-z", "-M", "--raw", tr.Base, tree}, spec...)...)
	if err != nil {
		return nil, err
	}
	num, err := gitInput(g, "", append([]string{"diff-tree", "-r", "-z", "-M", "--numstat", tr.Base, tree}, spec...)...)
	if err != nil {
		return nil, err
	}
	type entry struct {
		c        changedFile
		old, new string
	}
	var es []entry
	f := strings.Split(raw, "\x00")
	for i := 0; i < len(f); i++ {
		head := f[i]
		if !strings.HasPrefix(head, ":") {
			continue
		}
		p := strings.Fields(head[1:])
		if len(p) < 5 || i+1 >= len(f) {
			break
		}
		e := entry{old: p[2], new: p[3]}
		status := p[4][:1]
		e.c.Path = f[i+1]
		i++
		if status == "R" || status == "C" {
			if i+1 >= len(f) {
				break
			}
			e.c.From, e.c.Path = e.c.Path, f[i+1]
			i++
		}
		switch status {
		case "A", "C":
			e.c.Op, e.c.From = "add", ""
		case "D":
			e.c.Op = "delete"
		case "R":
			e.c.Op = "rename"
		default:
			e.c.Op = "modify"
		}
		es = append(es, e)
	}
	// numstat -z: "add\tdel\tpath\0", or "add\tdel\t\0from\0to\0" for a rename, in the same order
	nf := strings.Split(num, "\x00")
	k := 0
	for i := 0; i < len(nf) && k < len(es); i++ {
		p := strings.SplitN(nf[i], "\t", 3)
		if len(p) < 3 {
			continue
		}
		if p[2] == "" {
			i += 2
		}
		if p[0] == "-" {
			es[k].c.Binary = true
		} else {
			es[k].c.Add, _ = strconv.Atoi(p[0])
			es[k].c.Del, _ = strconv.Atoi(p[1])
		}
		k++
	}
	var ids strings.Builder
	for _, e := range es {
		ids.WriteString(e.old + "\n" + e.new + "\n")
	}
	sizes := map[string]int64{}
	if out, err := gitInput(g, ids.String(), "cat-file", "--batch-check=%(objectname) %(objectsize)"); err == nil {
		for _, l := range strings.Split(out, "\n") {
			if id, n, ok := strings.Cut(l, " "); ok {
				sizes[id], _ = strconv.ParseInt(n, 10, 64)
			}
		}
	}
	agent := agentPaths(dir, touched)
	out := make([]changedFile, 0, len(es))
	for _, e := range es {
		c := e.c
		c.Path, c.From = strings.TrimPrefix(c.Path, tr.Prefix), strings.TrimPrefix(c.From, tr.Prefix)
		c.Bytes = sizes[e.new]
		if c.Binary {
			c.OldBytes = sizes[e.old]
		}
		c.Big = c.Add+c.Del > bigLines || c.Bytes > bigBytes
		c.Generated = generatedName(c.Path)
		c.Agent = agent(c.Path) || c.From != "" && agent(c.From)
		out = append(out, c)
	}
	markGenerated(dir, out)
	slices.SortFunc(out, func(a, b changedFile) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// agentPaths tells the paths (relative to dir) the agent touched: edited, or named by a command (a directory named
// covers what is under it).
func agentPaths(dir string, touched []touch) func(string) bool {
	roots := []string{filepath.Clean(dir)}
	if real, err := realPath(dir); err == nil && real != roots[0] {
		roots = append(roots, real)
	}
	edits, named := map[string]bool{}, map[string]bool{}
	for _, t := range touched {
		for _, root := range roots {
			rel, ok := paths.Inside(root, t.Path)
			if !ok {
				continue
			}
			if rel = filepath.ToSlash(rel); t.Via == viaEdit {
				edits[rel] = true
			} else {
				named[rel] = true
			}
		}
	}
	return func(p string) bool {
		if edits[p] {
			return true
		}
		for q := p; q != "." && q != "/" && q != ""; q = path.Dir(q) {
			if named[q] {
				return true
			}
		}
		return false
	}
}

var generatedBase = map[string]bool{"go.sum": true, "go.work.sum": true, "package-lock.json": true, "yarn.lock": true,
	"pnpm-lock.yaml": true, "Cargo.lock": true, "poetry.lock": true, "Gemfile.lock": true, "composer.lock": true,
	"uv.lock": true, "bun.lockb": true, "Pipfile.lock": true, "flake.lock": true}

// generatedName tells a file by its name to be generated.
func generatedName(p string) bool {
	base := path.Base(p)
	if generatedBase[base] {
		return true
	}
	for _, suf := range []string{".min.js", ".min.css", ".pb.go", "_pb2.py", ".pb.ts", ".map"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	for _, d := range strings.Split(path.Dir(p), "/") {
		if d == "vendor" || d == "dist" || d == "node_modules" {
			return true
		}
	}
	return false
}

// markGenerated marks the files .gitattributes says are linguist-generated.
func markGenerated(dir string, files []changedFile) {
	if len(files) == 0 {
		return
	}
	var in strings.Builder
	for _, f := range files {
		in.WriteString(f.Path + "\x00")
	}
	out, err := gitInput(gitIn{dir: dir}, in.String(), "check-attr", "-z", "--stdin", "linguist-generated")
	if err != nil {
		return
	}
	gen := map[string]bool{}
	f := strings.Split(out, "\x00")
	for i := 0; i+2 < len(f); i += 3 {
		if v := f[i+2]; v == "set" || v == "true" {
			gen[f[i]] = true
		}
	}
	for i := range files {
		files[i].Generated = files[i].Generated || gen[files[i].Path]
	}
}

// localChanges is what the agent's tools changed in a directory outside git: each file it edited, from what it was
// before the first edit to what it is now. keepEnd keeps each file's content now in a blob (the run ended).
func localChanges(runDir, dir string, touched []touch, keepEnd *slimmer) ([]changedFile, string) {
	seen := map[string]bool{}
	var out []changedFile
	snap := sha256.New()
	for _, t := range touched {
		if t.Via != viaEdit || seen[t.Path] {
			continue
		}
		seen[t.Path] = true
		rel, ok := paths.Inside(dir, t.Path)
		if !ok {
			continue // the agent's tools wrote outside the run's directory
		}
		cur, err := os.ReadFile(t.Path)
		exists := err == nil
		sum := sha256.Sum256(cur)
		snap.Write([]byte(t.Path + "\x00" + strconv.FormatBool(exists) + hex.EncodeToString(sum[:]) + "\n"))
		c := changedFile{FileChange: FileChange{Path: filepath.ToSlash(rel), Agent: true, Bytes: int64(len(cur))}, Base: t.Base, Lost: t.Lost}
		var before []byte
		switch {
		case t.New && !exists:
			continue
		case t.New:
			c.Op = "add"
		case !exists:
			c.Op = "delete"
		default:
			c.Op = "modify"
		}
		if !t.New && !t.Lost {
			if before, err = os.ReadFile(filepath.Join(runDir, blobsDir, t.Base)); err != nil {
				c.Lost = true
			}
		}
		if c.Op == "modify" && !c.Lost && bytes.Equal(before, cur) {
			continue
		}
		if !c.Lost {
			if binary(before) || binary(cur) {
				c.Binary, c.OldBytes = true, int64(len(before))
			} else {
				for _, h := range lineDiff(string(before), string(cur), 0, false) {
					for _, l := range h.Lines {
						switch l[0] {
						case '+':
							c.Add++
						case '-':
							c.Del++
						}
					}
				}
			}
		}
		if keepEnd != nil && exists {
			c.End, _ = keepEnd.keep(cur)
		}
		c.Big = c.Add+c.Del > bigLines || c.Bytes > bigBytes
		c.Generated = generatedName(c.Path)
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b changedFile) int { return strings.Compare(a.Path, b.Path) })
	return out, hex.EncodeToString(snap.Sum(nil)[:12])
}

func binary(b []byte) bool {
	return len(b) > maxDiffSource || bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 || !utf8.Valid(b[:min(len(b), 8000)])
}

// endChanges records what the run changed when it ends: the end tree and its ref, then changes.json.
func (s *sup) endChanges() {
	var tr trees
	if fileio.ReadJSON(filepath.Join(s.dir, treesFile), &tr) != nil {
		return
	}
	cs := changeSet{Git: tr.Git}
	touched := readTouched(s.dir)
	if tr.Git {
		if tr.Base == "" {
			return
		}
		end, err := takeTree(s.spec.Dir, filepath.Join(s.dir, endIndex), filepath.Join(s.dir, baseIndex))
		if err != nil {
			s.warnChanges(err)
			return
		}
		tr.End = end
		tr.ref(s.spec.Run, "end", end)
		fileio.WriteJSON(filepath.Join(s.dir, treesFile), tr)
		if cs.Files, err = gitChanges(tr, s.spec.Dir, end, touched); err != nil {
			s.warnChanges(err)
			return
		}
		cs.Snapshot = end
	} else {
		cs.Files, cs.Snapshot = localChanges(s.dir, s.spec.Dir, touched, s.slim)
	}
	fileio.WriteJSON(filepath.Join(s.dir, changesFile), cs)
	os.Remove(filepath.Join(s.dir, liveIndex))
}

// startChanges records where the run's changes are counted from: in git, the tree of its workspace now and its ref.
func (s *sup) startChanges() {
	tr := trees{}
	if s.slim.git {
		var err error
		if tr, err = takeTrees(s.spec.Dir); err != nil {
			s.warnChanges(err)
			return
		}
		base, err := takeTree(s.spec.Dir, filepath.Join(s.dir, baseIndex), "")
		if err != nil {
			s.warnChanges(err)
			return
		}
		tr.Base = base
		if err := tr.ref(s.spec.Run, "base", base); err != nil {
			s.warnChanges(err)
		}
	}
	fileio.WriteJSON(filepath.Join(s.dir, treesFile), tr)
}

// warnChanges logs why the run's changes cannot be counted; its methods answer gone.
func (s *sup) warnChanges(err error) {
	s.log.Write([]byte("tend: the run's changes are not recorded: " + err.Error() + "\n"))
}

// liveReuse is how long a running run's changes are answered from the last comparison. A variable for the tests.
var liveReuse = 2 * time.Second

// liveChanges is a running run's changes as last compared.
type liveChanges struct {
	at time.Time
	cs changeSet
}

// changesOf is run's changes: changes.json once it ended, compared with its workspace now while it runs (reused
// for liveReuse).
func (n *Node) changesOf(run string) (changeSet, Spec, trees, error) {
	var spec Spec
	var tr trees
	if !runID.MatchString(run) {
		return changeSet{}, spec, tr, &wire.Error{Code: wire.CodeNotFound, Detail: run}
	}
	dir := n.runDir(run)
	if err := fileio.ReadJSON(filepath.Join(dir, "spec.json"), &spec); err != nil {
		return changeSet{}, spec, tr, &wire.Error{Code: wire.CodeNotFound, Detail: run}
	}
	if fileio.ReadJSON(filepath.Join(dir, treesFile), &tr) != nil || tr.Git && tr.Base == "" {
		return changeSet{}, spec, tr, errGone
	}
	var cs changeSet
	if fileio.ReadJSON(filepath.Join(dir, changesFile), &cs) == nil {
		return cs, spec, tr, nil
	}
	snap, err := n.Snapshot(run)
	if err != nil || Terminal(snap.State.State) || snap.State.State == StateUnknown {
		return changeSet{}, spec, tr, errGone
	}
	n.changesMu.Lock()
	defer n.changesMu.Unlock()
	if l, ok := n.live[run]; ok && time.Since(l.at) < liveReuse {
		return l.cs, spec, tr, nil
	}
	cs = changeSet{Git: tr.Git}
	if tr.Git {
		tree, err := takeTree(spec.Dir, filepath.Join(dir, liveIndex), filepath.Join(dir, baseIndex))
		if err != nil {
			return changeSet{}, spec, tr, &wire.Error{Code: wire.CodeInternal, Detail: err.Error()}
		}
		if cs.Files, err = gitChanges(tr, spec.Dir, tree, readTouched(dir)); err != nil {
			return changeSet{}, spec, tr, &wire.Error{Code: wire.CodeInternal, Detail: err.Error()}
		}
		cs.Snapshot = tree
	} else {
		cs.Files, cs.Snapshot = localChanges(dir, spec.Dir, readTouched(dir), nil)
	}
	if n.live == nil {
		n.live = map[string]liveChanges{}
	}
	for id, l := range n.live {
		if time.Since(l.at) > time.Minute {
			delete(n.live, id)
		}
	}
	n.live[run] = liveChanges{time.Now(), cs}
	return cs, spec, tr, nil
}

// visible is what of cs this reader sees: a directory run shows others only the agent's files, and how many more
// there are.
func visible(cs changeSet, spec Spec, all bool) (files []changedFile, hidden int) {
	if all || spec.Work != nil {
		return cs.Files, 0
	}
	for _, f := range cs.Files {
		if f.Agent {
			files = append(files, f)
		} else {
			hidden++
		}
	}
	return files, hidden
}

// Changes lists the files run changed, a page at a time.
func (n *Node) Changes(p ChangesParams) (Changes, error) {
	cs, spec, _, err := n.changesOf(p.Run)
	if err != nil {
		return Changes{}, err
	}
	if p.Snapshot != "" && p.Snapshot != cs.Snapshot {
		return Changes{}, errSnapshot
	}
	files, hidden := visible(cs, spec, p.All)
	res := Changes{Files: []FileChange{}, Snapshot: cs.Snapshot, Git: cs.Git, Hidden: hidden}
	for _, f := range files {
		res.Total.Files++
		res.Total.Add += f.Add
		res.Total.Del += f.Del
	}
	i := sort.Search(len(files), func(i int) bool { return files[i].Path > p.After })
	for ; i < len(files) && len(res.Files) < changesPage; i++ {
		res.Files = append(res.Files, files[i].FileChange)
	}
	if i < len(files) {
		res.Next = res.Files[len(res.Files)-1].Path
	}
	return res, nil
}

// Diff pages through the hunks of one file run changed.
func (n *Node) Diff(p DiffParams) (Diff, error) {
	cs, spec, tr, err := n.changesOf(p.Run)
	if err != nil {
		return Diff{}, err
	}
	if p.Snapshot != "" && p.Snapshot != cs.Snapshot {
		return Diff{}, errSnapshot
	}
	files, _ := visible(cs, spec, p.All)
	i := slices.IndexFunc(files, func(f changedFile) bool { return f.Path == p.Path })
	if i < 0 {
		return Diff{}, &wire.Error{Code: wire.CodeNotFound, Detail: p.Path}
	}
	f := files[i]
	ctx := defaultCtx
	if p.Context != nil {
		ctx = min(max(*p.Context, 0), maxCtx)
	}
	var hunks []hunk
	switch {
	case f.Binary:
	case cs.Git:
		hunks, err = gitHunks(tr, cs.Snapshot, f, ctx, p.IgnoreSpace)
	default:
		hunks, err = n.localHunks(p.Run, spec, f, ctx, p.IgnoreSpace)
	}
	if err != nil {
		return Diff{}, err
	}
	return page(hunks, p.Hunk, p.Line, p.N), nil
}

// gitHunks is the diff of f between the base and tree.
func gitHunks(tr trees, tree string, f changedFile, ctx int, ignoreSpace bool) ([]hunk, error) {
	paths := []string{tr.Prefix + f.Path}
	if f.From != "" {
		paths = append(paths, tr.Prefix+f.From)
	}
	args := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M", "-U" + strconv.Itoa(ctx)}
	if ignoreSpace {
		args = append(args, "-w")
	}
	out, err := gitInput(tr.repo(), "", append(append(args, tr.Base, tree, "--"), paths...)...)
	if err != nil {
		return nil, &wire.Error{Code: wire.CodeGone, Detail: err.Error()}
	}
	return parseHunks(out), nil
}

// parseHunks reads the hunks of a unified diff.
func parseHunks(diff string) []hunk {
	var out []hunk
	for l := range strings.Lines(diff) {
		l = strings.TrimSuffix(l, "\n")
		switch {
		case strings.HasPrefix(l, "@@"):
			out = append(out, hunk{At: l})
		case len(out) > 0 && l != "" && strings.ContainsRune(" -+\\", rune(l[0])):
			out[len(out)-1].Lines = append(out[len(out)-1].Lines, l)
		}
	}
	return out
}

// localHunks is the diff of f outside git: from its first base to its end (or to the file now while it runs).
func (n *Node) localHunks(run string, spec Spec, f changedFile, ctx int, ignoreSpace bool) ([]hunk, error) {
	dir := n.runDir(run)
	if f.Lost || fileExists(filepath.Join(dir, blobsGone)) {
		return nil, errGone
	}
	read := func(sha string) (string, error) {
		if sha == "" {
			return "", nil
		}
		b, err := os.ReadFile(filepath.Join(dir, blobsDir, sha))
		if err != nil {
			return "", errGone
		}
		return string(b), nil
	}
	before, err := read(f.Base)
	if err != nil {
		return nil, err
	}
	var after string
	if f.End != "" || f.Op == "delete" {
		after, err = read(f.End)
	} else {
		var b []byte
		b, err = os.ReadFile(filepath.Join(spec.Dir, filepath.FromSlash(f.Path)))
		after = string(b)
	}
	if err != nil {
		return nil, errGone
	}
	return lineDiff(before, after, ctx, ignoreSpace), nil
}

// page is hunks from hunk (its line on) to n hunks or diffPage bytes; a hunk past diffPage goes on by its lines.
func page(hunks []hunk, from, line, n int) Diff {
	if n <= 0 {
		n = defaultHunks
	}
	res := Diff{Hunks: []hunk{}, Of: len(hunks)}
	size := 0
	for h := max(from, 0); h < len(hunks) && len(res.Hunks) < n; h++ {
		start := 0
		if h == from {
			start = min(max(line, 0), len(hunks[h].Lines))
		}
		lines := hunks[h].Lines[start:]
		need := len(hunks[h].At)
		for _, l := range lines {
			need += len(l) + 1
		}
		if size+need > diffPage {
			if len(res.Hunks) > 0 {
				res.Next = &DiffNext{Hunk: h}
				return res
			}
			k, used := 0, len(hunks[h].At)
			for k < len(lines) && (k == 0 || used+len(lines[k])+1 <= diffPage) {
				used += len(lines[k]) + 1
				k++
			}
			res.Hunks = append(res.Hunks, hunk{At: hunks[h].At, Lines: lines[:k]})
			if start+k < len(hunks[h].Lines) {
				res.Next = &DiffNext{Hunk: h, Line: start + k}
			} else if h+1 < len(hunks) {
				res.Next = &DiffNext{Hunk: h + 1}
			}
			return res
		}
		size += need
		res.Hunks = append(res.Hunks, hunk{At: hunks[h].At, Lines: lines})
	}
	if last := max(from, 0) + len(res.Hunks); last < len(hunks) {
		res.Next = &DiffNext{Hunk: last}
	}
	return res
}

// BlobOf pages through a blob of run's.
func (n *Node) BlobOf(p BlobParams) (Blob, error) {
	if !runID.MatchString(p.Run) || !shaHex.MatchString(p.Sha) {
		return Blob{}, &wire.Error{Code: wire.CodeNotFound}
	}
	dir := n.runDir(p.Run)
	if fileExists(filepath.Join(dir, blobsGone)) {
		return Blob{}, errGone
	}
	f, err := os.Open(filepath.Join(dir, blobsDir, p.Sha))
	if err != nil {
		if !fileExists(dir) {
			return Blob{}, errGone
		}
		return Blob{}, &wire.Error{Code: wire.CodeNotFound, Detail: p.Sha}
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Blob{}, err
	}
	size, want := fi.Size(), p.N
	if want <= 0 || want > diffPage {
		want = diffPage
	}
	off := min(max(p.Off, 0), size)
	buf := make([]byte, want+utf8.UTFMax)
	got, err := f.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return Blob{}, err
	}
	buf = buf[:got]
	end := min(want, len(buf))
	if end < len(buf) { // ⚠️ a page ends where a character starts
		for back := 0; back < utf8.UTFMax && end > 1 && !utf8.RuneStart(buf[end]); back++ {
			end--
		}
	}
	res := Blob{Text: string(buf[:end]), Off: off, Size: size}
	if off+int64(end) < size {
		res.Next = off + int64(end)
	}
	return res, nil
}
