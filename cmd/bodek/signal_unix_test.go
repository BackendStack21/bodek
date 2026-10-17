//go:build unix

package main

import (
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// A quit from inside the TUI (no signal yet) must release the handler, or
// the first Ctrl+C of the shutdown countdown is swallowed.
func TestRegressSignalHandlerReleasedAfterRun(t *testing.T) {
	guard := make(chan os.Signal, 4) // keeps SIGTERM from killing the test
	signal.Notify(guard, syscall.SIGTERM)
	defer signal.Stop(guard)

	var quits atomic.Int32
	stop := setupSignalHandler(func() { quits.Add(1) })
	stop()
	stop() // idempotent
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	<-guard
	time.Sleep(50 * time.Millisecond)
	if n := quits.Load(); n != 0 {
		t.Fatalf("released handler still quit %d time(s)", n)
	}

	stop = setupSignalHandler(func() { quits.Add(1) })
	defer stop()
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	<-guard
	deadline := time.Now().Add(2 * time.Second)
	for quits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if quits.Load() != 1 {
		t.Fatal("live handler did not quit on SIGTERM")
	}
}
