package memory

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/oxsean/fav/internal/paths"
)

// ErrOutside: the path is no file under a memory root, or does not exist; which, it does not say.
var ErrOutside = errors.New("not a memory file")

// Read is a memory file: in a Claude memory directory or under Codex's memories/.
func Read(file string) (text string, at time.Time, sha string, err error) {
	p, err := memoryFile(file)
	if err != nil {
		return "", time.Time{}, "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", time.Time{}, "", ErrOutside
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", time.Time{}, "", ErrOutside
	}
	return string(b), info.ModTime(), digest(b), nil
}

// memoryFile is file resolved through its links, when it is a regular file under a memory root.
func memoryFile(file string) (string, error) {
	if !filepath.IsAbs(file) {
		return "", ErrOutside
	}
	real, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", ErrOutside
	}
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
		return "", ErrOutside
	}
	for _, root := range []string{claudeRoot(filepath.Clean(file)), codexRoot()} {
		if root == "" {
			continue
		}
		if r, err := filepath.EvalSymlinks(root); err == nil && paths.Under(real, r) && paths.Under(filepath.Clean(file), root) {
			return real, nil
		}
	}
	return "", ErrOutside
}
