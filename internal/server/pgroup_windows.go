//go:build windows

package server

import "os/exec"

// watchdogArg is unused on windows: there is no process-group signalling,
// so no orphan guard is spawned.
const watchdogArg = "__bodek-watchdog"

// setPgroup is a no-op on windows.
func setPgroup(*exec.Cmd) {}
