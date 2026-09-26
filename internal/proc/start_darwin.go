package proc

import "golang.org/x/sys/unix"

// StartTime is when process pid started, in microseconds of some fixed clock; 0 when it cannot be told.
func StartTime(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(k.Proc.P_pid) != pid {
		return 0
	}
	return k.Proc.P_starttime.Sec*1e6 + int64(k.Proc.P_starttime.Usec)
}

// zombie: pid has exited and waits to be reaped.
func zombie(pid int) bool {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && int(k.Proc.P_pid) == pid && k.Proc.P_stat == 5 // SZOMB
}
