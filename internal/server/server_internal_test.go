package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestConnectURLNotReady(t *testing.T) {
	// Attaching to a URL whose server never answers must fail after the ready
	// timeout instead of hanging.
	old := readyTimeout
	readyTimeout = 250 * time.Millisecond
	defer func() { readyTimeout = old }()

	if _, err := Connect(Options{URL: "http://127.0.0.1:1"}); err == nil {
		t.Error("expected Connect to fail when the attached server is not ready")
	}
}

func TestSpawnStartError(t *testing.T) {
	// A binary that passes LookPath but cannot be exec'd (its shebang points
	// at a nonexistent interpreter) must surface the cmd.Start error.
	bin := filepath.Join(t.TempDir(), "odek-fake")
	if err := os.WriteFile(bin, []byte("#!/nonexistent-interpreter-xyz\n"), 0o755); err != nil {
		t.Fatalf("write fake odek: %v", err)
	}
	c := &Conn{}
	if err := c.spawn(Options{Bin: bin}, "127.0.0.1:0"); err == nil {
		t.Error("expected spawn to fail when the binary cannot be started")
	}
}

func TestStopKillsLingeringProcess(t *testing.T) {
	// A server that ignores SIGINT must be force-killed once stopTimeout
	// elapses. `exec` preserves the ignored-signal disposition, so the sleep
	// itself ignores SIGINT and Stop falls back to Kill.
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no 'sh' binary available")
	}
	old := stopTimeout
	stopTimeout = 200 * time.Millisecond
	defer func() { stopTimeout = old }()

	// The "ready" line guarantees the trap is installed before SIGINT is sent.
	c := &Conn{proc: exec.Command(sh, "-c", "trap '' INT; echo ready; exec sleep 30")}
	stdout, err := c.proc.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := c.proc.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := io.ReadFull(stdout, make([]byte, len("ready\n"))); err != nil {
		t.Fatalf("read readiness line: %v", err)
	}
	done := make(chan struct{})
	go func() { c.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not fall back to Kill for a lingering process")
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
	// Server is ready but enforces auth without ever issuing a token cookie →
	// the legacy fallback probe 403s and Connect fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/models" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if _, err := Connect(Options{URL: srv.URL}); err == nil {
		t.Error("expected Connect to fail when auth is enforced and no token is available")
	}
}

// fakeOdekScript writes a shell script that mimics `odek serve` startup output
// on stderr and then exits, so Connect can scan the token from it.
func fakeOdekScript(t *testing.T, stderrLines ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for _, l := range stderrLines {
		fmt.Fprintf(&b, "echo '%s' >&2\n", l)
	}
	path := filepath.Join(t.TempDir(), "odek-fake")
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("write fake odek: %v", err)
	}
	return path
}

func TestConnectSpawnTokenFromStderr(t *testing.T) {
	// A current odek serve prints its token to stderr; Connect must pick it up
	// from the "WS token:" line while passing stderr through verbatim.
	bin := fakeOdekScript(t,
		"odek serve ⚡  http://127.0.0.1:9999/?token=cafef00d",
		"  WebSocket: ws://127.0.0.1:9999/ws",
		"  WS token:  cafef00d",
	)
	var stderr bytes.Buffer
	conn, err := Connect(Options{Bin: bin, Stderr: &stderr})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if conn.Token != "cafef00d" {
		t.Errorf("Token = %q, want %q", conn.Token, "cafef00d")
	}
	conn.Stop() // wait for the child's stderr copier to finish before reading
	for _, want := range []string{"odek serve ⚡", "WebSocket:", "WS token:  cafef00d"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr passthrough missing %q, got:\n%s", want, stderr.String())
		}
	}
}

func TestConnectSpawnTokenFromBannerFallback(t *testing.T) {
	// If the "WS token:" line is absent, the ?token= query in the banner URL
	// is the fallback.
	bin := fakeOdekScript(t,
		"odek serve ⚡  http://127.0.0.1:9999/?token=beefcafe",
		"  WebSocket: ws://127.0.0.1:9999/ws",
	)
	conn, err := Connect(Options{Bin: bin, Stderr: io.Discard})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Stop()
	if conn.Token != "beefcafe" {
		t.Errorf("Token = %q, want %q", conn.Token, "beefcafe")
	}
}
