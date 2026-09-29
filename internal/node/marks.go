package node

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/output"
)

// marksFile is where the supervisor marks what it did and saw in the output: only to place things in the output and
// page it. ⚠️ The agent can write the run directory: who asked or answered what is the journal's, never this file's.
const marksFile = "marks.jsonl"

// Mark is a line of marks.jsonl, at where output.log was when it was written.
type Mark = output.Mark

const (
	markStart, markTurn, markHook, markExit, markRoll = "start", "turn", "hook", "exit", "roll"
	markInput, markResolved, markInterrupt            = "input", "resolved", "interrupt"
	markDiff                                          = "diff"
	phaseBegin, phaseEnd                              = "begin", "end"
)

// readMarks reads the whole marks of marks.jsonl in dir from byte from on, each with its Pos; next is where the next
// read starts.
func readMarks(dir string, from int64) (ms []Mark, next int64) {
	f, err := os.Open(filepath.Join(dir, marksFile))
	if err != nil {
		return nil, from
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, from
	}
	br := bufio.NewReaderSize(io.LimitReader(f, maxMarksRead), 64<<10)
	for next = from; ; {
		line, err := br.ReadBytes('\n')
		if err != nil { // a line still being written waits for the next read
			return ms, next
		}
		var m Mark
		if json.Unmarshal(line, &m) == nil && m.Type == "tend" && m.File != "" {
			m.Pos = next
			ms = append(ms, m)
		}
		next += int64(len(line))
	}
}

// marksIn are the marks of log file placed in [from, to), and at to itself when to is the file's end.
func marksIn(ms []Mark, file string, from, to int64, end bool) []Mark {
	var out []Mark
	for _, m := range ms {
		if m.File == file && m.Off >= from && (m.Off < to || end && m.Off == to) {
			out = append(out, m)
		}
	}
	return out
}

// turnAt is where the output's turns stand at off in the log file names, read from the marks: what output.Parse
// reading the log from its start would hold there. nil when the marks never saw that file.
func turnAt(dir, file string, off int64) *output.State { return turnsOf(dir)(file, off) }

// turnsOf is turnAt for many places, the marks read once.
func turnsOf(dir string) func(file string, off int64) *output.State {
	ms, _ := linesFrom(filepath.Join(dir, marksFile), 0, func(m Mark) bool { return m.File != "" })
	return func(file string, off int64) *output.State { return turnIn(ms, file, off) }
}

func turnIn(ms []Mark, file string, off int64) *output.State {
	var st output.State
	seen := false
	for _, m := range ms {
		if m.File == file {
			seen = true
			if m.Event == markTurn && (m.Phase == "" && m.Off >= off || m.Phase == phaseEnd && m.Off > off) {
				break
			}
		} else if seen { // a later file
			break
		}
		if m.Event == markTurn {
			st = output.State{Turn: m.N, Closed: m.Phase == phaseEnd}
		}
	}
	if !seen {
		return nil
	}
	return &st
}

// turns follows the log's turns as output.Parse counts them, a line at a time as the log is written, and marks
// where each begins and ends.
type turns struct {
	st    output.State
	doing func(title string) // told the tool call each turn is at, "" once it ends
	line  []byte             // the line so far, unless it is past what is sent whole (over)
	over  bool
	at    int64 // where it starts
	n     int64 // its length so far
}

// took reads p, written at off; mark gets what to add.
func (t *turns) took(file string, off int64, p []byte, mark func(Mark)) {
	for len(p) > 0 {
		if t.n == 0 {
			t.at, t.line, t.over = off, t.line[:0], false
		}
		part := p
		i := bytes.IndexByte(p, '\n')
		if i >= 0 {
			part = p[:i+1]
		}
		t.n += int64(len(part))
		if t.over = t.over || t.n > maxSentLine+1; !t.over {
			t.line = append(t.line, part...)
		}
		off, p = off+int64(len(part)), p[len(part):]
		if i < 0 {
			return
		}
		t.n = 0
		if t.over {
			continue
		}
		if sent, head := clipLine(bytes.TrimSuffix(t.line, []byte("\n"))); !head {
			evs, st, _ := output.Parse(file, t.at, string(sent)+"\n", t.st)
			t.did(evs)
			if st.Turn != t.st.Turn {
				mark(Mark{Event: markTurn, N: st.Turn, Off: t.at})
			}
			if st.Closed && (!t.st.Closed || st.Turn != t.st.Turn) {
				mark(Mark{Event: markTurn, N: st.Turn, Phase: phaseEnd, Off: off})
			}
			t.st = st
		}
	}
}

// did tells doing what evs say the turn is at.
func (t *turns) did(evs []output.Event) {
	if t.doing == nil {
		return
	}
	for _, e := range evs {
		switch e.Kind {
		case output.KindTool, output.KindCmd, output.KindEdit, output.KindMCP:
			if e.Title != "" {
				t.doing(e.Title)
			}
		case output.KindResult:
			t.doing("")
		}
	}
}

// restart forgets a line that began in the file before.
func (t *turns) restart() { t.n = 0 }

// clipLine is line (without its newline) as it is sent: as it is up to maxSentField; a longer JSON line up to
// maxSentLine keeps its shape, each string over maxSentField cut to it and ended with "…"; anything else is sent as
// only its first maxSentField bytes (head).
func clipLine(line []byte) (sent []byte, head bool) {
	if len(line) <= maxSentField {
		return line, false
	}
	if len(line) <= maxSentLine && line[0] == '{' && json.Valid(line) {
		d := json.NewDecoder(bytes.NewReader(line))
		d.UseNumber()
		var v any
		if d.Decode(&v) == nil {
			var b bytes.Buffer
			e := json.NewEncoder(&b)
			e.SetEscapeHTML(false)
			if e.Encode(cutStrings(v)) == nil {
				return bytes.TrimSuffix(b.Bytes(), []byte("\n")), false
			}
		}
	}
	return runeHead(line, maxSentField), true
}

func cutStrings(v any) any {
	switch x := v.(type) {
	case string:
		if len(x) > maxSentField {
			return string(runeHead([]byte(x), maxSentField)) + "…"
		}
	case map[string]any:
		for k, e := range x {
			x[k] = cutStrings(e)
		}
	case []any:
		for i, e := range x {
			x[i] = cutStrings(e)
		}
	}
	return v
}

// runeHead is b's first n bytes at most, not splitting a character.
func runeHead(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	return b[:n]
}
