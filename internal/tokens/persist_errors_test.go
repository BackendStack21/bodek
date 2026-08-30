package tokens

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPersistSurfacesErrors pins that persist reports failures instead of
// silently dropping them: a store that fails to save must be observable,
// otherwise session resume breaks with no diagnostic.
func TestPersistSurfacesErrors(t *testing.T) {
	dir := t.TempDir()
	// A regular file where the store directory should be makes MkdirAll fail.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := persist(filepath.Join(blocker, "sessions.json"), map[string]string{"id": "tok"})
	if err == nil {
		t.Error("persist against an unwritable path should report an error")
	}
}

// TestPersistNoTmpLeftOnRenameFailure pins the tmp cleanup: when the final
// rename fails, the staged .tmp file must not be left behind.
func TestPersistNoTmpLeftOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "sessions.json")
	// A non-empty directory at the store path makes the final rename fail.
	if err := os.Mkdir(store, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "occupied"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := persist(store, map[string]string{"id": "tok"}); err == nil {
		t.Error("persist onto a directory path should report an error")
	}
	if _, err := os.Stat(store + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("failed persist left the .tmp file behind: %v", err)
	}
}
