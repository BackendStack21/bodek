//go:build unix

package tui

import (
	"path/filepath"
	"syscall"
	"testing"
)

// A FIFO would block the UI goroutine forever on read.
func TestAttachRejectsNonRegularFile(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo unsupported:", err)
	}
	m := newTestModel()
	_ = m.attachFile(fifo)
	if len(m.attachments) != 0 {
		t.Fatal("a FIFO was attached")
	}
}
