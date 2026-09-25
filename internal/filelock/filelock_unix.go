//go:build unix

package filelock

import (
	"errors"
	"os"
	"syscall"
)

const probeMode = os.O_RDONLY

func lockFile(f *os.File, try bool) (func(), error) {
	how := syscall.LOCK_EX
	if try {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }, nil
}
