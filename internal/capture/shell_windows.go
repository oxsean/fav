package capture

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ancestors: executable names of fav's parent processes, nearest first.
func ancestors() []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	parent, name := map[uint32]uint32{}, map[uint32]string{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		parent[e.ProcessID], name[e.ProcessID] = e.ParentProcessID, windows.UTF16ToString(e.ExeFile[:])
	}
	var out []string
	pid := uint32(os.Getpid())
	for range 8 {
		pid = parent[pid]
		n, ok := name[pid]
		if !ok {
			break
		}
		out = append(out, n)
	}
	return out
}
