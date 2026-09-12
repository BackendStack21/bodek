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
