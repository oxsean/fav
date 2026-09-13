//go:build unix

package fav

import (
	"os"
	"syscall"
)

// Two sessions running /fav at once: a single write is not atomic, flock serialises them.
func lock(f *os.File) (func(), error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }, nil
}
