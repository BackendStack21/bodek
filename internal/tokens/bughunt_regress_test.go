package tokens

import (
	"os"
	"path/filepath"
	"testing"
)

// An unreadable (not missing) store must not be replaced by an empty one.
func TestRegressUnreadableStoreNotOverwritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(p, []byte(`{"old":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	s := openAt(p)
	s.Set("new", "tok2")
	_ = os.Chmod(p, 0o600)
	data, _ := os.ReadFile(p)
	if string(data) != `{"old":"tok"}` {
		t.Fatalf("unreadable store was replaced: %q", data)
	}
}
