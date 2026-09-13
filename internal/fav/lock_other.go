//go:build !unix

package fav

import "os"

// No lock off unix: concurrent appends rely on O_APPEND alone; switch to LockFileEx if that ever matters.
func lock(*os.File) (func(), error) { return func() {}, nil }
