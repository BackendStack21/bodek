package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Regression: persist wrote a FIXED sessions.json.tmp shared by every
// caller, and Set/Delete persisted after releasing s.mu — two interleaved
// persists could publish a torn file (A.WriteFile → B.WriteFile truncates →
// A.Rename) that silently wipes the token store on next Open.
func TestPersistSerializesUnderStoreLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	s := &Store{m: map[string]string{}, path: path}

	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(2)
		go func(i int) { defer wg.Done(); s.Set(string(rune('a'+i%26)), "tok") }(i)
		go func() { defer wg.Done(); s.Delete("zz") }()
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("store unreadable after concurrent Set/Delete: %v", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("published store is not valid JSON (torn write): %v\n%s", err, data)
	}
	if len(m) == 0 {
		t.Fatal("store empty after 64 Sets — a torn rename dropped the entries")
	}
}
