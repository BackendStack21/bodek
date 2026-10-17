// Package filelock serializes read-merge-write cycles on bodek's shared
// stores (~/.bodek tokens, workspace, settings) across processes. Each store
// already merges the on-disk state before replacing it, but two instances
// could still interleave between the read and the rename and drop the
// other's update. An advisory lock on a sidecar "<path>.lock" closes that
// window; the OS releases it if the holder dies.
package filelock

import (
	"os"
	"path/filepath"
	"time"
)

// wait bounds how long Lock blocks on a peer. Persistence is best-effort
// and must never hang the TUI, so after it the write proceeds unlocked.
var wait = 2 * time.Second

// tryLockFn is tryLock, swappable so tests can fake an unsupported mount.
var tryLockFn = tryLock

// retryEvery is the polling interval while a peer holds the lock.
const retryEvery = 10 * time.Millisecond

// Lock takes the exclusive lock for path and returns its release. It never
// fails: when the lock file cannot be opened or a peer holds it past wait,
// the returned release is a no-op and the caller proceeds as before.
func Lock(path string) (unlock func()) {
	if path == "" {
		return func() {}
	}
	// Two links to one store must share its lock; the very first write
	// must not run unlocked for want of the directory.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	deadline := time.Now().Add(wait)
	for {
		acquired, busy := tryLockFn(f)
		if acquired {
			return func() {
				unlockFile(f)
				_ = f.Close()
			}
		}
		// Only a peer holding the lock is worth waiting for: a filesystem
		// without locking (NFS nolocks, some SMB/FUSE) fails every try, and
		// spinning there would stall each save by the full wait.
		if !busy || time.Now().After(deadline) {
			_ = f.Close()
			return func() {}
		}
		time.Sleep(retryEvery)
	}
}
