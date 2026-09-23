package fulltext

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/fav"
)

// Cands turns records into candidates: every transcript of the session, else the record's own file.
func Cands(recs []*fav.Rec, bySession map[string][]string) []Cand {
	out := make([]Cand, len(recs))
	for i, r := range recs {
		ps := bySession[r.Provider+":"+r.SessionID]
		if len(ps) == 0 {
			if p := pinnedOnly(r); p != "" {
				ps = []string{p}
			} else if r.TranscriptPath != "" {
				ps = []string{r.TranscriptPath}
			}
		}
		out[i] = Cand{Paths: ps, Meta: r.Title + "\n" + r.Summary + "\n" + strings.Join(r.Tags, " ")}
	}
	return out
}

// Prefixed: a query starting with > (》 from a CJK input method) searches messages; rest is the query without it.
func Prefixed(s string) (rest string, ok bool) {
	t := strings.TrimLeft(s, " ")
	for _, p := range []string{">", "》"} {
		if after, ok0 := strings.CutPrefix(t, p); ok0 {
			return strings.TrimSpace(after), true
		}
	}
	return s, false
}

// Split separates a message query into the keywords to search and the filter tokens (project:, #tag, last:…) that pick
// the sessions to search in. History is the point, so scope covers archived and short sessions unless status: / turns:
// say otherwise.
func Split(q string) (keywords, scope string) {
	var kw, sc []string
	status, turns := false, false
	for _, tok := range tokens(q) {
		f := tok.text
		if tok.quoted {
			kw = append(kw, `"`+f+`"`)
			continue
		}
		if len(fav.Parse(f).Words) > 0 { // whatever the list query would treat as a keyword
			kw = append(kw, f)
			continue
		}
		low := strings.ToLower(f)
		status = status || strings.HasPrefix(low, "status:")
		turns = turns || strings.HasPrefix(low, "turns:")
		sc = append(sc, f)
	}
	if !status {
		sc = append(sc, "status:all")
	}
	if !turns {
		sc = append(sc, "turns:0")
	}
	return strings.Join(kw, " "), strings.Join(sc, " ")
}

// Cand is one session to search: all its transcripts (a Claude continuation chain or several Codex rollouts).
type Cand struct {
	Paths []string
	Meta  string // title, summary and tags: keywords there earn metaBonus
}

// Result is a session where every keyword occurs somewhere; Cand indexes the slice given to Search.
type Result struct {
	Cand     int
	Hits     int       // entries matching at least one keyword
	AllInOne bool      // one entry matches every keyword
	Score    float64   // best entry's BM25, plus a little for many hits
	Snippet  string    // around the first highlight of the best entry
	Path     string    // transcript of the best entry
	Off      int64     // transcript offset of the best entry's message
	At       time.Time // time of the best entry
	Latest   time.Time // time of the newest entry matching a keyword
}

const maxTerms = 64

type hit struct {
	mask  uint64  // keywords matched
	keys  int     // bits in mask
	sum   int     // term occurrences
	w     float64 // weight of who said it and when
	tf    []uint16
	n     int // entry length in bytes
	exact int // keywords present verbatim
	win   int // bytes of the shortest stretch holding every matched keyword, -1 for one keyword
	at    int64
	path  string
	off   int64
	snip  string
}

// better orders hits before BM25 is known: more keywords, then more weighted term occurrences.
func (h hit) better(o hit) bool {
	return h.keys > o.keys || h.keys == o.keys && float64(h.sum)*h.w > float64(o.sum)*o.w
}

// weight: what the user said counts most, tool commands and Claude's context recap (which repeats everything) least;
// newer counts more, halving the bonus every recencyDays.
func weight(role byte, at int64, now time.Time) float64 {
	w := 1.0
	switch role {
	case 'u':
		w = 1.2
	case 't':
		w = 0.5
	case 's', 'o':
		w = 0.3
	}
	if at > 0 {
		days := max(0, now.Sub(time.Unix(at, 0)).Hours()/24)
		w *= 1 + 0.5*math.Exp2(-days/recencyDays)
	}
	return w
}

const recencyDays = 30

const fuzzyWeight = 0.7

// topHits: the hits a session is scored by; its score is its best hit, so a bounded shortlist keeps memory flat.
const topHits = 32

