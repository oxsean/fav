package fulltext

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/filelock"
)

// Store layout: <dir>/<sha1(transcript path)[:16]>.tsv holds one line per entry, "off\trole\tunix\ttext" (text has no tab or
// newline); <dir>/state.json records per transcript how far it was read and how long its text file was at that point.
// ⚠️ A text file longer than its recorded size was written by a run that died before saving state: it is cut back first.
const storeVer = 6

// bytes hashed at the start and before the read offset to notice a transcript rewritten in place (fav mv changes cwd in every line)
const headLen, tailLen = 4096, 256

var ErrBusy = errors.New("full-text store is being updated by another process")

type entry struct {
	Upto  int64  `json:"upto"`  // transcript offset read so far
	Owner int64  `json:"owner"` // offset of the last message seen, for tool calls right after Upto
	Size  int64  `json:"size"`  // text file size matching Upto
	Head  string `json:"head"`  // headSum of the transcript's first min(headLen, Upto) bytes
	Mtime int64  `json:"mtime"` // transcript mtime (ns) when read: a change at the same size is a rewrite
	Src   int64  `json:"src"`   // transcript size when read (Upto stops before a partial last line)
	Tail  string `json:"tail"`  // sum of the tailLen bytes before Upto
}

type state struct {
	Ver      int               `json:"ver"`
	OutLines int               `json:"out_lines"` // tool output lines kept per call when the text was written
	Files    map[string]*entry `json:"files"`
}

func Dir() string { return filepath.Join(fav.Home(), "text") }

func textName(path string) string {
	h := sha1.Sum([]byte(path))
	return hex.EncodeToString(h[:8]) + ".tsv"
}

func loadState(dir string) *state {
	st := &state{Ver: storeVer, Files: map[string]*entry{}}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return st
	}
	var got state
	if json.Unmarshal(b, &got) != nil || got.Ver != storeVer || got.Files == nil {
		return st
	}
	return &got
}

func (st *state) save(dir string) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "state.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "state.json"))
}

// Progress of one Update: Total transcripts needed reading, Done of them are read.
type Progress struct{ Done, Total int }

// Update brings the store in line with these transcripts: new and grown ones are read from where they stopped, shrunk or
// rewritten ones from the start, vanished ones dropped. It stops early, keeping what it did, when ctx ends; a deadline in
// ctx is the time budget. progress is called after each transcript. ErrBusy: another process holds the store.
func Update(ctx context.Context, dir string, paths []string, progress func(Progress)) (Progress, error) {
	return UpdateWith(ctx, dir, paths, Options{}, progress)
}

// Options of an update: OutLines lines of each tool output are kept (0 = none); changing it rebuilds the store.
type Options struct{ OutLines int }

func UpdateWith(ctx context.Context, dir string, paths []string, opt Options, progress func(Progress)) (Progress, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Progress{}, err
	}
	unlock, err := filelock.TryLock(filepath.Join(dir, ".lock"))
	if errors.Is(err, filelock.ErrLocked) {
		return Progress{}, ErrBusy
	}
	if err != nil {
		return Progress{}, err
	}
	defer unlock()

	st, voc := loadState(dir), loadVocab(dir)
	if st.OutLines != opt.OutLines { // every text file was written with the other setting
		st.Files, st.OutLines = map[string]*entry{}, opt.OutLines
		voc = &vocab{Ver: storeVer, Words: map[string]int{}, dirty: true}
	}
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	for p := range st.Files {
		if !want[p] {
			os.Remove(filepath.Join(dir, textName(p)))
			delete(st.Files, p)
		}
	}
	sweepOrphans(dir, st)

	type job struct {
		path  string
		size  int64
		fresh bool // start over
	}
	var jobs []job
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		e := st.Files[p]
		if e == nil {
			jobs = append(jobs, job{p, fi.Size(), true})
			continue
		}
		tf, terr := os.Stat(filepath.Join(dir, textName(p)))
		switch {
		case terr != nil || tf.Size() < e.Size: // text file lost or cut short
			jobs = append(jobs, job{p, fi.Size(), true})
		case fi.Size() != e.Src:
			jobs = append(jobs, job{p, fi.Size(), false})
		case fi.ModTime().UnixNano() != e.Mtime:
			jobs = append(jobs, job{p, fi.Size(), true})
		}
	}
	prog := Progress{Total: len(jobs)}
	lastSave := time.Now()
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		e := st.Files[j.path]
		if e == nil || j.fresh || j.size < e.Upto || headSum(j.path, e.Upto) != e.Head || tailSum(j.path, e.Upto) != e.Tail { // new or rewritten: start over
			e = &entry{Owner: -1}
		}
		if err := readInto(ctx, dir, j.path, e, voc, opt.OutLines); err == nil {
			st.Files[j.path] = e
		}
		if ctx.Err() != nil { // cut short inside this transcript: kept, but not done
			break
		}
		prog.Done++
		if progress != nil {
			progress(prog)
		}
		if time.Since(lastSave) > 2*time.Second {
			st.save(dir)
			voc.save(dir)
			lastSave = time.Now()
		}
	}
	voc.save(dir)
	return prog, st.save(dir)
}

// readInto appends the entries of path past e.Upto to its text file, after cutting the file back to e.Size.
func readInto(ctx context.Context, dir, path string, e *entry, voc *vocab, outLines int) error {
	src, err := os.Stat(path) // before reading: bytes appended meanwhile make the next size differ
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, textName(path)), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(e.Size); err != nil {
		return err
	}
	if _, err := f.Seek(e.Size, 0); err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 64*1024)
	n := e.Size
	upto, owner, err := capture.Extract(ctx, path, e.Upto, e.Owner, outLines, func(x capture.Entry) {
		text := strings.Map(func(r rune) rune {
			if r == '\t' || r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, x.Text)
		voc.add(text)
		k, _ := fmt.Fprintf(w, "%d\t%c\t%d\t%s\n", x.Off, x.Role, x.At, text)
		n += int64(k)
	})
	if ferr := w.Flush(); ferr != nil {
		return ferr
	}
	if err != nil {
		return err
	}
	e.Upto, e.Owner, e.Size, e.Head, e.Tail = upto, owner, n, headSum(path, upto), tailSum(path, upto)
	e.Src, e.Mtime = src.Size(), src.ModTime().UnixNano()
	return nil
}

func headSum(path string, upto int64) string { return sumAt(path, 0, min(upto, headLen)) }

func tailSum(path string, upto int64) string {
	n := min(upto, tailLen)
	return sumAt(path, upto-n, n)
}

func sumAt(path string, from, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b := make([]byte, n)
	if _, err := f.ReadAt(b, from); err != nil && !(errors.Is(err, io.EOF) && n == 0) {
		return ""
	}
	h := sha1.Sum(b)
	return hex.EncodeToString(h[:8])
}

// sweepOrphans removes text files no state entry points at (a run died after creating one).
func sweepOrphans(dir string, st *state) {
	keep := make(map[string]bool, len(st.Files))
	for p := range st.Files {
		keep[textName(p)] = true
	}
	ents, _ := os.ReadDir(dir)
	for _, d := range ents {
		if n := d.Name(); strings.HasSuffix(n, ".tsv") && !keep[n] {
			os.Remove(filepath.Join(dir, n))
		}
	}
}

// Covered is how far the store has read path (0 when not at all): messages at or past it are not searchable yet.
func Covered(dir, path string) int64 {
	if e := loadState(dir).Files[path]; e != nil {
		return e.Upto
	}
	return 0
}
