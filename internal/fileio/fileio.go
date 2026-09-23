// Package fileio holds the file primitives every store shares: atomic replacement and resumable line scanning.
package fileio

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// WriteAtomic replaces path through a unique temp file beside it; no fsync, so a power loss may still tear it.
func WriteAtomic(path string, perm os.FileMode, fill func(io.Writer) error) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // a dotfile manager's link stays a link
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	w := bufio.NewWriter(f)
	err = fill(w)
	if err == nil {
		err = w.Flush()
	}

	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, perm)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func WriteFile(path string, data []byte, perm os.FileMode) error {
	return WriteAtomic(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// Lines feeds fn each complete line from offset from, skipping lines over max, and returns the offset after the last
// one consumed; a trailing partial line waits for the next call. ⚠️ line is only valid during fn.
func Lines(ctx context.Context, path string, from int64, max int, fn func(off int64, line []byte) bool) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return from, err
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return from, err
	}
	r := bufio.NewReaderSize(f, max)
	off := from
	for n := 1; ; n++ {
		if n%4096 == 0 && ctx.Err() != nil {
			return off, nil
		}
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			skipped := int64(len(line))
			for errors.Is(err, bufio.ErrBufferFull) {
				line, err = r.ReadSlice('\n')
				skipped += int64(len(line))
			}
			if err == nil {
				off += skipped
				continue
			}
		}
		if errors.Is(err, io.EOF) {
			return off, nil
		}
		if err != nil {
			return off, err
		}
		at := off
		off += int64(len(line))
		if !fn(at, line) {
			return off, nil
		}
	}
}
