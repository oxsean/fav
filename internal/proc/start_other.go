//go:build !darwin && !linux && !windows

package proc

// StartTime cannot be told here.
func StartTime(int) int64 { return 0 }

// zombie cannot be told here.
func zombie(int) bool { return false }
