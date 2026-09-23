//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// detach puts c in its own session: fzf kills the process group of a reload command it replaces.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
