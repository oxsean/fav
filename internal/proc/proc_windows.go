//go:build windows

package proc

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StartDetached starts c with no console, outside the job its parent runs in when that job allows it: sshd puts a
// session's processes in a job it closes when the session ends.
func StartDetached(c *exec.Cmd) error {
	flags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS)
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags | windows.CREATE_BREAKAWAY_FROM_JOB, HideWindow: true}
	err := c.Start()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) { // the job forbids breaking away
		c.SysProcAttr.CreationFlags = flags
		c.Process = nil
		err = c.Start()
	}
	return err
}

// Alive: pid runs.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

type tree struct{ job windows.Handle }

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// start puts c in a job that is killed as a whole, and killed when this process ends (no orphans). c starts suspended
// and runs only once it is in the job, so nothing it starts escapes.
func (t *Tree) start(bool) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return err
	}
	if t.Cmd.SysProcAttr == nil {
		t.Cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	t.Cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := t.Cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(t.Cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, h)
		if err == nil {
			if st, _, _ := ntResumeProcess.Call(uintptr(h)); st != 0 {
				err = fmt.Errorf("NtResumeProcess: 0x%x", st)
			}
		}
		windows.CloseHandle(h)
	}
	if err != nil {
		t.Cmd.Process.Kill()
		windows.CloseHandle(job)
		return err
	}
	t.job = job
	return nil
}

func (t *Tree) stop(bool) error { return windows.TerminateJobObject(t.job, 1) }

// KillTree ends process pid now; its job ended the rest with the supervisor that held it.
func KillTree(pid int) error { return KillPID(pid) }

// KillPID ends process pid now.
func KillPID(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

// CheckArgs refuses argv whose program resolves to a batch file that one of its arguments would break out of.
func CheckArgs(argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	exe, err := exec.LookPath(argv[0])
	if err != nil {
		return nil // starting it reports that
	}
	if batchUnsafe(exe, argv[1:]) {
		return ErrBatchArgs
	}
	return nil
}
