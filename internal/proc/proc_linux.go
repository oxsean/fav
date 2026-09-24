//go:build linux

package proc

import "syscall"

// setDeathSignal: the tree is killed when the process that started it dies.
func setDeathSignal(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
