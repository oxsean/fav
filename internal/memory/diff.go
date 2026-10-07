package memory

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/pathmap"
)

// Mapping is how two machines' memories of one project line up: their ends (homes and systems) and the directories
// known to be the same, each as {here, there}. Only these are rewritten when texts are compared, never the whole text.
type Mapping struct {
	Here, There pathmap.End
	Pairs       [][2]string
}

// Entry is one memory compared: a Claude memory by its file name, a Codex block by its title.
type Entry struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Here  *Item  `json:"here,omitempty"`
	There *Item  `json:"there,omitempty"`
	Loose bool   `json:"loose,omitempty"` // the same only once line endings and the mapped paths are set aside
}

// Swap is e seen from the other machine.
func (e Entry) Swap() Entry {
	e.Here, e.There = e.There, e.Here
	return e
}

// Comparison is two machines' memories item by item.
type Comparison struct {
	OnlyHere  []Entry `json:"only_here"`
	OnlyThere []Entry `json:"only_there"`
	Differ    []Entry `json:"differ"`
	Same      []Entry `json:"same"`
}

// Texts reads an item's text on one side; Diff asks only for items whose hashes differ after line endings.
type Texts func(there bool, it Item) (string, error)

// Diff compares here's sets with there's by kind and name: the raw hash, then the hash with LF line ends, then (with a
// mapping) both texts with there's mapped directories and home written as here's.
func Diff(here, there []Set, m Mapping, text Texts) Comparison {
	hs, ts := keyed(here, false), keyed(there, true)
	c := Comparison{OnlyHere: []Entry{}, OnlyThere: []Entry{}, Differ: []Entry{}, Same: []Entry{}}
	for k, h := range hs {
		t, ok := ts[k]
		switch {
		case !ok:
			c.OnlyHere = append(c.OnlyHere, h)
		case h.Here.SHA != "" && h.Here.SHA == t.There.SHA:
			c.Same = append(c.Same, both(h, t, false))
		case h.Here.Norm != "" && h.Here.Norm == t.There.Norm || sameMapped(*h.Here, *t.There, m, text):
			c.Same = append(c.Same, both(h, t, true))
		default:
			c.Differ = append(c.Differ, both(h, t, false))
		}
	}
	for k, t := range ts {
		if _, ok := hs[k]; !ok {
			c.OnlyThere = append(c.OnlyThere, t)
		}
	}
	for _, l := range []*[]Entry{&c.OnlyHere, &c.OnlyThere, &c.Differ, &c.Same} {
		slices.SortFunc(*l, func(a, b Entry) int { return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name)) })
	}
	return c
}

func both(h, t Entry, loose bool) Entry {
	h.There, h.Loose = t.There, loose
	return h
}

// keyed are one side's items by kind and name; a name that comes back on one side is told apart by its count.
func keyed(sets []Set, there bool) map[string]Entry {
	out := map[string]Entry{}
	for _, s := range sets {
		for i := range s.Items {
			it := s.Items[i]
			e := Entry{Kind: s.Kind, Name: it.Title}
			if s.Kind == KindClaude {
				e.Name = baseName(it.File)
			}
			k := e.Kind + "\x00" + e.Name
			for n := 2; ; n++ {
				if _, dup := out[k]; !dup {
					break
				}
				k = e.Kind + "\x00" + e.Name + "\x00" + strconv.Itoa(n)
			}
			if there {
				e.There = &it
			} else {
				e.Here = &it
			}
			out[k] = e
		}
	}
	return out
}

func baseName(p string) string { return p[strings.LastIndexAny(p, `/\`)+1:] }

func sameMapped(h, t Item, m Mapping, text Texts) bool {
	if text == nil || len(m.Pairs) == 0 && (m.Here.Home == "" || m.There.Home == "") {
		return false
	}
	a, err := text(false, h)
	if err != nil {
		return false
	}
	b, err := text(true, t)
	if err != nil {
		return false
	}
	return string(normalize([]byte(a))) == mapPaths(string(normalize([]byte(b))), m)
}

// mapPaths writes there's paths in text as here's: a path at a word boundary that is a mapped directory, there's
// home, or inside one, moved with pathmap.Rebase. Anything else stays as it is.
func mapPaths(text string, m Mapping) string {
	type rule struct{ from, to string }
	var rules []rule
	for _, p := range m.Pairs {
		if p[0] != "" && p[1] != "" {
			rules = append(rules, rule{p[1], p[0]})
		}
	}
	if m.Here.Home != "" && m.There.Home != "" {
		rules = append(rules, rule{m.There.Home, m.Here.Home})
	}
	slices.SortStableFunc(rules, func(a, b rule) int { return len(b.from) - len(a.from) })
	var out strings.Builder
	for i := 0; i < len(text); {
		if i > 0 && pathChar(text[i-1]) {
			out.WriteByte(text[i])
			i++
			continue
		}
		hit := false
		for _, r := range rules {
			if !strings.HasPrefix(text[i:], r.from) {
				continue
			}
			end := i + len(r.from)
			if end < len(text) && text[end] != '/' && text[end] != '\\' && pathChar(text[end]) {
				continue
			}
			for end < len(text) && !stopChar(text[end]) {
				end++
			}
			for end > i+len(r.from) && (text[end-1] == '.' || text[end-1] == ',') {
				end--
			}
			if p, ok := pathmap.Rebase(text[i:end], r.from, r.to, m.There, m.Here); ok {
				out.WriteString(p)
				i, hit = end, true
				break
			}
		}
		if !hit {
			out.WriteByte(text[i])
			i++
		}
	}
	return out.String()
}

// pathChar: c may continue a path, so a match next to it is inside a longer name.
func pathChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80 || strings.IndexByte(`/\._-~`, c) >= 0
}

// stopChar ends a path in prose: white space, quotes, brackets, punctuation that is no part of a name.
func stopChar(c byte) bool {
	return c <= ' ' || strings.IndexByte("`'\"()[]<>{},;|:*?=", c) >= 0
}

// Block is the Codex task group starting at line (1-based) of text: its lines up to the next group, LF-terminated,
// without the blank lines before the next.
func Block(text string, line int) string {
	lines := strings.Split(string(normalize([]byte(text))), "\n")
	end := blockEnd(lines, line)
	if end == 0 {
		return ""
	}
	return strings.Join(lines[line-1:end], "\n") + "\n"
}

// In is it's text within text, its file's: a Codex block is cut out of the file.
func (it Item) In(text string) string {
	if it.Line > 0 {
		return Block(text, it.Line)
	}
	return text
}

// blockEnd is where the group starting at line ends in lines (exclusive), 0 when line is not in them.
func blockEnd(lines []string, line int) int {
	if line < 1 || line > len(lines) {
		return 0
	}
	end := line
	for end < len(lines) && !strings.HasPrefix(lines[end], groupHead) {
		end++
	}
	for end > line && strings.TrimSpace(strings.TrimRight(lines[end-1], "\r")) == "" {
		end--
	}
	return end
}
