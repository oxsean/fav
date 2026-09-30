package node

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/wire"
)

// MRunOutputFind finds text in what a run's output shows (FindParams).
const MRunOutputFind = "run.output.find"

const (
	findLimit     = 50      // hits a page holds by default
	maxFindHits   = 200     // at most
	maxFindQ      = 1 << 10 // bytes of what is looked for
	findsAtOnce   = 2       // finds this node runs at once; more wait
	excerptRunes  = 160     // a hit's excerpt
	excerptBefore = 40      // of which before the match
)

type FindParams struct {
	Run    string `json:"run"`
	Q      string `json:"q"`
	File   string `json:"file,omitempty"` // the log to look in ("": the current one); the current one goes on into .1
	Before int64  `json:"before"`         // look before this line start; < 0: from the end
	Limit  int    `json:"limit,omitempty"`
}

type Found struct {
	Hits []Hit   `json:"hits"`           // in the log's order: the ones nearest before Before
	File string  `json:"file"`           // the log looked in first
	Next *FindAt `json:"next,omitempty"` // where earlier hits are looked for; none: no more
}

// Hit is an event whose shown text has the text looked for.
type Hit struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Ref     string `json:"ref,omitempty"` // a tool_result's call
	Turn    int    `json:"turn"`
	At      string `json:"at,omitempty"`
	Excerpt string `json:"excerpt"` // the shown text around the first match
}

type FindAt struct {
	File   string `json:"file"`
	Before int64  `json:"before"`
}

func (n *Node) findSlots() chan struct{} {
	n.findOnce.Do(func() { n.finds = make(chan struct{}, findsAtOnce) })
	return n.finds
}

// Find looks for p.Q in the text a page of the run's output shows (the events its lines are sent as): the lines before
// p.Before in p.File, then in the log before it.
func (n *Node) Find(ctx context.Context, p FindParams) (Found, error) {
	q := strings.TrimSpace(p.Q)
	if !runID.MatchString(p.Run) || q == "" || len(q) > maxFindQ {
		return Found{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "find"}
	}
	limit := p.Limit
	if limit <= 0 {
		limit = findLimit
	}
	limit = min(limit, maxFindHits)
	if err := ctx.Err(); err != nil {
		return Found{}, err
	}
	slots := n.findSlots()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return Found{}, ctx.Err()
	}
	defer func() { <-slots }()

	dir := n.runDir(p.Run)
	f, id, prev, err := openLog(dir, p.File)
	if errors.Is(err, errNoLog) {
		return Found{Hits: []Hit{}}, nil
	}
	if err != nil {
		return Found{}, err
	}
	defer f.Close()
	s := &finder{ctx: ctx, q: q, forms: formsOf(q), turns: turnsOf(dir), limit: limit}
	lines, dropped, err := s.scan(f, id, p.Before)
	if err != nil {
		return Found{}, err
	}
	out := Found{File: id}
	switch {
	case dropped:
		out.Next = &FindAt{File: id, Before: lines[0].off}
	case prev == "":
	case count(lines) >= limit:
		out.Next = &FindAt{File: id}
	default:
		if g, pid, _, err := openLog(dir, prev); err == nil {
			defer g.Close()
			s.limit = limit - count(lines)
			older, dropped, err := s.scan(g, pid, -1)
			if err != nil {
				return Found{}, err
			}
			if dropped {
				out.Next = &FindAt{File: pid, Before: older[0].off}
			}
			lines = append(older, lines...)
		}
	}
	out.Hits = []Hit{}
	for _, l := range lines {
		out.Hits = append(out.Hits, l.hits...)
	}
	return out, nil
}

type finder struct {
	ctx   context.Context
	q     string
	forms [][]byte
	turns func(file string, off int64) *output.State
	limit int
	fold  []byte
}

type hitLine struct {
	off  int64
	hits []Hit
}

func count(ls []hitLine) int {
	k := 0
	for _, l := range ls {
		k += len(l.hits)
	}
	return k
}

// scan reads the whole lines of f (file) before before and keeps the last lines whose hits reach s.limit; dropped
// tells that earlier lines had hits too.
func (s *finder) scan(f *os.File, file string, before int64) (lines []hitLine, dropped bool, err error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	end := before
	if end < 0 || end > fi.Size() {
		end = fi.Size()
	}
	br := bufio.NewReaderSize(io.NewSectionReader(f, 0, end), 64<<10)
	var line []byte
	var off, size, read int64
	for {
		part, rerr := br.ReadSlice('\n')
		size += int64(len(part))
		if len(line) <= maxSentLine {
			line = append(line, part[:min(len(part), maxSentLine+1-len(line))]...)
		}
		if errors.Is(rerr, bufio.ErrBufferFull) {
			continue
		}
		if rerr != nil && rerr != io.EOF {
			return nil, false, rerr
		}
		if rerr == io.EOF { // a line still being written shows only as a temp event
			return lines, dropped, nil
		}
		if hits := s.line(file, off, bytes.TrimSuffix(line, []byte("\n"))); len(hits) > 0 {
			lines = append(lines, hitLine{off: off, hits: hits})
			for len(lines) > 1 && count(lines)-len(lines[0].hits) >= s.limit {
				lines, dropped = lines[1:], true
			}
		}
		if read += size; read >= 1<<20 {
			if err := s.ctx.Err(); err != nil {
				return nil, false, err
			}
			read = 0
		}
		off, size, line = off+size, 0, line[:0]
	}
}

