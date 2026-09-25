//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const probeMode = os.O_RDWR

func lockFile(f *os.File, try bool) (func(), error) {
	h := windows.Handle(f.Fd())
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if try {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, flags, 0, 1, 0, ol); err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() { windows.UnlockFileEx(h, 0, 1, 0, ol) }, nil
}
