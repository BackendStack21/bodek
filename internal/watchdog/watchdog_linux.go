//go:build linux

package watchdog

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
)

// startToken returns a start-time identity for pid (0 when unavailable):
// field 22 of /proc/<pid>/stat (clock ticks since boot). A recycled PID
// reports a different value.
func startToken(pid int) int64 {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 || i+2 > len(stat) {
		return 0
	}
	fields := bytes.Fields(stat[i+2:])
	// Remainder starts at field 3 (state); starttime is field 22.
	if len(fields) < 20 {
		return 0
	}
	n, err := strconv.ParseInt(string(fields[19]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

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
