// Package fulltext searches the prose of every transcript: a per-transcript text store kept current by the index refresh,
// scanned per query. CJK text is matched by overlapping bigrams and Latin text by words, so there is no dictionary and
// word order inside a keyword does not matter.
package fulltext

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Keyword is one space-separated part of a query: it matches a text holding at least `need` of its terms, or, quoted,
// the text as typed; with Alts (a|b, or a spelling fix) it matches when one of them does.
type Keyword struct {
	Raw   string   // lowercased as typed, without quotes
	Terms []string // CJK bigrams and Latin words, lowercased; with Alts, all of theirs
	Exact bool     // quoted: Raw must occur verbatim
	Fuzzy bool     // a spelling fix of what was typed: its hits count less
	Alts  []Keyword
	need  int
}

// Query is a message search: every keyword must occur in the session; an entry holding a Neg keyword, or said by a
// role outside Roles, does not count.
type Query struct {
	Kws   []Keyword
	Neg   []Keyword
	Roles string // entry roles allowed ("" = all): u user, a assistant, s context recap, t tool call, o tool output
}

var whoRoles = map[string]string{"me": "u", "user": "u", "ai": "as", "assistant": "as", "tool": "to"}

// ParseQuery reads the keywords of a message search: whitespace separates them; a quoted part ("…", “…” or 「…」,
// spaces allowed) must occur verbatim; a|b matches either; -x excludes messages holding x; who:me, who:ai, who:tool
// keep one speaker. A part with no usable term (a lone Latin letter) matches as a plain substring.
func ParseQuery(q string) Query {
	var out Query
	for _, tok := range tokens(lowerASCII(q)) {
		t := tok.text
		switch {
		case tok.quoted:
			out.Kws = append(out.Kws, keyword(t, true))
		case strings.HasPrefix(t, "who:") && whoRoles[t[4:]] != "":
			out.Roles += whoRoles[t[4:]]
		case len(t) > 1 && t[0] == '-':
			out.Neg = append(out.Neg, keyword(t[1:], false))
		default:
			out.Kws = append(out.Kws, keyword(t, false))
		}
	}
	return out
}

// Keywords are the positive keywords of q: what a hit must hold and what gets highlighted.
func Keywords(q string) []Keyword { return ParseQuery(q).Kws }

func keyword(t string, quoted bool) Keyword {
	if !quoted {
		var alts []Keyword
		for p := range strings.SplitSeq(t, "|") {
			if p != "" {
				alts = append(alts, plain(p, false))
			}
		}
		switch len(alts) {
		case 0:
			return plain(t, false)
		case 1:
			return alts[0]
		}
		k := Keyword{Raw: t, Alts: alts}
		for _, a := range alts {
			k.Terms = append(k.Terms, a.Terms...)
		}
		return k
	}
	return plain(t, true)
}

func plain(t string, quoted bool) Keyword {
	k := Keyword{Raw: t, Terms: terms(t), Exact: quoted}
	if len(k.Terms) == 0 {
		k.Terms = []string{t}
	}
	k.need = max(1, int(math.Ceil(float64(len(k.Terms))*0.6)))
	if k.Exact {
		k.need = len(k.Terms)
	}
	return k
}

// Allows: an entry of this role counts.
func (q Query) Allows(role byte) bool { return q.Roles == "" || strings.IndexByte(q.Roles, role) >= 0 }

// Excluded: low (lowercased with lowerASCII) holds a negative keyword.
func (q Query) Excluded(low string) bool {
	for _, k := range q.Neg {
		if k.Match(low) {
			return true
		}
	}
	return false
}

// MatchEntry: an entry of this role holds some keyword and no negative one. Used inside one session, where each
// message needs only one keyword.
func (q Query) MatchEntry(role byte, text string) bool {
	if !q.Allows(role) {
		return false
	}
	low := lowerASCII(text)
	for _, k := range q.Kws {
		if k.Match(low) {
			return !q.Excluded(low)
		}
	}
	return false
}

type token struct {
	text   string
	quoted bool
}

