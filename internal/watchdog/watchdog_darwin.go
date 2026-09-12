//go:build darwin

package watchdog

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// darwin's SZOMB (sys/proc.h) is not exported by x/sys.
const szomb = 5

// startToken returns a start-time identity for pid (0 when unavailable).
// kinfo's P_start distinguishes a recycled PID from the original process.
func startToken(pid int) int64 {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0
	}
	return int64(kp.Proc.P_starttime.Sec)*1_000_000 + int64(kp.Proc.P_starttime.Usec)
}

// processAlive reports whether pid is still a live process. kill -0 alone
// also succeeds on unreaped zombies, so a darwin kinfo lookup filters those:
// the watchdog must treat a dead-but-unreaped parent as gone.
func processAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		// Entry gone from the process table: dead.
		return false
	}
	return kp.Proc.P_stat != szomb
}

// signalGroup signals the target's process group when the target leads one
// (bodek spawns the server with Setpgid), falling back to the bare pid.
func signalGroup(pid int, sig syscall.Signal) {
	if err := syscall.Kill(-pid, sig); err != nil {
		_ = syscall.Kill(pid, sig)
	}
}

const (
	sigInterrupt = syscall.SIGINT
	sigKill      = syscall.SIGKILL
)

const supported = true
