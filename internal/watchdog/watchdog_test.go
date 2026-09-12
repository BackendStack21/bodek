package watchdog

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func skipWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("watchdog process-group kill is unix-only")
	}
}

func alive(t *testing.T, pid int) bool {
	t.Helper()
	return processAlive(pid)
}

// TestRunKillsTargetWhenParentDies is the core orphan-prevention contract:
// the spawned server must not outlive bodek when bodek is killed outright.
func TestRunKillsTargetWhenParentDies(t *testing.T) {
	skipWindows(t)
	target, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no 'sleep' binary")
	}
	srv := exec.Command(target, "30")
	if err := srv.Start(); err != nil {
		t.Fatalf("start server stand-in: %v", err)
	}
	defer func() { _ = srv.Process.Kill() }()

	// A short-lived stand-in for the bodek parent process.
	parent := exec.Command(target, "1")
	if err := parent.Start(); err != nil {
		t.Fatalf("start parent stand-in: %v", err)
	}

	done := make(chan struct{})
	go func() {
		Run(context.Background(), parent.Process.Pid, srv.Process.Pid, 100*time.Millisecond, time.Second)
		close(done)
	}()

	// Kill the parent abruptly (what a SIGKILLed bodek looks like).
	time.Sleep(200 * time.Millisecond)
	if err := parent.Process.Kill(); err != nil {
		t.Fatalf("kill parent: %v", err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after parent death")
	}
	deadline := time.Now().Add(5 * time.Second)
	for alive(t, srv.Process.Pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(t, srv.Process.Pid) {
		t.Fatal("target still alive after parent died — orphaned server")
	}
}

// TestRunExitsWhenTargetDiesWhileParentLives pins the self-termination
// contract: once the server is gone there is nothing to guard, so the
// watchdog must not linger for the rest of the parent's session.
func TestRunExitsWhenTargetDiesWhileParentLives(t *testing.T) {
	skipWindows(t)
	bin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no 'sleep' binary")
	}
	parent := exec.Command(bin, "30")
	if err := parent.Start(); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	defer func() { _ = parent.Process.Kill() }()
	target := exec.Command(bin, "1")
	if err := target.Start(); err != nil {
		t.Fatalf("start target: %v", err)
	}

	done := make(chan struct{})
	go func() {
		Run(context.Background(), parent.Process.Pid, target.Process.Pid, 100*time.Millisecond, time.Second)
		close(done)
	}()
	// Target dies within ~1s while the parent lives: Run must return soon
	// after, not hang until the parent exits.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run lingered after target death while parent alive")
	}
	if !alive(t, parent.Process.Pid) {
		t.Fatal("parent must be untouched")
	}
}

// TestRunLeavesTargetWhileParentLives: no kill while bodek is healthy.
func TestRunLeavesTargetWhileParentLives(t *testing.T) {
	skipWindows(t)
	bin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no 'sleep' binary")
	}
	parent := exec.Command(bin, "30")
	if err := parent.Start(); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	defer func() { _ = parent.Process.Kill() }()
	target := exec.Command(bin, "30")
	if err := target.Start(); err != nil {
		t.Fatalf("start target: %v", err)
	}
	defer func() { _ = target.Process.Kill() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, parent.Process.Pid, target.Process.Pid, 100*time.Millisecond, time.Second)
		close(done)
	}()

	time.Sleep(400 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run ignored cancellation")
	}
	if !alive(t, target.Process.Pid) {
		t.Fatal("target killed while parent alive")
	}
}

// TestRunTargetAlreadyDead exits promptly without hanging or erroring.
func TestRunTargetAlreadyDead(t *testing.T) {
	skipWindows(t)
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no 'true' binary")
	}
	parent := exec.Command(bin)
	if err := parent.Start(); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	_ = parent.Wait() // both dead already

	done := make(chan struct{})
	go func() {
		Run(context.Background(), parent.Process.Pid, parent.Process.Pid, 50*time.Millisecond, time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung on dead processes")
	}
}
