//go:build unix

package server

import (
	"net"
	"strings"
	"syscall"
	"testing"
)

// TestFreePortFdStarvation exercises the freePort error path — and Connect's
// "allocate port" branch on top of it — by temporarily clamping RLIMIT_NOFILE
// so that no new file descriptor can be opened. The soft limit is lowered and
// then raised back before the test returns, so no other test is affected.
func TestFreePortFdStarvation(t *testing.T) {
	// Force netpoller initialization before clamping: it needs a kqueue/epoll
	// descriptor of its own, and creating one under the clamp is a fatal
	// runtime error instead of a clean Listen error.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("warmup listen: %v", err)
	}
	_ = l.Close()

	// New descriptors are always allocated at the lowest free number, and
	// RLIMIT_NOFILE caps exactly that number — clamping the soft limit to the
	// lowest free descriptor makes every subsequent open fail with EMFILE.
	lowest := 0
	var st syscall.Stat_t
	for lowest < 4096 && syscall.Fstat(lowest, &st) == nil {
		lowest++
	}
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatalf("getrlimit: %v", err)
	}
	restore := lim
	lim.Cur = uint64(lowest)
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatalf("setrlimit: %v", err)
	}
	defer func() {
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &restore); err != nil {
			t.Errorf("restore rlimit: %v", err)
		}
	}()

	if _, err := freePort(); err == nil {
		t.Error("expected freePort to fail with no descriptors available")
	}
	if _, err := Connect(Options{}); err == nil || !strings.Contains(err.Error(), "allocate port") {
		t.Errorf("Connect = %v, want allocate-port error", err)
	}
}
