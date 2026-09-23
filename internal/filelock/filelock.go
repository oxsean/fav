package filelock

import (
	"errors"
	"os"
)

var ErrLocked = errors.New("file is locked by another process")

// Lock blocks until it holds an exclusive lock on path (created 0600 if missing).
func Lock(path string) (unlock func(), err error) { return acquire(path, false) }

// TryLock takes the exclusive lock without waiting; ErrLocked when another holder has it. Creates path 0600 if missing.
func TryLock(path string) (unlock func(), err error) { return acquire(path, true) }

func acquire(path string, try bool) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	unlock, err := lockFile(f, try)
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { unlock(); f.Close() }, nil
}

// Held: someone else holds an exclusive lock on the existing file path. It never creates the file; a file that cannot
// be opened counts as not held.
func Held(path string) bool {
	f, err := os.OpenFile(path, probeMode, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	unlock, err := lockFile(f, true)
	if err != nil {
		return true
	}
	unlock()
	return false
}
