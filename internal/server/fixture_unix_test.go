//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

// fixturePgroup matches spawn(): own process group, so signalServer's
// group-signalled SIGINT actually targets the fixture alone.
func fixturePgroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// fixtureGroupKill force-kills the fixture's whole process group.
func fixtureGroupKill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
