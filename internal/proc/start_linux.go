package proc

import (
	"os"
	"strconv"
	"strings"
)

// zombie: pid has exited and waits to be reaped.
func zombie(pid int) bool {
	f := statFields(pid)
	return len(f) > 0 && f[0] == "Z"
}

// statFields are the fields of /proc/<pid>/stat after the command name: state first; nil when it cannot be read.
func statFields(pid int) []string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil
	}
	i := strings.LastIndexByte(string(b), ')') // ⚠️ the command name may hold spaces and parentheses
	if i < 0 {
		return nil
	}
	return strings.Fields(string(b)[i+1:])
}

// StartTime is when process pid started, in clock ticks since boot; 0 when it cannot be told.
func StartTime(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	f := statFields(pid)
	if len(f) < 20 { // starttime is field 22 of the line, the 20th after the name
		return 0
	}
	n, _ := strconv.ParseInt(f[19], 10, 64)
	return n
}
