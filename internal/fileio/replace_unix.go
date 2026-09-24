//go:build !windows

package fileio

import "os"

func replace(tmp, path string) error { return os.Rename(tmp, path) }
