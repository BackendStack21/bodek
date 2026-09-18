package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestForeignClearNotResurrected guards cross-instance consistency: when
// another bodek instance clears a directory's session (/new), this
// instance's next Save of a DIFFERENT directory must not republish the
// stale pre-clear session id over the fresher disk state.
func TestForeignClearNotResurrected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.json")

	a := openAt(path)
	a.Save("/w1", State{SessionID: "s1", Draft: "d1"})

	b := openAt(path) // second instance sees s1 on disk
	b.ClearSession("/w1")

	// Instance A persists a different cwd; the cleared /w1 must stay cleared.
	if err := a.Save("/w2", State{SessionID: "s2"}); err != nil {
		t.Fatalf("Save /w2: %v", err)
	}

	got := openAt(path).Load("/w1")
	if got.SessionID != "" {
		t.Fatalf("foreign ClearSession resurrected: /w1 session = %q, want empty", got.SessionID)
	}
	if got := openAt(path).Load("/w2"); got.SessionID != "s2" {
		t.Fatalf("Save /w2 lost: %q", got.SessionID)
	}
}

// openAt builds a Store pointed at an explicit path (Open has no path hook).
func openAt(path string) *Store {
	s := &Store{path: path, all: map[string]State{}}
	if _, err := os.ReadFile(path); err == nil {
		s.reloadLocked("")
	}
	return s
}
