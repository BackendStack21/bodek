package tokens

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// On some platforms UserHomeDir consults other vars; set them too.
	t.Setenv("USERPROFILE", dir)

	s := Open()
	if got := s.Get("missing"); got != "" {
		t.Errorf("Get(missing) = %q, want empty", got)
	}
	s.Set("sess-1", "tok-1")
	s.Set("sess-2", "tok-2")
	if got := s.Get("sess-1"); got != "tok-1" {
		t.Errorf("Get(sess-1) = %q", got)
	}

	// A fresh Store should load what was persisted.
	s2 := Open()
	if got := s2.Get("sess-2"); got != "tok-2" {
		t.Errorf("reloaded Get(sess-2) = %q, want tok-2", got)
	}

	// Delete persists too.
	s2.Delete("sess-1")
	s3 := Open()
	if got := s3.Get("sess-1"); got != "" {
		t.Errorf("after delete Get(sess-1) = %q, want empty", got)
	}
	if got := s3.Get("sess-2"); got != "tok-2" {
		t.Errorf("after delete Get(sess-2) = %q, want tok-2", got)
	}

	if _, err := os.Stat(filepath.Join(dir, ".bodek", "sessions.json")); err != nil {
		t.Errorf("store file not written: %v", err)
	}
}

func TestStoreNilSafe(t *testing.T) {
	var s *Store
	if got := s.Get("x"); got != "" {
		t.Errorf("nil Get = %q", got)
	}
	s.Set("a", "b") // must not panic
	s.Delete("a")   // must not panic
}

func TestStoreIgnoresEmpty(t *testing.T) {
	s := &Store{m: map[string]string{}}
	s.Set("", "tok") // empty id ignored
	s.Set("id", "")  // empty token ignored
	if len(s.m) != 0 {
		t.Errorf("empty inputs were stored: %v", s.m)
	}
	s.Delete("nope") // missing id is a no-op
}

func TestSetNoChangeSkipsWrite(t *testing.T) {
	s := &Store{m: map[string]string{"id": "tok"}}
	s.Set("id", "tok") // identical value — should be a no-op
	if s.m["id"] != "tok" {
		t.Error("value changed unexpectedly")
	}
}

func TestNoPathPersistNoop(t *testing.T) {
	// A store with no path keeps values in memory and never touches disk.
	s := &Store{m: map[string]string{}}
	s.Set("id", "tok")
	if s.Get("id") != "tok" {
		t.Error("in-memory set failed")
	}
	s.Delete("id")
	if s.Get("id") != "" {
		t.Error("in-memory delete failed")
	}
}

func TestOpenNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	s := Open() // UserHomeDir errors → in-memory store with empty path
	s.Set("k", "v")
	if s.Get("k") != "v" {
		t.Error("in-memory store should still work without a home dir")
	}
}

func TestPersistMkdirError(t *testing.T) {
	dir := t.TempDir()
	// Make a regular file, then use it as if it were a directory in the path —
	// MkdirAll must fail, exercising persist's error branch.
	blocker := filepath.Join(dir, "afile")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Store{m: map[string]string{}, path: filepath.Join(blocker, "sub", "sessions.json")}
	s.Set("k", "v") // persist fails internally; value still cached in memory
	if s.Get("k") != "v" {
		t.Error("value should remain in memory even if persist fails")
	}
}

func TestPersistWriteAndRenameErrors(t *testing.T) {
	dir := t.TempDir()

	// WriteFile error: the temp target path is an existing directory.
	base := filepath.Join(dir, "a")
	if err := os.Mkdir(base+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{m: map[string]string{}, path: base}
	s.Set("k", "v") // WriteFile(base+".tmp") fails; value stays in memory
	if s.Get("k") != "v" {
		t.Error("value should remain in memory after write failure")
	}

	// Rename error: the destination path is an existing directory.
	d2 := filepath.Join(dir, "d")
	if err := os.Mkdir(d2, 0o755); err != nil {
		t.Fatal(err)
	}
	s2 := &Store{m: map[string]string{}, path: d2}
	s2.Set("k", "v") // WriteFile ok, Rename onto a directory fails
}

func TestOpenWithCorruptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	// Pre-seed a corrupt store file; Open must tolerate it.
	if err := os.MkdirAll(filepath.Join(dir, ".bodek"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".bodek", "sessions.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open()
	if s.Get("anything") != "" {
		t.Error("corrupt file should yield empty store")
	}
	s.Set("k", "v") // should still persist fine
	if Open().Get("k") != "v" {
		t.Error("set after corrupt-open did not persist")
	}
}
