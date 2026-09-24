//go:build unix

package proc

import (
	"os/exec"
	"syscall"

	"github.com/charmbracelet/x/term"
)

// StartDetached starts c in its own session: it survives the terminal or ssh session that started it, and fzf's
// killing of a reload command's process group.
func StartDetached(c *exec.Cmd) error {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return c.Start()
}

// Alive: pid runs (a zombie still counts; callers that need more hold a lock).
func Alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

type tree struct{ pgid int }

func (t *Tree) start(foreground bool) error {
	attr := &syscall.SysProcAttr{Setpgid: true}
	if foreground && term.IsTerminal(0) { // the child inherits stdin: fd 0 is the terminal in both processes
		attr.Foreground, attr.Ctty = true, 0
	}
	setDeathSignal(attr)
	t.Cmd.SysProcAttr = attr
	if err := t.Cmd.Start(); err != nil {
		return err
	}
	t.pgid = t.Cmd.Process.Pid
	return nil
}

func (t *Tree) stop(hard bool) error {
	sig := syscall.SIGTERM
	if hard {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(-t.pgid, sig)
}

// KillPID ends process pid now.
func KillPID(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
