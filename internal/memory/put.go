package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/oxsean/fav/internal/paths"
)

// ErrStale: the memory there is not the one the caller saw.
var ErrStale = errors.New("the memory changed since it was listed")

// BadPut is a Put refused for what it was given.
type BadPut struct{ Why string }

func (e *BadPut) Error() string { return e.Why }

// Put writes one Claude memory of dir (a project directory, or a Claude memory directory itself) when what is there
// is what the caller saw, expect: "" for no file, else the sha of the one there. Write's rules apply after: never
// over another. line, when set, is the one MEMORY.md line pointing at name.
func Put(dir, name string, data []byte, line, expect string) (Written, error) {
	if !filepath.IsAbs(dir) {
		return Written{}, &BadPut{"not an absolute directory: " + dir}
	}
	dir = filepath.Clean(dir)
	mem := DirOf(dir)
	if mem != dir && !paths.IsDir(dir) {
		return Written{}, &BadPut{"no such directory: " + dir}
	}
	if line != "" {
		m := indexLine.FindStringSubmatch(line)
		if strings.ContainsAny(line, "\r\n") || m == nil || filepath.Clean(filepath.FromSlash(m[2])) != name {
			return Written{}, &BadPut{"not the index line of " + name + ": " + line}
		}
	}
	if !memoryName(name) {
		return Written{}, &BadPut{"not a memory file name: " + name}
	}
	switch old, err := os.ReadFile(filepath.Join(mem, name)); {
	case err == nil && digest(old) != expect, os.IsNotExist(err) && expect != "":
		return Written{}, ErrStale
	case err != nil && !os.IsNotExist(err):
		return Written{}, err
	}
	return Write(mem, name, data, line)
}
