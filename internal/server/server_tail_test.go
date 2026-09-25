package server

import (
	"io"
	"testing"
)

// Regression: appendTail never merged the buffered partial line (s.buf) into
// the incoming chunk, so a stderr line straddling two pipe writes lost its
// head half — the tail recorded "ror: boom" instead of "error: boom".
func TestTailSplitsAcrossWritesStayWhole(t *testing.T) {
	s := &tokenScanWriter{w: io.Discard, tok: "found", tail: []string{}}
	s.Write([]byte("er")) // no newline: buffered
	s.Write([]byte("ror: bind: address already in use\n"))
	got := s.Tail(4)
	if got != "error: bind: address already in use" {
		t.Fatalf("split line not reassembled whole in tail: %q", got)
	}
}
