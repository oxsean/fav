package fileio

import (
	"io"
	"os"
	"path/filepath"
	"time"
)

var rename = Rename

// Move moves from, a file or a tree, to to: a rename, else (another file system) a copy that keeps modes and file
// times, then the original removed.
func Move(from, to string) error {
	if err := rename(from, to); err == nil {
		return nil
	}
	st, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if st.IsDir() {
		err = filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(from, p)
			dst := filepath.Join(to, rel)
			if info.IsDir() {
				return os.MkdirAll(dst, info.Mode().Perm())
			}
			return copyFile(p, dst, info)
		})
	} else {
		err = copyFile(from, to, st)
	}
	if err != nil {
		return err
	}
	return os.RemoveAll(from)
}

func copyFile(from, to string, st os.FileInfo) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := WriteAtomic(to, st.Mode().Perm(), func(w io.Writer) error { _, err := io.Copy(w, in); return err }); err != nil {
		return err
	}
	return os.Chtimes(to, time.Now(), st.ModTime())
}
