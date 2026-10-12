package server

import (
	"errors"
	"strings"
	"testing"
)

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// A failing log sink must not surface to os/exec, whose copy goroutine
// would close the stderr pipe and kill odek with SIGPIPE.
func TestTokenScanWriterSwallowsSinkErrors(t *testing.T) {
	s := &tokenScanWriter{w: failWriter{}}
	n, err := s.Write([]byte("WS token:  abc\n"))
	if err != nil || n != 15 {
		t.Fatalf("Write = %d, %v; want 15, nil", n, err)
	}
	if s.Token() != "abc" {
		t.Fatalf("token = %q, want abc", s.Token())
	}
}

// Newline-free output (\r progress bars) must not grow the buffer unbounded,
// before or after the token is found.
func TestTokenScanWriterCapsPartialLine(t *testing.T) {
	chunk := []byte(strings.Repeat("x", 4096))
	for _, tok := range []string{"", "found"} {
		s := &tokenScanWriter{w: failWriter{}, tok: tok}
		for range 64 {
			_, _ = s.Write(chunk)
		}
		if len(s.buf) > maxPartialLine {
			t.Fatalf("tok=%q: partial buffer %d bytes, want <= %d", tok, len(s.buf), maxPartialLine)
		}
	}
}
