//go:build !windows

package server

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Regression: procAlive probed with Signal(0), which succeeds on an
// exited-but-unreaped child — the zombie still counts as alive, so a crashed
// `odek serve` burned the full ready timeout instead of failing fast.
func TestProcAliveDetectsExitedUnreapedChild(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no 'true' binary")
	}
	c := &Conn{proc: exec.Command(bin)}
	if err := c.proc.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	c.startReaper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !c.procAlive() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("exited child still reported alive (Signal(0) zombie probe)")
}

// Regression: waitSpawned's timeout error carried no server output, so a
// bind failure (port taken, config error) was undiagnosable — the stderr
// tail must be included in the timeout error.
func TestWaitSpawnedTimeoutIncludesStderrTail(t *testing.T) {
	// A port nothing listens on: every probe refuses fast.
	baseURL := "http://127.0.0.1:1"
	scan := &tokenScanWriter{w: nilDiscard{}}
	scan.Write([]byte("listen tcp: bind: address already in use\n"))

	err := waitSpawned(baseURL, scan, func() bool { return true }, 50*time.Millisecond)
	if err == nil {
		t.Fatal("waitSpawned unexpectedly succeeded against a dead port")
	}
	if !strings.Contains(err.Error(), "bind: address already in use") {
		t.Fatalf("timeout error missing stderr tail:\n%v", err)
	}
}

// nilDiscard is a zero io.Writer (io.Discard import stays out of this file).
type nilDiscard struct{}

func (nilDiscard) Write(p []byte) (int, error) { return len(p), nil }
