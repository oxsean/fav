package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/tend"
)

// Trash moves a Claude memory file, or a whole Claude memory directory, into tend's trash; a file's MEMORY.md line
// goes with it. Codex's memories are Codex's own and stay.
func Trash(path string) (id string, err error) {
	if !filepath.IsAbs(path) {
		return "", ErrOutside
	}
	path = filepath.Clean(path)
	root := claudeRoot(path)
	if root == "" {
		return "", ErrOutside
	}
	if paths.Same(path, root) {
		if !paths.IsDir(path) {
			return "", ErrOutside
		}
		e, err := tend.TrashMemory(tend.TrashEntry{Provider: tend.ProviderClaude, Title: path, Cwd: path}, path)
		return e.MemoryID(), err
	}
	if filepath.Dir(path) != root || filepath.Base(path) == indexName {
		return "", errors.New("only a memory in the directory itself goes to the trash: " + path)
	}
	if _, err := memoryFile(path); err != nil {
		return "", err
	}
	s, _ := Load(KindClaude, root)
	title := strings.TrimSuffix(filepath.Base(path), ".md")
	for _, it := range s.Items {
		if it.File == path {
			title = it.Title
		}
	}
	line := lineFor(root, filepath.Base(path))
	e, err := tend.TrashMemory(tend.TrashEntry{Provider: tend.ProviderClaude, Title: title, Cwd: root, Line: line}, path)
	if err != nil {
		return "", err
	}
	if line != "" {
		if err := editIndex(root, func(lines []string) []string { return remove(lines, line) }); err != nil {
			tend.RestoreMemory(e.MemoryID())
			return "", err
		}
	}
	return e.MemoryID(), nil
}

// Restore puts a trashed memory back, its MEMORY.md line at the end of the index; file is where it went.
func Restore(id string) (file string, err error) {
	e, err := tend.RestoreMemory(id)
	if err != nil {
		return "", err
	}
	if len(e.Files) == 0 {
		return "", errors.New("empty trash entry")
	}
	file = e.Files[0].From
	if e.Line != "" {
		err = editIndex(filepath.Dir(file), func(lines []string) []string { return add(lines, e.Line) })
	}
	return file, err
}

// editIndex rewrites dir's MEMORY.md, its lines without line ends; each line ends in LF after.
func editIndex(dir string, change func([]string) []string) error {
	p := filepath.Join(dir, indexName)
	var lines []string
	if b, err := os.ReadFile(p); err == nil {
		for l := range strings.SplitSeq(strings.TrimRight(string(normalize(b)), "\n"), "\n") {
			lines = append(lines, l)
		}
		if len(b) == 0 {
			lines = nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	out := change(lines)
	return fileio.WriteFile(p, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

func remove(lines []string, line string) []string {
	out := lines[:0:0]
	for _, l := range lines {
		if l != line {
			out = append(out, l)
		}
	}
	return out
}

func add(lines []string, line string) []string {
	for _, l := range lines {
		if l == line {
			return lines
		}
	}
	return append(lines, line)
}
