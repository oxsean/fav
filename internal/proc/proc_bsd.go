//go:build unix && !linux

package proc

import "syscall"

func setDeathSignal(*syscall.SysProcAttr) {}
