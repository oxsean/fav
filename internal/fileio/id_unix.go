//go:build !windows

package fileio

import (
	"io/fs"
	"os"
	"strconv"
	"syscall"
)

// ID tells path's file from one later written in its place (a rename over it): device and inode; "" when unreadable.
func ID(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return idOf(fi)
}

// IDOf is ID of the file f has open, whatever its path names by now.
func IDOf(f *os.File) string {
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	return idOf(fi)
}

func idOf(fi fs.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(st.Dev), 16) + ":" + strconv.FormatUint(uint64(st.Ino), 16)
	}
	return ""
}
