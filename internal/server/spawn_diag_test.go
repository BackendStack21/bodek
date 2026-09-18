package server

import (
	"strings"
	"testing"
	"time"
)

// TestWaitSpawnedErrorHasNoNilVerb guards the failure-card text: a dead
// server with an empty (or absent) stderr tail must not render Go's
// "%!w(<nil>)" artifact into the user-facing error.
func TestWaitSpawnedErrorHasNoNilVerb(t *testing.T) {
	err := waitSpawned("http://127.0.0.1:1", nil, func() bool { return false }, 300*time.Millisecond)
	if err == nil {
		t.Fatal("waitSpawned: want error for a dead server, got nil")
	}
	if strings.Contains(err.Error(), "%!") {
		t.Fatalf("waitSpawned error contains a bad verb: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "exited before becoming ready") {
		t.Fatalf("waitSpawned error lost its reason: %q", err.Error())
	}

	err = waitSpawned("http://127.0.0.1:1", nil, func() bool { return true }, 250*time.Millisecond)
	if err == nil || strings.Contains(err.Error(), "%!") {
		t.Fatalf("timeout path rendered a bad verb or no error: %v", err)
	}
}

// TestTokenScanTailKeepsFollowingServerOutput guards the diagnostics tail:
// a server that prints its startup banner (with the token) and THEN fails
// must surface the late failure lines in the tail, not the banner itself.
func TestTokenScanTailKeepsFollowingServerOutput(t *testing.T) {
	s := &tokenScanWriter{w: &strings.Builder{}}
	s.scan([]byte("odek serve ⚡  http://127.0.0.1:8080/?token=abc\n"))
	s.scan([]byte("  WS token:  abc\n"))
	if s.Token() == "" {
		t.Fatal("scan missed the token line")
	}
	s.scan([]byte("config error: invalid provider\n"))
	s.scan([]byte("FATAL: cannot start\n"))

	tail := s.Tail(maxTailLines)
	if !strings.Contains(tail, "FATAL: cannot start") {
		t.Fatalf("tail froze at the banner; late failure line missing: %q", tail)
	}
}
