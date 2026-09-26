package proc

import "golang.org/x/sys/windows"

// StartTime is when process pid started, in 100 ns units since 1601; 0 when it cannot be told.
func StartTime(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return 0
	}
	return int64(created.HighDateTime)<<32 | int64(created.LowDateTime)
}
