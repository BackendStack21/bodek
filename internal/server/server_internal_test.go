package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestSpawnAndStop exercises the spawn + Stop lifecycle using a harmless
// short-lived binary in place of odek.
func TestSpawnAndStop(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no 'true' binary available")
	}
	for _, sandbox := range []bool{false, true} {
		c := &Conn{}
		if err := c.spawn(Options{Bin: bin, Sandbox: sandbox}, "127.0.0.1:0"); err != nil {
			t.Fatalf("spawn(sandbox=%v): %v", sandbox, err)
		}
		if c.proc == nil {
			t.Fatal("proc not set after spawn")
		}
		c.Stop() // process already exited or exits on signal
	}
}

func TestStopInterruptsLongProcess(t *testing.T) {
	bin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no 'sleep' binary")
	}
	c := &Conn{proc: exec.Command(bin, "30")}
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
}

func TestSpawnDefaultBinMissing(t *testing.T) {
	// Empty Bin defaults to "odek"; absent from PATH here → LookPath error.
	// Isolate PATH so the test is deterministic even when odek is installed.
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "")
	c := &Conn{}
	if err := c.spawn(Options{Bin: ""}, "127.0.0.1:0"); err == nil {
		t.Error("expected default-bin lookup to fail")
	}
}

func TestConnectSpawnNotReady(t *testing.T) {
	bin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no 'sleep' binary")
	}
	old := readyTimeout
	readyTimeout = 250 * time.Millisecond
	defer func() { readyTimeout = old }()

	// 'sleep serve --addr …' starts but never serves HTTP → waitReady fails →
	// Connect tears the process down and returns an error.
	if _, err := Connect(Options{Bin: bin}); err == nil {
		t.Error("expected Connect to fail when the server never becomes ready")
	}
}

func TestConnectFetchTokenFailure(t *testing.T) {
	// Server is ready but never issues the token cookie → Connect fails at
	// fetchToken and tears down.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if _, err := Connect(Options{URL: srv.URL}); err == nil {
		t.Error("expected Connect to fail without a token cookie")
	}
}
