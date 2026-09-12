//go:build windows

package server

import (
	"os"
	"os/exec"
	"syscall"
)

// watchdogArg is unused on windows: there is no process-group signalling,
// so no orphan guard is spawned.
const watchdogArg = "__bodek-watchdog"

// setPgroup is a no-op on windows.
func setPgroup(*exec.Cmd) {}

// signalServer falls back to bare-process signalling on windows (no
// process groups); Kill maps to Process.Kill, anything else to Interrupt
// as before.
func (c *Conn) signalServer(sig syscall.Signal) {
	if c.proc == nil || c.proc.Process == nil {
		return
	}
	if sig == syscall.SIGKILL {
		_ = c.proc.Process.Kill()
		return
	}
	_ = c.proc.Process.Signal(os.Interrupt)
}
