package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression: the corrupt-store quarantine used a fixed .corrupt name, so a
// second corrupting write replaced the first quarantined evidence instead of
// rotating it aside. Mirrors the tokens.go fix.
func TestQuarantineRotatesBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	t.Setenv("BODEK_WORKSPACE", path)

	if err := os.WriteFile(path, []byte("{first"), 0o600); err != nil {
		t.Fatal(err)
	}
	Open()
	first, err := os.ReadFile(path + ".corrupt")
	if err != nil || string(first) != "{first" {
		t.Fatalf("first quarantine missing: %q err=%v", first, err)
	}

	if err := os.WriteFile(path, []byte("{second"), 0o600); err != nil {
		t.Fatal(err)
	}
	Open()
	if got, _ := os.ReadFile(path + ".corrupt"); string(got) != "{second" {
		t.Fatalf("latest quarantine wrong: %q", got)
	}
	if got, err := os.ReadFile(path + ".corrupt.1"); err != nil || string(got) != "{first" {
		t.Fatalf("first quarantine was clobbered: %q err=%v", got, err)
	}
}
