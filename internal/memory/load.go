package memory

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Claude loads the first 200 lines or 25 KB of MEMORY.md, whichever comes first.
const (
	loadLines = 200
	loadBytes = 25 << 10
)

// indexLine is a MEMORY.md entry: "- [title](file.md) — description" (any dash).
var indexLine = regexp.MustCompile(`^\s*[-*]\s*\[([^\]]+)\]\(([^)\s]+)\)\s*(?:[—–-]+\s*(.*))?$`)

type indexed struct{ title, description, line string }

// Load reads the memory directory dir; ok is false when it does not exist.
func Load(kind, dir string) (Set, bool) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return Set{}, false
	}
	s := Set{Kind: kind, Dir: dir}
	lines := map[string]indexed{}
	if b, err := os.ReadFile(filepath.Join(dir, indexName)); err == nil {
		s.Index = filepath.Join(dir, indexName)
		s.Lines, s.Bytes = countLines(b), int64(len(b))
		s.Over = kind == KindClaude && (s.Lines > loadLines || s.Bytes > loadBytes)
		for _, l := range strings.Split(string(b), "\n") {
			if m := indexLine.FindStringSubmatch(strings.TrimRight(l, "\r")); m != nil {
				lines[filepath.Clean(filepath.FromSlash(m[2]))] = indexed{m[1], strings.TrimSpace(m[3]), strings.TrimRight(l, "\r")}
			}
		}
	}
	s.Items = items(dir, ents, lines)
	if kind == KindClaude {
		if ents, err := os.ReadDir(filepath.Join(dir, incomingDir)); err == nil {
			s.Incoming = items(filepath.Join(dir, incomingDir), ents, nil)
		}
	}
	return s, true
}

// items are the memory files among dir's ents, the newest first, described by their front matter and lines.
func items(dir string, ents []os.DirEntry, lines map[string]indexed) []Item {
	out := []Item{}
	for _, e := range ents {
		name := e.Name()
		if !e.Type().IsRegular() || name == indexName || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".md" {
			continue
		}
		p := filepath.Join(dir, name)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		it := Item{File: p, At: info.ModTime(), Size: info.Size(), SHA: digest(b), Norm: digest(normalize(b))}
		it.Title, it.Description = frontMatter(b)
		if l, ok := lines[name]; ok {
			it.InIndex = true
			if it.Title == "" {
				it.Title = l.title
			}
			if it.Description == "" {
				it.Description = l.description
			}
		}
		if it.Title == "" {
			it.Title = strings.TrimSuffix(name, ".md")
		}
		out = append(out, it)
	}
	slices.SortFunc(out, func(a, b Item) int { return b.At.Compare(a.At) })
	return out
}

// lineFor is the MEMORY.md line pointing at name in dir, "" when there is none.
func lineFor(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, indexName))
	if err != nil {
		return ""
	}
	return IndexLine(string(b), name)
}

// IndexLine is the line of the MEMORY.md text pointing at name, "" when there is none.
func IndexLine(text, name string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, "\r")
		if m := indexLine.FindStringSubmatch(l); m != nil && filepath.Clean(filepath.FromSlash(m[2])) == name {
			return l
		}
	}
	return ""
}

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte("\n"))
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func normalize(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// frontMatter is the name and description of a leading "---" block.
func frontMatter(b []byte) (name, description string) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return "", ""
	}
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(l) == "---" {
			return name, description
		}
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "name":
			name = unquote(v)
		case "description":
			description = unquote(v)
		}
	}
	return "", ""
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	for _, q := range []string{`"`, "'"} {
		if len(v) >= 2 && strings.HasPrefix(v, q) && strings.HasSuffix(v, q) {
			return v[1 : len(v)-1]
		}
	}
	return v
}
