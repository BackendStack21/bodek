package main

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/watchdog"
)

// TestWatchdogSubcommandDispatch re-execs the test binary through the hidden
// `__bodek-watchdog` subcommand and verifies the guard kills the target when
// its parent dies — the same chain production uses via os.Executable().
func TestWatchdogSubcommandDispatch(t *testing.T) {
	if !watchdog.Supported() {
		t.Skip("no orphan guard on this platform")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no 'sleep' binary")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}

	parent := exec.Command(sleep, "30")
	if err := parent.Start(); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	target := exec.Command(sleep, "30")
	if err := target.Start(); err != nil {
		t.Fatalf("start target: %v", err)
	}
	defer func() { _ = target.Process.Kill() }()

	guard := exec.Command(self, watchdogSubcommandName,
		strconv.Itoa(parent.Process.Pid), strconv.Itoa(target.Process.Pid))
	if err := guard.Start(); err != nil {
		t.Fatalf("start guard: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	if err := parent.Process.Kill(); err != nil {
		t.Fatalf("kill parent: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for watchdog.Alive(target.Process.Pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if watchdog.Alive(target.Process.Pid) {
		t.Fatal("target survived parent death — orphan guard failed")
	}
	// The guard itself must be gone too once its job is done.
	for watchdog.Alive(guard.Process.Pid) && time.Now().Before(deadline) {
		_, _ = guard.Process.Wait() // reap if exited
		time.Sleep(100 * time.Millisecond)
	}
	_, _ = guard.Process.Wait()
	if watchdog.Alive(guard.Process.Pid) {
		t.Error("guard still running after target termination")
	}
}

// TestWatchdogSubcommandBadArgs: malformed input exits cleanly instead of
// hanging or crashing.
func TestWatchdogSubcommandBadArgs(t *testing.T) {
	done := make(chan struct{})
	go func() {
		runWatchdog([]string{"not-a-pid"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runWatchdog hung on bad args")
	}
}
