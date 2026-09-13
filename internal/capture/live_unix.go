//go:build !windows

package capture

import (
	"os"
	"syscall"
)

func lockHeld(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

func alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }
