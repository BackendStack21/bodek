//go:build unix

package tui

import (
	"time"

	"golang.org/x/sys/unix"
)

// waitReadable blocks until the descriptor has input or budget elapses,
// reporting whether input arrived. poll(2) keeps unread bytes in the kernel
// buffer, which is the whole point of the synchronous read path: kqueue/epoll
// wakeups stay coherent because nothing is ever pre-drained.
func (r *inputReassembler) waitReadable(budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		ms := int(time.Until(deadline).Milliseconds())
		n, err := unix.Poll([]unix.PollFd{{Fd: int32(r.file.Fd()), Events: unix.POLLIN}}, ms)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			// An un-pollable descriptor: proceed to the read itself
			// rather than stranding the caller.
			return true
		}
		return n > 0
	}
}
