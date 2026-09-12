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

// Alive reports whether pid is a live process (zombies count as dead).
// Exported for callers that need honest liveness probes.
func Alive(pid int) bool { return processAlive(pid) }

// Supported reports whether the orphan guard can run on this platform.
func Supported() bool { return supported }

// Run blocks until parentPID dies, then terminates targetPID's process
// group: SIGINT first (graceful), SIGKILL after grace. It returns early
// when ctx is cancelled (the parent is shutting down through its own Stop
// path) or when the target is already gone. On platforms without process
// signals (windows) it is a no-op.
func Run(ctx context.Context, parentPID, targetPID int, poll, grace time.Duration) {
	if !supported || parentPID <= 0 || targetPID <= 0 {
		return
	}
	if poll <= 0 {
		poll = DefaultPoll
	}
	for processAlive(parentPID) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
	}
	// Parent is gone: if the target already exited, nothing to do.
	if !processAlive(targetPID) {
		return
	}
	signalGroup(targetPID, sigInterrupt)
	deadline := time.Now().Add(grace)
	for processAlive(targetPID) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
	}
	if processAlive(targetPID) {
		signalGroup(targetPID, sigKill)
	}
}
