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

// processAlive reports whether pid is still a live process. It fails
// SAFE (alive) when observation is degraded: EPERM from kill means the
// process exists but is not ours to probe; an unreadable /proc entry
// (hidepid) is likewise treated as alive — a false-alive at worst delays
// the guard, a false-dead kills a healthy server. Only a definitive
// no-such-process or zombie state counts as dead.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == syscall.EPERM {
		return true // exists, but not ours to probe
	}
	if err != nil {
		return false // ESRCH: no such process
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true // unreadable ≠ dead (hidepid): fail safe
	}
	// Field 3 is the state, after "(comm)" — comm may contain spaces.
	if i := bytes.LastIndexByte(stat, ')'); i >= 0 && i+2 < len(stat) {
		return stat[i+2] != 'Z'
	}
	return true
}

// signalGroup signals the target's process group when the target leads one
// (bodek spawns the server with Setpgid), falling back to the bare pid
// when it does not (ESRCH on the group). Safe because every caller has
// just verified the pid's identity — a recycled PID never reaches here.
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
