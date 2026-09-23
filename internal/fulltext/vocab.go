package fulltext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/oxsean/fav/internal/fileio"
)

// The vocabulary counts the Latin words of the store (add-only: words of dropped transcripts linger harmlessly). A
// typed word it barely knows is corrected to known words one edit away.
const (
	vocabFile    = "vocab.json"
	fixMinLen    = 5 // shorter words have too many neighbours (test, text, next)
	fixMaxRare   = 2 // a typed word seen more often than this is taken as meant
	fixMinCommon = 5 // a correction must be seen this many times, and 5× more than the typed word
	fixMax       = 3
)

type vocab struct {
	Ver   int            `json:"ver"`
	Words map[string]int `json:"words"`
	dirty bool
}

func loadVocab(dir string) *vocab {
	v := &vocab{Ver: storeVer, Words: map[string]int{}}
	b, err := os.ReadFile(filepath.Join(dir, vocabFile))
	if err != nil {
		return v
	}
	var got vocab
	if json.Unmarshal(b, &got) != nil || got.Ver != storeVer || got.Words == nil {
		return v
	}
	return &got
}

func (v *vocab) save(dir string) error {
	if !v.dirty {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := fileio.WriteFile(filepath.Join(dir, vocabFile), b, 0o644); err != nil {
		return err
	}
	v.dirty = false
	return nil
}

// add counts the Latin words (4+ letters, lowercased) of text.
func (v *vocab) add(text string) {
	low := lowerASCII(text)
	for i := 0; i < len(low); {
		if !isWordByte(low[i]) {
			i++
			continue
		}
		j := i
		for j < len(low) && isWordByte(low[j]) {
			j++
		}
		if n := j - i; n >= 4 && n <= 40 {
			v.Words[low[i:j]]++
			v.dirty = true
		}
		i = j
	}
}

func isWordByte(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

var vocabCache struct {
	sync.Mutex
	dir   string
	stamp int64
	v     *vocab
}

// cachedVocab reloads the vocabulary only when its file changed.
func cachedVocab(dir string) *vocab {
	fi, err := os.Stat(filepath.Join(dir, vocabFile))
	if err != nil {
		return nil
	}
	vocabCache.Lock()
	defer vocabCache.Unlock()
	if stamp := fi.ModTime().UnixNano() ^ fi.Size(); vocabCache.dir != dir || vocabCache.stamp != stamp {
		vocabCache.dir, vocabCache.stamp, vocabCache.v = dir, stamp, loadVocab(dir)
	}
	return vocabCache.v
}

// Expand adds, to each plain keyword holding a Latin word the store barely knows, alternatives with that word corrected.
func Expand(dir string, q Query) Query {
	v := cachedVocab(dir)
	if v == nil || len(v.Words) == 0 {
		return q
	}
	out := q
	out.Kws = make([]Keyword, len(q.Kws))
	for i, k := range q.Kws {
		out.Kws[i] = k
		if k.Exact || len(k.Alts) > 0 {
			continue
		}
		var alts []Keyword
		for _, t := range k.Terms {
			for _, fix := range v.fixes(t) {
				a := plain(strings.Replace(k.Raw, t, fix, 1), false)
				a.Fuzzy = true
				alts = append(alts, a)
			}
		}
		if len(alts) > 0 {
			nk := Keyword{Raw: k.Raw, Alts: append([]Keyword{k}, alts...)}
			for _, a := range nk.Alts {
				nk.Terms = append(nk.Terms, a.Terms...)
			}
			out.Kws[i] = nk
		}
	}
	return out
}

// Fixes lists the corrected keywords Expand added.
func (q Query) Fixes() []string {
	var out []string
	for _, k := range q.Kws {
		for _, a := range k.Alts {
			if a.Fuzzy {
				out = append(out, a.Raw)
			}
		}
	}
	return out
}

// fixes are the known words one edit from w, most common first, when w itself is rare.
func (v *vocab) fixes(w string) []string {
	if len(w) < fixMinLen || !isLatin(w) {
		return nil
	}
	own := v.Words[w]
	if own > fixMaxRare {
		return nil
	}
	need := max(fixMinCommon, 5*own)
	var out []string
	for c, n := range v.Words {
		if n >= need && c != w && len(c) >= len(w)-1 && len(c) <= len(w)+1 && oneEdit(w, c) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if v.Words[out[i]] != v.Words[out[j]] {
			return v.Words[out[i]] > v.Words[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > fixMax {
		out = out[:fixMax]
	}
	return out
}

func isLatin(w string) bool {
	for i := 0; i < len(w); i++ {
		if !isWordByte(w[i]) {
			return false
		}
	}
	return true
}

// oneEdit: a and b differ by one insertion, deletion, substitution or swap of neighbours.
func oneEdit(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	i := 0
	for i < len(a) && a[i] == b[i] {
		i++
	}
	if len(a) == len(b) {
		if i == len(a) {
			return false // equal
		}
		if a[i+1:] == b[i+1:] {
			return true // substitution
		}
		return i+1 < len(a) && a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:] // swap
	}
	return len(b) == len(a)+1 && a[i:] == b[i+1:] // insertion
}
