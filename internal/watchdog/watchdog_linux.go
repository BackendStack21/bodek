//go:build linux

package watchdog

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
)

// processAlive reports whether pid is still a live process. kill -0 also
// succeeds on unreaped zombies, so the /proc stat state is checked: a
// zombie is dead.
func processAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false // no /proc entry: dead
	}
	// Field 3 is the state, after "(comm)" — comm may contain spaces.
	if i := bytes.LastIndexByte(stat, ')'); i >= 0 && i+2 < len(stat) {
		return stat[i+2] != 'Z'
	}
	return true
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
