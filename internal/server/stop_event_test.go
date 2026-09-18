package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/watchdog"
)

// TestStopReportsStopping verifies Stop emits the "stopping" event when the
// graceful SIGINT is delivered to a well-behaved child, and no escalation.
func TestStopReportsStopping(t *testing.T) {
	bin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no 'sleep' binary")
	}
	var events []StopEvent
	c := &Conn{proc: exec.Command(bin, "30"), OnStopEvent: func(e StopEvent) {
		events = append(events, e)
	}}
	if err := c.proc.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan struct{})
	go func() { c.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
	if len(events) != 1 {
		t.Fatalf("events = %v, want exactly one StopStopping", events)
	}
	if events[0] != StopStopping {
		t.Errorf("event = %v, want %v", events[0], StopStopping)
	}
}

// fakeIgnoreINTScript builds a stand-in server that ignores SIGINT, forcing
// Stop's escalation path.
func fakeIgnoreINTScript(t *testing.T) *exec.Cmd {
	t.Helper()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	bin := filepath.Join(dir, "fake-ignore-int")
	// The trap shields only the shell — the group-signalled SIGINT still
	// kills each `sleep`, so loop forever to survive the graceful window.
	// The ready file proves the trap is installed before Stop's SIGINT;
	// without it the signal can win the race and kill the shell outright.
	script := "#!/bin/sh\ntrap '' INT\ntouch \"" + ready + "\"\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cmd := exec.Command(bin)
	// Match spawn(): own process group, so signalServer's group-signalled
	// SIGINT actually targets the fixture alone.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cmd
}

// TestStopReportsEscalation verifies Stop emits the "escalated" event when the
// child ignores SIGINT and Stop force-kills after the graceful window.
func TestStopReportsEscalation(t *testing.T) {
	if !watchdog.Supported() {
		t.Skip("no process-group signalling on this platform")
	}
	old := stopTimeout
	stopTimeout = 300 * time.Millisecond
	defer func() { stopTimeout = old }()

	var events []StopEvent
	c := &Conn{proc: fakeIgnoreINTScript(t), OnStopEvent: func(e StopEvent) {
		events = append(events, e)
	}}
	done := make(chan struct{})
	go func() { c.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
	var stopping, escalated int
	var order []StopEvent
	for _, e := range events {
		switch e {
		case StopStopping:
			stopping++
		case StopEscalated:
			escalated++
		}
		order = append(order, e)
	}
	if stopping != 1 || escalated != 1 {
		t.Fatalf("events = %v (stopping=%d escalated=%d), want one of each", events, stopping, escalated)
	}
	if len(order) != 2 || order[0] != StopStopping || order[1] != StopEscalated {
		t.Errorf("event order = %v, want [stopping escalated]", order)
	}
}
