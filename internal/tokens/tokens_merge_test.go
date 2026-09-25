package tokens

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression: persistLocked rewrote the whole map from the in-memory
// snapshot without re-reading disk, so two concurrent bodek instances
// erased each other's minted session tokens (B's token vanished when A's
// next Set persisted its stale snapshot).
func TestSetMergesForeignTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	a := openAt(path)
	b := openAt(path)

	a.Set("sess-a", "tok-a") // instance A mints and persists
	b.Set("sess-b", "tok-b") // instance B, opened before A's write

	// A fresh store must see both tokens.
	c := openAt(path)
	if got := c.Get("sess-a"); got != "tok-a" {
		t.Fatalf("foreign token lost: Get(sess-a) = %q", got)
	}
	if got := c.Get("sess-b"); got != "tok-b" {
		t.Fatalf("own token lost: Get(sess-b) = %q", got)
	}
}

// Regression: a stale peer must not resurrect tokens another instance
// deleted. B (opened before A's delete) writes an unrelated token — the
// wholesale disk adoption must keep the deletion converged.
func TestStalePeerKeepsPeerDeletions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	a := openAt(path)
	b := openAt(path)

	a.Set("sess-x", "tok-x") // both instances now know sess-x
	a.Delete("sess-x")       // A deletes it (persists the deletion)

	b.Set("sess-y", "tok-y") // stale B persists an unrelated write

	c := openAt(path)
	if got := c.Get("sess-x"); got != "" {
		t.Fatalf("deleted token resurrected by stale peer: %q", got)
	}
	if got := c.Get("sess-y"); got != "tok-y" {
		t.Fatalf("unrelated token lost: %q", got)
	}
}

// Regression: the .corrupt quarantine used a fixed name, so a second
// corrupting write replaced the first quarantined evidence (POSIX rename
// replaces its destination) — the earlier snapshot became undiagnosable.
func TestQuarantineRotatesBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")

	if err := os.WriteFile(path, []byte("{first"), 0o600); err != nil {
		t.Fatal(err)
	}
	openAt(path)
	first, err := os.ReadFile(path + ".corrupt")
	if err != nil || string(first) != "{first" {
		t.Fatalf("first quarantine missing: %q err=%v", first, err)
	}

	if err := os.WriteFile(path, []byte("{second"), 0o600); err != nil {
		t.Fatal(err)
	}
	openAt(path)
	if got, _ := os.ReadFile(path + ".corrupt"); string(got) != "{second" {
		t.Fatalf("latest quarantine wrong: %q", got)
	}
	if got, err := os.ReadFile(path + ".corrupt.1"); err != nil || string(got) != "{first" {
		t.Fatalf("first quarantine was clobbered: %q err=%v", got, err)
	}
}
