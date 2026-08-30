package tui

import (
	"os"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestExportWritesOwnerOnlyFile pins the export file mode: transcripts can
// carry sensitive tool output, so the artifact must be readable by its owner
// only (0600), matching the tokens/settings store standard.
func TestExportWritesOwnerOnlyFile(t *testing.T) {
	m := wired(t)
	t.Chdir(t.TempDir()) // exports land in the process CWD

	m.panel = panelSessions
	m.sessions = []client.Session{{ID: "s1"}}
	m.panelSel = 0

	msg := exec(m.exportSelected("md"))
	em, ok := msg.(sessionExportedMsg)
	if !ok {
		t.Fatalf("export cmd yielded %#v, want sessionExportedMsg", msg)
	}
	if em.err != nil {
		t.Fatalf("export failed: %v", em.err)
	}
	info, err := os.Stat(em.path)
	if err != nil {
		t.Fatalf("stat exported file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("export %s mode = %o, want 600", em.path, got)
	}
}
