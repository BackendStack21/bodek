//go:build windows

package server

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// watchdogArg is unused on windows: the kill-on-close Job Object from
// guardChild replaces the re-exec orphan guard.
const watchdogArg = "__bodek-watchdog"

// setPgroup starts the child in its own console process group so a
// CTRL_BREAK can target it alone (the group id is its pid). The group also
// stops console Ctrl+C from reaching odek; guardChild makes sure a bodek
// that dies on one still takes odek with it.
func setPgroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// guardChild assigns the child to a Job Object that kills it on close.
// bodek holds the only handle, so if it dies for any reason the OS closes
// the handle and terminates odek (and the children it starts after this
// point). Stop closes it once the child is gone, sweeping leftovers.
// Best effort: on any failure the child just runs unguarded, as before.
func (c *Conn) guardChild() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.proc.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	err = windows.AssignProcessToJobObject(job, h)
	_ = windows.CloseHandle(h)
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	c.watchMu.Lock()
	c.watch = func() { _ = windows.CloseHandle(job) }
	c.watchMu.Unlock()
}

// signalServer maps SIGKILL to Process.Kill. Anything else becomes a
// CTRL_BREAK to the child's group, which a Go odek receives as
// os.Interrupt — Process.Signal(os.Interrupt) is unsupported on windows
// and always fails, so every Stop waited out the grace and force-killed.
func (c *Conn) signalServer(sig syscall.Signal) {
	if c.proc == nil || c.proc.Process == nil {
		return
	}
	if sig == syscall.SIGKILL {
		_ = c.proc.Process.Kill()
		return
	}
	if windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(c.proc.Process.Pid)) == nil {
		return
	}
	_ = c.proc.Process.Signal(os.Interrupt)
}
