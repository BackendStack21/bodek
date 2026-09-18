package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Regression: persist staged at a FIXED path+".tmp" while Save/Patch
// persisted after releasing s.mu — two interleaved persists could publish
// a torn file (A.Write → B.Write truncates → A.Rename) that wipes every
// workspace's draft/queue/session on next Open.
func TestPersistSerializesAcrossConcurrentSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	t.Setenv("BODEK_WORKSPACE", path)

	s := Open()

	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(2)
		go func(i int) { defer wg.Done(); _ = s.Save("/proj/a", State{Draft: "d", History: []string{"h"}}) }(i)
		go func() { defer wg.Done(); s.Patch("/proj/b", func(st *State) { st.Queue = append(st.Queue, "q") }) }()
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("workspace unreadable after concurrent Save/Patch: %v", err)
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("published workspace is not valid JSON (torn write): %v\n%s", err, data)
	}
	if len(f.Workspaces) == 0 {
		t.Fatal("workspace empty after 64 saves — a torn rename dropped the entries")
	}
}

// Regression: a corrupt workspaces.json was silently reset — every saved
// draft, queue, and session id was dropped with no diagnostic. The corrupt
// file must be quarantined as <path>.corrupt instead.
func TestOpenQuarantinesCorruptWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	t.Setenv("BODEK_WORKSPACE", path)

	s := Open()
	if s == nil {
		t.Fatal("Open returned nil")
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("corrupt workspace was not quarantined as %s.corrupt: %v", path, err)
	}
}
