//go:build unix

package filelock

import (
	"errors"
	"os"
	"syscall"
)

// tryLock reports whether the lock was taken, and if not whether a peer
// holds it (worth retrying) rather than locking being unsupported.
func tryLock(f *os.File) (acquired, busy bool) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, false
	}
	return false, errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR)
}

func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