type candScan struct {
	mask   uint64
	hits   int
	all    bool
	top    []hit
	latest int64
}

// slot is where h goes in the shortlist: len(top) to append, -1 when it does not make it.
func (c *candScan) slot(h hit) int {
	if len(c.top) < topHits {
		return len(c.top)
	}
	worst := 0
	for i := range c.top {
		if c.top[worst].better(c.top[i]) {
			worst = i
		}
	}
	if h.better(c.top[worst]) {
		return worst
	}
	return -1
}

func (c *candScan) add(h hit) {
	switch i := c.slot(h); {
	case i == len(c.top):
		c.top = append(c.top, h)
	case i >= 0:
		c.top[i] = h
	}
}

func (c *candScan) merge(o *candScan) {
	c.mask |= o.mask
	c.hits += o.hits
	c.all = c.all || o.all
	for _, h := range o.top {
		c.add(h)
	}
	c.latest = max(c.latest, o.latest)
}

type fileScan struct {
	cands    map[int]*candScan
	df       []int
	docs     int
	totalLen int
}

// Search scans the text files of the candidates in parallel and ranks the sessions holding every keyword: those with
// one entry matching all keywords first, then by BM25. It returns what it has when ctx ends.
func Search(ctx context.Context, dir string, cands []Cand, q string) []Result {
	base := ParseQuery(q)
	if len(base.Kws) == 0 || tooLong(base.Kws) {
		return nil
	}
	query := Expand(dir, base)
	if tooLong(query.Kws) { // the corrections would overflow the term mask: search as typed
		query = base
	}
	kws := query.Kws
	var all []string
	idx := map[string]int{}
	for _, k := range kws {
		for _, t := range k.Terms {
			if _, ok := idx[t]; !ok {
				idx[t] = len(all)
				all = append(all, t)
			}
		}
	}
	termB := make([][]byte, len(all))
	for i, t := range all {
		termB[i] = []byte(t)
	}
	kwTerms := make([][]int, len(kws))
	for i, k := range kws {
		for _, t := range k.Terms {
			if j, ok := idx[t]; ok {
				kwTerms[i] = append(kwTerms[i], j)
			}
		}
	}
	type altSpec struct {
		terms []int
		need  int
		exact bool
		fuzzy bool
		raw   []byte
	}
	kwAlts := make([][]altSpec, len(kws)) // a keyword matches when one of its alternatives does
	for i, k := range kws {
		alts := k.Alts
		if len(alts) == 0 {
			alts = []Keyword{k}
		}
		for _, a := range alts {
			sp := altSpec{need: a.need, exact: a.Exact, fuzzy: a.Fuzzy, raw: []byte(a.Raw)}
			for _, t := range a.Terms {
				sp.terms = append(sp.terms, idx[t])
			}
			kwAlts[i] = append(kwAlts[i], sp)
		}
	}

	full := uint64(1)<<len(kws) - 1
	now := time.Now()
	type job struct {
		cand int
		path string
	}
	jobs := make(chan job)
	out := make(chan fileScan)
	workers := max(1, min(runtime.NumCPU(), 8))
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			buf := make([]byte, 0, 4096)
			for j := range jobs {
				if ctx.Err() != nil {
					continue
				}
				f, err := os.Open(filepath.Join(dir, textName(j.path)))
				if err != nil {
					continue
				}
				rd := bufio.NewReaderSize(f, 64*1024)
				fs := fileScan{df: make([]int, len(all)), cands: map[int]*candScan{}}
				tfBuf := make([]uint16, len(all))
				for n := 0; ; n++ {
					if n%4096 == 0 && ctx.Err() != nil {
						break
					}
					line, err := rd.ReadSlice('\n')
					if errors.Is(err, bufio.ErrBufferFull) { // entries are capped far below the buffer: a damaged line
						for errors.Is(err, bufio.ErrBufferFull) {
							_, err = rd.ReadSlice('\n')
						}
						continue
					}
					if err != nil {
						break // EOF, or a line still being written
					}
					line = line[:len(line)-1]
					off, role, at, text, ok := fields(line)
					if !ok {
						continue
					}
					fs.docs++
					fs.totalLen += len(text)
					buf = appendLower(buf[:0], text)
					var present uint64
					for ti, t := range termB {
						if bytes.Contains(buf, t) {
							present |= 1 << ti
							fs.df[ti]++
						}
					}
					if present == 0 {
						continue
					}
					var mask, real uint64
					exact := 0
					for ki, alts := range kwAlts {
						verbatim := false
						for _, a := range alts {
							got := 0
							for _, ti := range a.terms {
								if present&(1<<ti) != 0 {
									got++
								}
							}
							if got == 0 || got < a.need || a.exact && !bytes.Contains(buf, a.raw) {
								continue
							}
							mask |= 1 << ki
							if !a.fuzzy {
								real |= 1 << ki
							}
							verbatim = verbatim || bytes.Contains(buf, a.raw)
						}
						if verbatim {
							exact++
						}
					}
					if mask == 0 || !query.Allows(role) || len(query.Neg) > 0 && query.Excluded(string(buf)) {
						continue
					}
					h := hit{mask: mask, keys: bits.OnesCount64(mask), n: len(text), w: weight(role, at, now), at: at, exact: exact}
					if mask&^real != 0 { // matched only through a spelling fix
						h.w *= fuzzyWeight
					}
					for ti, t := range termB {
						tfBuf[ti] = 0
						if present&(1<<ti) != 0 {
							c := min(bytes.Count(buf, t), math.MaxUint16)
							tfBuf[ti] = uint16(c)
							h.sum += c
						}
					}
					cs := fs.cands[j.cand]
					if cs == nil {
						cs = &candScan{}
						fs.cands[j.cand] = cs
					}
					cs.mask |= mask
					cs.hits++
					cs.all = cs.all || mask == full
					cs.latest = max(cs.latest, at)
					if cs.slot(h) >= 0 {
						h.win = window(buf, mask, kwTerms, termB, present)
						h.tf = append([]uint16(nil), tfBuf...)
						h.path, h.off, h.snip = j.path, off, snippet(kws, string(text))
						cs.add(h)
					}
				}
				f.Close()
				out <- fs
			}
		})
	}
	go func() {
		defer close(jobs)
		for ci, c := range cands {
			for _, p := range c.Paths {
				select {
				case jobs <- job{ci, p}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	go func() { wg.Wait(); close(out) }()

	df := make([]int, len(all))
	docs, totalLen := 0, 0
	byCand := map[int]*candScan{}
	for fs := range out {
		for i, v := range fs.df {
			df[i] += v
		}
		docs += fs.docs
		totalLen += fs.totalLen
		for c, cs := range fs.cands {
			if cur := byCand[c]; cur != nil {
				cur.merge(cs)
			} else {
				byCand[c] = cs
			}
		}
	}
	if docs == 0 || ctx.Err() != nil {
		return nil
	}

	avg := float64(totalLen) / float64(docs)
	idf := make([]float64, len(all))
	for ti := range idf {
		idf[ti] = math.Log(1 + (float64(docs)-float64(df[ti])+0.5)/(float64(df[ti])+0.5))
	}
	var res []Result
	for c, cs := range byCand {
		if cs.mask != full {
			continue
		}
		score, best := -1.0, hit{}
		for _, h := range cs.top {
			s := 0.0
			for ti, n := range h.tf {
				if n == 0 {
					continue
				}
				tf := float64(n)
				s += idf[ti] * tf * 2.2 / (tf + 1.2*(0.25+0.75*float64(h.n)/avg))
			}
			s *= 1 + 0.5*float64(h.exact)/float64(len(kws))
			if h.win >= 0 {
				s *= 1 + 1/(1+float64(h.win)/proxBytes)
			}
			if s *= h.w; s > score {
				score, best = s, h
			}
		}
		score *= metaBonus(kws, cands[c].Meta)
		res = append(res, Result{Cand: c, Hits: cs.hits, AllInOne: cs.all, Score: score + 0.2*math.Log1p(float64(cs.hits)),
			Snippet: best.snip, Path: best.path, Off: best.off, At: unixTime(best.at), Latest: unixTime(cs.latest)})
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].AllInOne != res[j].AllInOne {
			return res[i].AllInOne
		}
		if res[i].Score != res[j].Score {
			return res[i].Score > res[j].Score
		}
		if !res[i].At.Equal(res[j].At) {
			return res[i].At.After(res[j].At)
		}
		return res[i].Cand < res[j].Cand
	})
	return res
}

// fields splits "off\trole\tunix\ttext".
func fields(line []byte) (off int64, role byte, at int64, text []byte, ok bool) {
	f := bytes.SplitN(line, []byte{'\t'}, 4)
	if len(f) != 4 || len(f[1]) != 1 {
		return 0, 0, 0, nil, false
	}
	off, err := strconv.ParseInt(string(f[0]), 10, 64)
	if err != nil {
		return 0, 0, 0, nil, false
	}
	at, _ = strconv.ParseInt(string(f[2]), 10, 64)
	return off, f[1][0], at, f[3], true
}

func unixTime(at int64) time.Time {
	if at <= 0 {
		return time.Time{}
	}
	return time.Unix(at, 0)
}

// metaBonus: a session whose title, summary or tags hold the keywords is about them; up to ×1.3 when they hold all.
func metaBonus(kws []Keyword, meta string) float64 {
	if meta == "" {
		return 1
	}
	low, n := lowerASCII(meta), 0
	for _, k := range kws {
		if k.Match(low) {
			n++
		}
	}
	return 1 + 0.3*float64(n)/float64(len(kws))
}

// field4 is the text column of "off\trole\tunix\ttext"; nil for a malformed line.
func field4(line []byte) []byte {
	for range 3 {
		t := bytes.IndexByte(line, '\t')
		if t < 0 {
			return nil
		}
		line = line[t+1:]
	}
	return line
}

// TooLong: the query has more keywords or distinct terms than one search matches (64 each); Search returns nothing for it.
func TooLong(q string) bool { return tooLong(Keywords(q)) }

func tooLong(kws []Keyword) bool {
	if len(kws) > 64 {
		return true
	}
	seen := map[string]bool{}
	for _, k := range kws {
		for _, t := range k.Terms {
			seen[t] = true
		}
	}
	return len(seen) > maxTerms
}

// proxBytes: keywords this many bytes apart (about 20 CJK characters) earn half the full proximity bonus of ×2.
const proxBytes = 60

// window is the length of the shortest stretch of buf holding an occurrence of every keyword in mask (a keyword occurs
// where any of its terms does); -1 when mask has fewer than two keywords.
func window(buf []byte, mask uint64, kwTerms [][]int, termB [][]byte, present uint64) int {
	if bits.OnesCount64(mask) < 2 {
		return -1
	}
	type occ struct{ pos, end, kw int }
	var occs []occ
	need := 0
	for ki, ts := range kwTerms {
		if mask&(1<<ki) == 0 {
			continue
		}
		need++
		for _, ti := range ts {
			if present&(1<<ti) == 0 {
				continue
			}
			t := termB[ti]
			for from, n := 0, 0; n < 32; n++ { // a few occurrences per term are enough to find a close pair
				i := bytes.Index(buf[from:], t)
				if i < 0 {
					break
				}
				occs = append(occs, occ{from + i, from + i + len(t), ki})
				from += i + len(t)
			}
		}
	}
	sort.Slice(occs, func(i, j int) bool { return occs[i].pos < occs[j].pos })
	count := map[int]int{}
	best, have, lo := -1, 0, 0
	for hi := range occs {
		if count[occs[hi].kw]++; count[occs[hi].kw] == 1 {
			have++
		}
		for have == need {
			end := 0
			for k := lo; k <= hi; k++ {
				end = max(end, occs[k].end)
			}
			if w := end - occs[lo].pos; best < 0 || w < best {
				best = w
			}
			if count[occs[lo].kw]--; count[occs[lo].kw] == 0 {
				have--
			}
			lo++
		}
	}
	return best
}

func lineOff(line []byte) int64 {
	before, _, ok := bytes.Cut(line, []byte{'\t'})
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(string(before), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// Hit is one entry of a session matching at least one keyword.
type Hit struct {
	Path string
	Off  int64 // transcript offset of the message (a tool call carries the message it belongs to)
	Role byte  // 'u' user, 'a' assistant, 't' tool call
	At   time.Time
	Text string
}

// Hits lists the entries of these transcripts matching any keyword of q, newest first: at most limit (0 = all), plus
// the entry at pin (Path + Off) wherever it falls. total counts every match; nothing is returned once ctx ends.
func Hits(ctx context.Context, dir string, paths []string, q string, limit int, pin Hit) (hits []Hit, total int) {
	query := Expand(dir, ParseQuery(q))
	if len(query.Kws) == 0 {
		return nil, 0
	}
	var out []Hit
	var pinned *Hit
	for _, p := range paths {
		f, err := os.Open(filepath.Join(dir, textName(p)))
		if err != nil {
			continue
		}
		var ring []Hit // the newest limit of this file (text files run oldest first); next is the slot to overwrite
		next := 0
		rd := bufio.NewReaderSize(f, 64*1024)
		for n := 1; ; n++ {
			if n%4096 == 0 && ctx.Err() != nil {
				f.Close()
				return nil, 0
			}
			line, err := rd.ReadSlice('\n')
			if errors.Is(err, bufio.ErrBufferFull) {
				for errors.Is(err, bufio.ErrBufferFull) {
					_, err = rd.ReadSlice('\n')
				}
				continue
			}
			if err != nil {
				break
			}
			fl := bytes.SplitN(line[:len(line)-1], []byte{'\t'}, 4)
			if len(fl) != 4 || len(fl[1]) != 1 || !query.MatchEntry(fl[1][0], string(fl[3])) {
				continue
			}
			at, _ := strconv.ParseInt(string(fl[2]), 10, 64)
			h := Hit{Path: p, Off: lineOff(line), Role: fl[1][0], Text: string(fl[3])}
			if at > 0 {
				h.At = time.Unix(at, 0)
			}
			if h.Path == pin.Path && h.Off == pin.Off && pinned == nil {
				pinned = &h
			}
			total++
			if limit > 0 && len(ring) == limit {
				ring[next] = h
				next = (next + 1) % limit
			} else {
				ring = append(ring, h)
			}
		}
		f.Close()
		out = append(out, ring...)
	}
	if ctx.Err() != nil {
		return nil, 0
	}
	newer := func(a, b Hit) bool {
		if !a.At.Equal(b.At) {
			return a.At.After(b.At)
		}
		return a.Path == b.Path && a.Off > b.Off
	}
	sort.SliceStable(out, func(i, j int) bool { return newer(out[i], out[j]) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if pinned != nil {
		for _, h := range out {
			if h.Path == pinned.Path && h.Off == pinned.Off && h.Role == pinned.Role {
				return out, total
			}
		}
		if limit > 0 && len(out) == limit {
			out = out[:limit-1]
		}
		out = append(out, *pinned)
		sort.SliceStable(out, func(i, j int) bool { return newer(out[i], out[j]) })
	}
	return out, total
}

// Snippet cuts text around the first keyword hit.
func Snippet(q, text string) string { return snippet(Keywords(q), text) }

func appendLower(dst, src []byte) []byte {
	for _, c := range src {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst
}

const snippetRunes = 120

// snippet cuts text around its first highlight: a little context before, the rest after.
func snippet(kws []Keyword, text string) string {
	if text == "" {
		return ""
	}
	start := 0
	if sp := Spans(kws, text); len(sp) > 0 {
		start = sp[0][0]
		for back := 0; start > 0 && back < 24; back++ { // up to 24 runes of lead-in
			_, n := utf8.DecodeLastRuneInString(text[:start])
			start -= n
		}
	}
	s := text[start:]
	if utf8.RuneCountInString(s) > snippetRunes {
		s = string([]rune(s)[:snippetRunes])
	}
	s = strings.TrimSpace(s)
	if start > 0 {
		s = "…" + s
	}
	return s
}

// Cands turns records into candidates: every transcript of the session, else the records

// Sources are the transcripts to keep text for: the index's, plus the pinned hard links of favorites whose original is gone.
func Sources(indexed []string, recs []*fav.Rec) []string {
	out := indexed
	for _, r := range recs {
		if p := pinnedOnly(r); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func pinnedOnly(r *fav.Rec) string {
	if r.PinnedPath == "" {
		return ""
	}
	if _, err := os.Stat(r.PinnedPath); err != nil {
		return ""
	}
	if r.TranscriptPath != "" {
		if _, err := os.Stat(r.TranscriptPath); err == nil {
			return ""
		}
	}
	return r.PinnedPath
}
