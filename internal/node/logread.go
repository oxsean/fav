package node

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/wire"
)

type TailParams struct {
	Run    string `json:"run"`
	Before int64  `json:"before"` // < 0: from the end
	Max    int    `json:"max"`    // bytes
	File   string `json:"file,omitempty"`
	Clip   bool   `json:"clip,omitempty"` // Lines, long lines cut, instead of Text; an older node answers Text
}

type Tail struct {
	Text  string        `json:"text,omitempty"`
	Lines []TailLine    `json:"lines,omitempty"` // Clip
	From  int64         `json:"from"`            // where the page starts; the next page ends here
	File  string        `json:"file"`            // the log's identity: another one answers stale
	Done  bool          `json:"done"`            // the page starts at the beginning of this file
	Prev  string        `json:"prev,omitempty"`  // Done in the current log: the log before it, still kept
	Turn  *output.State `json:"turn,omitempty"`  // where the turns stand at From, from marks.jsonl
}

// TailLine is a line of a Clip page, as clipLine sends it.
type TailLine struct {
	Off  int64  `json:"off"`
	Text string `json:"text"`           // with its newline, but for a head or the last line still being written
	Size int64  `json:"size,omitempty"` // the whole line's length, when Text is cut
	Head bool   `json:"head,omitempty"` // Text is only the line's start
}

type LineParams struct {
	Run  string `json:"run"`
	File string `json:"file,omitempty"`
	Off  int64  `json:"off"` // where the line starts
	Max  int    `json:"max,omitempty"`
}

type Line struct {
	Text string `json:"text"` // up to Max
	Size int64  `json:"size"` // the whole line's length, its newline too
	File string `json:"file"`
}

var errNoLog = errors.New("no log")

// openLog opens a run's output.log, or output.log.1 when file names that one ("": the current log); prev is
// output.log.1's identity when the current log is open. A file no longer kept is stale.
func openLog(dir, file string) (f *os.File, id, prev string, err error) {
	path := filepath.Join(dir, "output.log")
	f, err = os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = errNoLog
		}
		return nil, "", "", err
	}
	id, prev = fileio.IDOf(f), fileio.ID(path+".1")
	if file == "" || file == id {
		return f, id, prev, nil
	}
	f.Close()
	if file == prev {
		if f, err = os.Open(path + ".1"); err == nil {
			if id = fileio.IDOf(f); id == file { // ⚠️ by the open file's: the log may have rolled since
				return f, id, "", nil
			}
			f.Close()
		}
	}
	return nil, "", "", &wire.Error{Code: wire.CodeStale}
}

// Tail reads a page of a run's output.log backwards from p.Before.
func (n *Node) Tail(p TailParams) (Tail, error) {
	if !runID.MatchString(p.Run) {
		return Tail{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "run id"}
	}
	dir := n.runDir(p.Run)
	f, id, prev, err := openLog(dir, p.File)
	if errors.Is(err, errNoLog) {
		return Tail{Done: true}, nil
	}
	if err != nil {
		return Tail{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Tail{}, err
	}
	end := p.Before
	if end < 0 || end > fi.Size() {
		end = fi.Size()
	}
	size := int64(p.Max)
	if size <= 0 || size > 1<<20 {
		size = 64 << 10
	}
	from := max(0, end-size)
	b := make([]byte, end-from)
	if _, err := f.ReadAt(b, from); err != nil && err != io.EOF {
		return Tail{}, err
	}
	var t Tail
	if !p.Clip {
		if from > 0 { // start at a line
			if i := bytes.IndexByte(b, '\n'); i >= 0 {
				b, from = b[i+1:], from+int64(i+1)
			}
		}
		t.Text = string(b)
	} else {
		var head *TailLine
		if from > 0 { // start at a line; one too long to send whole is sent as its head
			i := bytes.IndexByte(b, '\n')
			ends := from + int64(i) + 1
			if i < 0 {
				ends = end // in the last line, still being written
			}
			start := lineStart(f, from)
			switch {
			case start == from:
			case i >= 0 && ends-start-1 > maxSentLine:
				h := make([]byte, maxSentField)
				k, _ := f.ReadAt(h, start)
				head = &TailLine{Off: start, Text: string(runeHead(h[:k], maxSentField)), Size: ends - start, Head: true}
				b, from = b[i+1:], start
			case i < 0:
				b, from = nil, start
				if end-start <= maxSentField {
					b = make([]byte, end-start)
					f.ReadAt(b, start)
				}
			default:
				b, from = b[i+1:], ends
			}
		}
		at := from
		if head != nil {
			at = head.Off + head.Size
		}
		t.Lines = linesOf(b, at)
		if head != nil {
			t.Lines = append([]TailLine{*head}, t.Lines...)
		}
	}
	t.From, t.File, t.Done, t.Turn = from, id, from == 0, turnAt(dir, id, from)
	if t.Done {
		t.Prev = prev
	}
	return t, nil
}

// lineStart is where the line that at is in starts.
func lineStart(f *os.File, at int64) int64 {
	b := make([]byte, 64<<10)
	for end := at; end > 0; {
		from := max(0, end-int64(len(b)))
		k, _ := f.ReadAt(b[:end-from], from)
		if i := bytes.LastIndexByte(b[:k], '\n'); i >= 0 {
			return from + int64(i) + 1
		}
		end = from
	}
	return 0
}

// linesOf is b, read from off, as the lines of a Clip page; a last line still being written is sent only when it is
// short.
func linesOf(b []byte, off int64) []TailLine {
	var out []TailLine
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			if len(b) <= maxSentField {
				out = append(out, TailLine{Off: off, Text: string(b)})
			}
			break
		}
		l := TailLine{Off: off}
		sent, head := clipLine(b[:i])
		switch {
		case head:
			l.Text, l.Size, l.Head = string(sent), int64(i+1), true
		case len(sent) != i:
			l.Text, l.Size = string(sent)+"\n", int64(i+1)
		default:
			l.Text = string(b[:i+1])
		}
		out = append(out, l)
		b, off = b[i+1:], off+int64(i+1)
	}
	return out
}

// Line reads one line of a run's output from where it starts, up to p.Max bytes (at most maxSentLine).
func (n *Node) Line(p LineParams) (Line, error) {
	if !runID.MatchString(p.Run) || p.Off < 0 {
		return Line{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "line"}
	}
	f, id, _, err := openLog(n.runDir(p.Run), p.File)
	if errors.Is(err, errNoLog) {
		return Line{}, &wire.Error{Code: wire.CodeNotFound, Detail: p.Run}
	}
	if err != nil {
		return Line{}, err
	}
	defer f.Close()
	limit := p.Max
	if limit <= 0 || limit > maxSentLine {
		limit = maxSentLine
	}
	if _, err := f.Seek(p.Off, io.SeekStart); err != nil {
		return Line{}, err
	}
	br := bufio.NewReaderSize(f, 64<<10)
	l := Line{File: id}
	var text []byte
	for {
		part, err := br.ReadSlice('\n')
		l.Size += int64(len(part))
		text = append(text, part[:min(len(part), limit-len(text))]...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			if err != nil && err != io.EOF {
				return Line{}, err
			}
			break
		}
	}
	l.Text = string(runeHead(text, limit))
	return l, nil
}
