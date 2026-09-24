//go:build !windows

package fileio

import "os"

// LinkDir makes link a name for the directory target: a symlink.
func LinkDir(target, link string) error { return os.Symlink(target, link) }
