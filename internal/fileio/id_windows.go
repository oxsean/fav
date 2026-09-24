package fileio

import (
	"strconv"
	"syscall"
)

// ID tells path's file from one later written in its place (a rename over it): volume and file index; "" when
// unreadable. ⚠️ Not the creation time: a file renamed over another inherits its creation time (tunneling).
func ID(path string) string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	h, err := syscall.CreateFile(p, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	var d syscall.ByHandleFileInformation
	if syscall.GetFileInformationByHandle(h, &d) != nil {
		return ""
	}
	return strconv.FormatUint(uint64(d.VolumeSerialNumber), 16) + ":" + strconv.FormatUint(uint64(d.FileIndexHigh)<<32|uint64(d.FileIndexLow), 16)
}
