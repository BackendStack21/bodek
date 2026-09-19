package server

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/watchdog"
)

// TestStopReportsStopping verifies Stop emits the "stopping" event when the
// graceful SIGINT is delivered to a well-behaved child, then "stopped" on
// its clean exit — never an escalation.
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
	if len(events) != 2 {
		t.Fatalf("events = %v, want [stopping stopped]", events)
	}
	if events[0] != StopStopping || events[1] != StopStopped {
		t.Errorf("events = %v, want [%v %v]", events, StopStopping, StopStopped)
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
	fixturePgroup(cmd)
	t.Cleanup(func() { fixtureGroupKill(cmd) })
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

// TestStopWindowsDefaults pins the shutdown timing contract: the hard
// deadline is 30s and escalation on silence waits activityGrace for
// in-flight work (a memory flush writing stderr) before giving up.
func TestStopWindowsDefaults(t *testing.T) {
	if stopTimeout != 30*time.Second {
		t.Fatalf("stopTimeout = %v, want 30s hard deadline", stopTimeout)
	}
	if activityGrace != 12*time.Second {
		t.Fatalf("activityGrace = %v, want 12s (must stay above the legacy uniform 8s so silent-but-working teardowns are never killed faster than before)", activityGrace)
	}
}

// fakeBusyINTScript builds (unstarted) a stand-in server that ignores SIGINT
// and keeps writing stderr — a proxy for a memory flush in flight. The caller
// must wire the scan writer and Start it, then waitBusyReady.
func fakeBusyINTScript(t *testing.T) *exec.Cmd {
	t.Helper()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	bin := filepath.Join(dir, "fake-busy-int")
	// The ready file proves the trap is installed before Stop's SIGINT;
	// without it the signal wins the race and kills the shell outright.
	script := "#!/bin/sh\ntrap '' INT\ntouch \"" + ready + "\"\nwhile :; do echo working… >&2; sleep 0.05; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cmd := exec.Command(bin)
	// Match spawn(): own process group, so signalServer's group-signalled
	// SIGINT actually targets the fixture alone.
	fixturePgroup(cmd)
	t.Cleanup(func() { fixtureGroupKill(cmd) })
	return cmd
}

// waitBusyReady blocks until the fixture's trap is installed and its loop is
// running, matching fakeIgnoreINTScript's readiness contract.
func waitBusyReady(t *testing.T, dir string) {
	t.Helper()
	ready := filepath.Join(dir, "ready")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// newFixtureConn wires a fixture's stderr through a tokenScanWriter the
// same way spawn does, so Stop's activity tracking sees its output.
func newFixtureConn(cmd *exec.Cmd, on func(StopEvent), prog func(StopProgress)) *Conn {
	c := &Conn{proc: cmd, scan: &tokenScanWriter{w: io.Discard}}
	c.scan.act = &c.lastAct
	cmd.Stderr = c.scan
	c.OnStopEvent = on
	c.OnStopProgress = prog
	return c
}

// TestStopIdleEscalatesEarly verifies a silent child that ignores SIGINT is
// force-killed after activityGrace — long before the 30s hard deadline.
func TestStopIdleEscalatesEarly(t *testing.T) {
	oldStop, oldGrace := stopTimeout, activityGrace
	stopTimeout, activityGrace = 10*time.Second, 300*time.Millisecond
	defer func() { stopTimeout, activityGrace = oldStop, oldGrace }()
	var events []StopEvent
	c := newFixtureConn(fakeIgnoreINTScript(t), func(e StopEvent) { events = append(events, e) }, nil)
	start := time.Now()
	done := make(chan struct{})
	go func() { c.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
	elapsed := time.Since(start)
	if elapsed >= stopTimeout/2 {
		t.Fatalf("Stop took %v; a silent child should escalate well before the deadline", elapsed)
	}
	if len(events) != 2 || events[0] != StopStopping || events[1] != StopEscalated {
		t.Fatalf("events = %v, want [stopping escalated]", events)
	}
}

// TestStopActivityDelaysEscalationToDeadline verifies a child that keeps
// writing stderr is given the full hard deadline, not killed at the idle
// grace window.
func TestStopActivityDelaysEscalationToDeadline(t *testing.T) {
	oldStop, oldGrace := stopTimeout, activityGrace
	stopTimeout, activityGrace = 800*time.Millisecond, 400*time.Millisecond
	defer func() { stopTimeout, activityGrace = oldStop, oldGrace }()
	var events []StopEvent
	cmd := fakeBusyINTScript(t)
	c := newFixtureConn(cmd, func(e StopEvent) { events = append(events, e) }, nil)
	// Stderr must be wired before Start — after it the pipe is already fixed
	// and no activity would reach the scan writer.
	cmd.Stderr = c.scan
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture: %v", err)
	}
	waitBusyReady(t, filepath.Dir(cmd.Path))
	start := time.Now()
	done := make(chan struct{})
	go func() { c.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
	elapsed := time.Since(start)
	if elapsed < stopTimeout-200*time.Millisecond {
		t.Fatalf("Stop took %v; an active child must be held until the %v deadline (stderr tail: %q)", elapsed, stopTimeout, c.scan.Tail(4))
	}
	if len(events) != 2 || events[0] != StopStopping || events[1] != StopEscalated {
		t.Fatalf("events = %v, want [stopping escalated]", events)
	}
}

// TestStopProgressEvents verifies Stop reports elapsed countdown progress
// while waiting, with Max carrying the deadline for the caller's rendering.
func TestStopProgressEvents(t *testing.T) {
	oldStop, oldGrace := stopTimeout, activityGrace
	stopTimeout, activityGrace = 2*time.Second, 1500*time.Millisecond
	defer func() { stopTimeout, activityGrace = oldStop, oldGrace }()
	var events []StopEvent
	var progress []StopProgress
	c := newFixtureConn(fakeIgnoreINTScript(t),
		func(e StopEvent) { events = append(events, e) },
		func(p StopProgress) { progress = append(progress, p) })
	done := make(chan struct{})
	go func() { c.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
	if len(progress) == 0 {
		t.Fatal("no StopProgress events; the countdown would render as silence")
	}
	for _, p := range progress {
		if p.Max != 2*time.Second {
			t.Errorf("progress Max = %v, want the 2s deadline", p.Max)
		}
		if p.Elapsed <= 0 {
			t.Errorf("progress Elapsed = %v, want > 0", p.Elapsed)
		}
	}
	last, prev := progress[0], progress[0]
	for _, p := range progress[1:] {
		last, prev = p, last
	}
	if len(progress) > 1 && last.Elapsed <= prev.Elapsed {
		t.Errorf("progress not monotonic: %v then %v", prev.Elapsed, last.Elapsed)
	}
	// Instant-exit children legitimately produce zero progress events; a
	// graceful window that never ticks is not a defect. This test pins the
	// silent-child fixture, which always survives past the first tick.
	if progress[0].Max != stopTimeout {
		t.Errorf("progress not monotonic: %v then %v", prev.Elapsed, last.Elapsed)
	}
}
