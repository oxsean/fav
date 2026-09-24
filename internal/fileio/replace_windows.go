package fileio

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// replace renames tmp over path. ⚠️ Windows refuses while another process has path open without FILE_SHARE_DELETE
// (Go's own readers do): such a reader is brief, so the rename is tried again for up to about 2 s.
func replace(tmp, path string) error {
	const sharingViolation = syscall.Errno(32)
	wait := 5 * time.Millisecond
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Rename(tmp, path)
		if err == nil || !(errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, sharingViolation)) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(wait)
		wait = min(wait*2, 100*time.Millisecond)
	}
}
