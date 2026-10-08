package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// serverLogName is the fixed file the spawned server's stderr is captured to.
// A stable, predictable path lets users `tail -f` it; it is truncated per run
// so it never grows without bound.
const serverLogName = "bodek-odek-serve.log"

// openServerLog (re)creates the server log file. On failure it falls
// back to a discarding writer so a missing temp dir never breaks startup.
func openServerLog() (w io.Writer, path string, closeFn func()) {
	path = filepath.Join(os.TempDir(), serverLogName)
	// The log receives odek's "WS token:" line. Never reuse an existing
	// file: another user may have pre-created it (or a symlink to a file of
	// theirs) with open permissions. Remove it and create exclusively; if the
	// remove is refused (not ours, sticky dir) the create fails and logging
	// is skipped rather than leaking the token.
	_ = os.Remove(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return io.Discard, "", func() {}
	}
	return f, path, func() { _ = f.Close() }
}

// ringWriter keeps the most recent lines written to it. It backs the in-memory
// tail of the spawned server's stderr, which we surface if the server dies
// before (or instead of) the TUI ever starting.
type ringWriter struct {
	mu      sync.Mutex
	maxLine int
	lines   []string
	partial []byte
}

func newRingWriter(maxLine int) *ringWriter {
	return &ringWriter{maxLine: maxLine}
}

func (w *ringWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		w.lines = append(w.lines, string(w.partial[:i]))
		w.partial = w.partial[i+1:]
		if len(w.lines) > w.maxLine {
			w.lines = w.lines[len(w.lines)-w.maxLine:]
		}
	}
	return len(p), nil
}

// String returns the buffered tail, including any unterminated final line.
func (w *ringWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	lines := w.lines
	if len(w.partial) > 0 {
		lines = append(append([]string(nil), lines...), string(w.partial))
	}
	return strings.Join(lines, "\n")
}
