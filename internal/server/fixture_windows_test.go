//go:build windows

package server

import "os/exec"

// fixturePgroup is a no-op on windows: no process-group signalling
// (TestStopReportsEscalation skips via watchdog.Supported() there).
func fixturePgroup(cmd *exec.Cmd) {}

// fixtureGroupKill is a no-op on windows for the same reason.
func fixtureGroupKill(cmd *exec.Cmd) {}
