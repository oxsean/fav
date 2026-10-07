package memory

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/fileio"
)

const incomingDir = ".incoming"

// Written is what Write did: File is where the text went, under .incoming/ (and not indexed) when another one held
// the name; Lines and Bytes are MEMORY.md's after.
type Written struct {
	File     string
	Incoming bool
	Lines    int
	Bytes    int64
	Over     bool
}

// Write puts one memory into the Claude memory directory dir and adds line to its MEMORY.md, never over another:
// the same text is left as it is, a different one goes to .incoming/<name>.
func Write(dir, name string, data []byte, line string) (Written, error) {
	if name == "" || name != filepath.Base(name) || name == indexName || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".md" {
		return Written{}, errors.New("not a memory file name: " + name)
	}
	p := filepath.Join(dir, name)
	w := Written{File: p}
	switch old, err := os.ReadFile(p); {
	case err == nil && bytes.Equal(old, data):
	case err == nil:
		w.File, w.Incoming = filepath.Join(dir, incomingDir, name), true
		if err := fileio.WriteFile(w.File, data, 0o644); err != nil {
			return Written{}, err
		}
	case os.IsNotExist(err):
		if err := fileio.WriteFile(p, data, 0o644); err != nil {
			return Written{}, err
		}
	default:
		return Written{}, err
	}
	if line != "" && !w.Incoming {
		if err := editIndex(dir, func(lines []string) []string { return add(lines, line) }); err != nil {
			return Written{}, err
		}
	}
	if s, ok := Load(KindClaude, dir); ok {
		w.Lines, w.Bytes, w.Over = s.Lines, s.Bytes, s.Over
	}
	return w, nil
}

// Merged is what Merge did with each memory of the old directory; Left are files it does not copy, which keep the
// old directory; Trashed is the old directory's trash id once everything was copied.
type Merged struct {
	Copied, Same, Incoming, Left []string
	Trashed                      string
}

// Merge copies each memory of the Claude memory directory from into to (Write: never over another), then moves from
// into the trash when nothing else is left in it.
func Merge(from, to string) (Merged, error) {
	var m Merged
	if claudeRoot(filepath.Clean(from)) != filepath.Clean(from) || claudeRoot(filepath.Clean(to)) != filepath.Clean(to) {
		return m, ErrOutside
	}
	if filepath.Clean(from) == filepath.Clean(to) {
		return m, errors.New("the same memory directory")
	}
	ents, err := os.ReadDir(from)
	if err != nil {
		return m, err
	}
	for _, e := range ents {
		name := e.Name()
		if name == indexName {
			continue
		}
		if !e.Type().IsRegular() || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".md" {
			m.Left = append(m.Left, name)
			continue
		}
		data, err := os.ReadFile(filepath.Join(from, name))
		if err != nil {
			return m, err
		}
		_, statErr := os.Stat(filepath.Join(to, name))
		w, err := Write(to, name, data, lineFor(from, name))
		switch {
		case err != nil:
			return m, err
		case w.Incoming:
			m.Incoming = append(m.Incoming, name)
		case statErr == nil:
			m.Same = append(m.Same, name)
		default:
			m.Copied = append(m.Copied, name)
		}
	}
	for _, l := range [][]string{m.Copied, m.Same, m.Incoming, m.Left} {
		slices.Sort(l)
	}
	if len(m.Left) == 0 {
		if m.Trashed, err = Trash(filepath.Clean(from)); err != nil {
			return m, err
		}
	}
	return m, nil
}
