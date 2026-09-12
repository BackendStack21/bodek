// Package watchdog guards against orphaned `odek serve` child processes.
//
// bodek spawns the server as a child; its own Stop path shuts it down
// gracefully, but if bodek itself is SIGKILLed (or crashes, or the terminal
// vanishes) nothing remains to reap the child. Unix offers no portable
// "die with parent" for Go children on darwin, so bodek re-execs itself as
// a tiny watchdog process that polls its parent and terminates the server
// when the parent disappears for any reason.
package watchdog

import (
	"context"
	"time"
)

// DefaultPoll is how often the watchdog checks that the parent is alive.
const DefaultPoll = 500 * time.Millisecond

// DefaultGrace bounds the graceful (SIGINT) shutdown window after parent
// death before the watchdog escalates to SIGKILL on the process group.
const DefaultGrace = 8 * time.Second

// Supported reports whether the orphan guard can run on this platform.
func Supported() bool { return supported }

// Alive reports whether pid is a live process (zombies count as dead).
// Exported for callers that need honest liveness probes.
func Alive(pid int) bool { return processAlive(pid) }

// Run blocks until parentPID dies, then terminates targetPID's process
// group: SIGINT first (graceful), SIGKILL after grace. It also returns as
// soon as the target itself dies — there is nothing left to guard — and
// when ctx is cancelled (the parent is shutting down through its own Stop
// path).
//
// The target is identified by its start time, captured on entry: if the
// original server exits and the OS recycles its PID, the replacement is
// never signalled. On platforms without process signals (windows) Run is a
// no-op.
func Run(ctx context.Context, parentPID, targetPID int, poll, grace time.Duration) {
	if !supported || parentPID <= 0 || targetPID <= 0 {
		return
	}
	if poll <= 0 {
		poll = DefaultPoll
	}
	token := startToken(targetPID)
	for processAlive(parentPID) {
		if !sameTarget(targetPID, token) {
			return // server already gone (or its PID recycled): nothing to guard
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
	}
	// Parent is gone: if the target already exited, nothing to do.
	if !sameTarget(targetPID, token) {
		return
	}
	signalGroup(targetPID, sigInterrupt)
	deadline := time.Now().Add(grace)
	for sameTarget(targetPID, token) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
	}
	if sameTarget(targetPID, token) {
		signalGroup(targetPID, sigKill)
	}
}

// sameTarget reports whether targetPID still refers to the live process
// the watchdog was started for. A start-time token distinguishes a
// recycled PID from the original; liveness (zombie-aware) is required
// either way.
func sameTarget(pid int, token int64) bool {
	if !processAlive(pid) {
		return false
	}
	cur := startToken(pid)
	if token != 0 && cur != 0 {
		return cur == token
	}
	return true
}