// line is the hits in the line at off (its first maxSentLine+1 bytes at most): a byte prefilter on the line, then its
// events as a page sends them, checked in their shown text.
func (s *finder) line(file string, off int64, line []byte) []Hit {
	s.fold = foldASCII(s.fold[:0], line)
	if !s.prefilter() {
		return nil
	}
	st := output.State{}
	if t := s.turns(file, off); t != nil {
		st = *t
	}
	var evs []output.Event
	if sent, head := clipLine(line); head {
		evs = []output.Event{{ID: file + ":" + strconv.FormatInt(off, 10) + ":0", Kind: output.KindRaw, Text: string(sent), Turn: max(st.Turn, 1)}}
	} else {
		evs, _, _ = output.Parse(file, off, string(sent)+"\n", st)
	}
	var hits []Hit
	for _, e := range evs {
		if e.Temp {
			continue
		}
		if ex, ok := excerpt(shown(e), s.q); ok {
			hits = append(hits, Hit{ID: e.ID, Kind: e.Kind, Ref: e.Ref, Turn: e.Turn, At: e.At, Excerpt: ex})
		}
	}
	return hits
}

func (s *finder) prefilter() bool {
	for _, form := range s.forms {
		if bytes.Contains(s.fold, form) {
			return true
		}
	}
	return false
}

// formsOf is q as a line may hold it, case folded: as it is, JSON escaped, and JSON escaped with <>& as <.
func formsOf(q string) [][]byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.Encode(q)
	html, _ := json.Marshal(q)
	var forms [][]byte
	for _, form := range [][]byte{[]byte(q), bytes.TrimSuffix(b.Bytes(), []byte("\n")), html} {
		form = foldASCII(nil, bytes.TrimSuffix(bytes.TrimPrefix(form, []byte(`"`)), []byte(`"`)))
		dup := false
		for _, f := range forms {
			dup = dup || bytes.Equal(f, form)
		}
		if !dup {
			forms = append(forms, form)
		}
	}
	return forms
}

// ⚠️ Find folds case in ASCII only, as the Web UI's does: a CJK or accented text is matched as written.
func foldASCII(dst, b []byte) []byte {
	for _, c := range b {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst
}

// shown is the text of e a viewer sees, as the Web UI's textOf takes it from a step: what was said, the title, the
// head and tail of an output, a diff's preview, a command, a question, a plan's steps, one per line.
func shown(e output.Event) string {
	title := e.Title
	if title == "" {
		title = e.Tool
	}
	parts := []string{e.Text, title, e.Output}
	for _, ed := range e.Edits {
		parts = append(parts, ed.Preview...)
	}
	var in struct {
		Command   any `json:"command"`
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
		Todos []planStep `json:"todos"`
		Plan  []planStep `json:"plan"`
		Items []planStep `json:"items"`
	}
	if len(e.Input) > 0 {
		json.Unmarshal(e.Input, &in) // a field of another shape is left out
		if c, ok := in.Command.(string); ok {
			parts = append(parts, c)
		}
		switch e.Family {
		case output.FamilyAsk:
			for _, q := range in.Questions {
				parts = append(parts, q.Question)
			}
		case output.FamilyPlan:
			steps := in.Todos
			if steps == nil {
				steps = in.Plan
			}
			if steps == nil {
				steps = in.Items
			}
			for _, p := range steps {
				parts = append(parts, p.text())
			}
		}
	}
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, "\n")
}

type planStep struct {
	Content *string `json:"content"`
	Step    *string `json:"step"`
	Text    string  `json:"text"`
}

func (p planStep) text() string {
	if p.Content != nil {
		return *p.Content
	}
	if p.Step != nil {
		return *p.Step
	}
	return p.Text
}

// excerpt is the shown text around q's first match, cut to excerptRunes with "…" where it was cut.
func excerpt(part, q string) (string, bool) {
	needle := foldASCII(nil, []byte(q))
	if i := bytes.Index(foldASCII(nil, []byte(part)), needle); i >= 0 {
		from := i
		for k := 0; k < excerptBefore && from > 0; k++ {
			_, w := utf8.DecodeLastRuneInString(part[:from])
			from -= w
		}
		to := from
		for k := 0; k < excerptRunes && to < len(part); k++ {
			_, w := utf8.DecodeRuneInString(part[to:])
			to += w
		}
		to = max(to, i+len(needle))
		ex := part[from:to]
		if from > 0 {
			ex = "…" + ex
		}
		if to < len(part) {
			ex += "…"
		}
		return ex, true
	}
	return "", false
}