var closeQuote = map[rune]rune{'"': '"', '“': '”', '「': '」'}

// tokens splits on whitespace, keeping a quoted run as one token; an unclosed quote runs to the end (still being typed).
func tokens(q string) []token {
	var out []token
	rs := []rune(q)
	for i := 0; i < len(rs); {
		switch c, ok := closeQuote[rs[i]]; {
		case unicode.IsSpace(rs[i]):
			i++
		case ok:
			j := i + 1
			for j < len(rs) && rs[j] != c {
				j++
			}
			if t := strings.TrimSpace(string(rs[i+1 : j])); t != "" {
				out = append(out, token{t, true})
			}
			i = j + 1
		default:
			j := i
			for j < len(rs) && !unicode.IsSpace(rs[j]) {
				j++
			}
			out = append(out, token{string(rs[i:j]), false})
			i = j
		}
	}
	return out
}

// Match: at least 60% of the keyword's terms occur in text (already lowercased with lowerASCII); a quoted one verbatim.
func (k Keyword) Match(low string) bool {
	for _, a := range k.Alts {
		if a.Match(low) {
			return true
		}
	}
	if len(k.Alts) > 0 {
		return false
	}
	if k.Exact {
		return strings.Contains(low, k.Raw)
	}
	got := 0
	for i, t := range k.Terms {
		if strings.Contains(low, t) {
			got++
			if got >= k.need {
				return true
			}
		}
		if got+len(k.Terms)-i-1 < k.need {
			return false
		}
	}
	return false
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

func isWord(r rune) bool {
	return r < utf8.RuneSelf && (r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r))
}

// terms: each CJK run becomes its overlapping bigrams (a single character stays one term), each Latin/digit run of two or
// more characters one word; everything else separates.
func terms(s string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(t string) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); {
		switch {
		case isCJK(rs[i]):
			j := i
			for j < len(rs) && isCJK(rs[j]) {
				j++
			}
			if j-i == 1 {
				add(string(rs[i]))
			}
			for k := i; k+1 < j; k++ {
				add(string(rs[k : k+2]))
			}
			i = j
		case isWord(rs[i]):
			j := i
			for j < len(rs) && isWord(rs[j]) {
				j++
			}
			if j-i >= 2 {
				add(string(rs[i:j]))
			}
			i = j
		default:
			i++
		}
	}
	return out
}

// lowerASCII folds A–Z only, so byte offsets in the result equal those in s (strings.ToLower can change lengths).
func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; 'A' <= c && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if 'A' <= b[j] && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// Spans are the byte ranges of text to highlight for these keywords: every occurrence of every term and of the raw keyword,
// overlapping or touching ranges merged, in order.
func Spans(kws []Keyword, text string) [][2]int {
	low := lowerASCII(text)
	var sp [][2]int
	for _, k := range flatten(kws) {
		ts := append([]string{k.Raw}, k.Terms...)
		if k.Exact {
			ts = ts[:1]
		}
		for _, t := range ts {
			for from := 0; ; {
				i := strings.Index(low[from:], t)
				if i < 0 {
					break
				}
				sp = append(sp, [2]int{from + i, from + i + len(t)})
				from += i + len(t)
			}
		}
	}
	if len(sp) == 0 {
		return nil
	}
	sortSpans(sp)
	out := sp[:1]
	for _, s := range sp[1:] {
		last := &out[len(out)-1]
		if s[0] <= last[1] {
			last[1] = max(last[1], s[1])
			continue
		}
		out = append(out, s)
	}
	return out
}

func sortSpans(sp [][2]int) {
	sort.Slice(sp, func(i, j int) bool { return sp[i][0] < sp[j][0] || sp[i][0] == sp[j][0] && sp[i][1] > sp[j][1] })
}

// flatten replaces keywords having alternatives with the alternatives.
func flatten(kws []Keyword) []Keyword {
	var out []Keyword
	for _, k := range kws {
		if len(k.Alts) > 0 {
			out = append(out, flatten(k.Alts)...)
		} else {
			out = append(out, k)
		}
	}
	return out
}
