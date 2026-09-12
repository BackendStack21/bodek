//go:build unix

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/watchdog"
)

// fakeBodekScript builds a stand-in for the bodek binary: on the hidden
// `__bodek-watchdog` subcommand it records its pid and sleeps; any other
// invocation just sleeps (server stand-in duties are handled by other
// fixtures — this one only impersonates the guard host).
func fakeBodekScript(t *testing.T) (bin string, pidfile string) {
	t.Helper()
	dir := t.TempDir()
	pidfile = filepath.Join(dir, "guard.pid")
	bin = filepath.Join(dir, "fake-bodek")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"__bodek-watchdog\" ]; then\n" +
		"  echo $$ > \"" + pidfile + "\"\n" +
		"  exec sleep 60\n" +
		"fi\n" +
		"exec sleep 60\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bodek: %v", err)
	}
	return bin, pidfile
}

func pidFromFile(t *testing.T, path string) (int, bool) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

func processExists(pid int) bool {
	return watchdog.Alive(pid)
}

// TestSpawnStartsWatchdogAndStopRetiresIt: spawning a server also launches
// the orphan guard, and a graceful Stop retires the guard (it must not
// linger or fire after the orderly path ran).
func TestSpawnStartsWatchdogAndStopRetiresIt(t *testing.T) {
	if !watchdog.Supported() {
		t.Skip("no process-group signalling on this platform")
	}
	// Server stand-in: ignore spawn args, just live.
	serverBin := filepath.Join(t.TempDir(), "fake-server")
	if err := os.WriteFile(serverBin, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatalf("write fake server: %v", err)
	}

	fake, pidfile := fakeBodekScript(t)
	old := watchdogBin
	watchdogBin = func() (string, error) { return fake, nil }
	defer func() { watchdogBin = old }()

	c := &Conn{}
	if err := c.spawn(Options{Bin: serverBin}, "127.0.0.1:0"); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer c.Stop()

	var guardPID int
	deadline := time.Now().Add(5 * time.Second)
	for {
		if pid, ok := pidFromFile(t, pidfile); ok {
			guardPID = pid
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watchdog guard was never launched")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !processExists(guardPID) {
		t.Fatal("watchdog guard died immediately")
	}
	// The spawned server must lead its own process group.
	pgid, _, errno := syscall.Syscall(syscall.SYS_GETPGID, uintptr(c.proc.Process.Pid), 0, 0)
	if errno != 0 || int(pgid) != c.proc.Process.Pid {
		t.Errorf("server not in its own process group: pgid=%d pid=%d errno=%v",
			pgid, c.proc.Process.Pid, errno)
	}

	c.Stop()
	deadline = time.Now().Add(5 * time.Second)
	for processExists(guardPID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if processExists(guardPID) {
		t.Error("watchdog guard still alive after Stop")
	}
}

// TestSpawnWatchdogBestEffort: a guard host that cannot be resolved must
// not break spawning — the graceful Stop path stays authoritative.
func TestSpawnWatchdogBestEffort(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no 'sleep' binary")
	}
	old := watchdogBin
	watchdogBin = func() (string, error) { return "", os.ErrNotExist }
	defer func() { watchdogBin = old }()

	c := &Conn{}
	if err := c.spawn(Options{Bin: sleep}, "127.0.0.1:0"); err != nil {
		t.Fatalf("spawn without guard: %v", err)
	}
	c.Stop()
}
