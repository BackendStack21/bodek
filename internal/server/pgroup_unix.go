//go:build darwin || linux

package server

import (
	"os/exec"
	"syscall"
)

// watchdogArg is the hidden subcommand bodek re-execs as to guard the
// spawned server against orphaning (see internal/watchdog).
const watchdogArg = "__bodek-watchdog"

// setPgroup puts the child in its own process group so it (and its own
// subprocesses) can be signalled as a unit.
func setPgroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalServer sends sig to the spawned server's whole process group (the
// server's own subprocesses follow it down), falling back to the leader
// when the group is gone. Safe because Stop holds a live Process handle
// for this pid — no recycled-PID window.
func (c *Conn) signalServer(sig syscall.Signal) {
	if c.proc == nil || c.proc.Process == nil {
		return
	}
	if err := syscall.Kill(-c.proc.Process.Pid, sig); err != nil {
		_ = syscall.Kill(c.proc.Process.Pid, sig)
	}
}
