//go:build unix

package tui

import (
	"os"
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

// A persisted attachment that became a symlink (planted via the workspace)
// is not silently re-read at startup.
func TestRestoreAttachmentSkipsSymlink(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "notes.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	m := newTestModel()
	_ = m.restoreAttachment(link)
	if len(m.attachments) != 0 {
		t.Fatal("a symlinked persisted attachment was re-staged")
	}
	_ = m.attachFile(link) // an explicit /attach still follows the operator's choice
	if len(m.attachments) != 1 {
		t.Fatal("explicit attach of a symlink to a regular file should still work")
	}
}

// The descriptor itself is vetted: a non-regular file never reaches the read.
func TestReadCappedRejectsFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "p")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo unsupported:", err)
	}
	if _, err := readCapped(fifo, 10); err == nil {
		t.Fatal("readCapped read a FIFO")
	}
}

// A symlink planted on a parent directory of a persisted path is caught too.
func TestRestoreAttachmentSkipsSymlinkedParent(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(root, "docs")
	if err := os.Mkdir(docs, 0o700); err != nil {
		t.Fatal(err)
	}
	persisted := filepath.Join(docs, "config")
	if err := os.WriteFile(persisted, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := filepath.Join(root, "secrets")
	if err := os.Mkdir(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "config"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(docs); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secrets, docs); err != nil {
		t.Fatal(err)
	}
	m := newTestModel()
	_ = m.restoreAttachment(persisted)
	if len(m.attachments) != 0 {
		t.Fatal("a persisted path behind a planted directory symlink was re-staged")
	}
}
