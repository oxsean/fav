package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"sync"
)

// Build names the Web UI's files, hello's build: the first 12 hex digits of a SHA-256 over every embedded file's path
// and content. A page whose connection comes back to another build loads the new files.
var Build = sync.OnceValue(func() string { return buildOf(webFiles) })

func buildOf(files fs.FS) string {
	h := sha256.New()
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
