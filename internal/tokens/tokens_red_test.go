package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// quarantinePath mirrors Open's quarantine layout: a corrupt store is set
// aside as <name>.corrupt instead of being silently overwritten.
func quarantinePath(path string) string {
	return path + ".corrupt"
}

// RED: Open must not silently wipe a corrupt store — it quarantines the
// bytes as sessions.json.corrupt so nothing is lost, then works in-memory.
func TestOpenQuarantinesCorruptStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	corrupt := `{"sess-1": "tok-1", "broken`
	if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	s := openAt(path) // direct-path variant of Open for tests
	if got := s.Get("sess-1"); got != "" {
		t.Fatalf("corrupt store yielded token %q, want none", got)
	}
	q, err := os.ReadFile(quarantinePath(path))
	if err != nil {
		t.Fatalf("corrupt store not quarantined: %v", err)
	}
	if string(q) != corrupt {
		t.Fatalf("quarantine content changed: %q", q)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt store still in place at %s (err=%v)", path, err)
	}
	// The store keeps working and re-persists cleanly.
	s.Set("sess-2", "tok-2")
	if s.Get("sess-2") != "tok-2" {
		t.Fatal("store unusable after quarantine")
	}
	var m map[string]string
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("re-persisted store invalid: %v", err)
	}
	if m["sess-2"] != "tok-2" {
		t.Fatalf("re-persisted store wrong: %v", m)
	}
}

// The interleaved-persist race: Set and Delete snapshot under the store
// mutex but persisted outside it, so a slow Set persist can resurrect a
// deleted token on disk. The fix serializes persist with the snapshot.
// This stress loop asserts the on-disk store always equals the in-memory
// view once the calls return.
func TestSetDeletePersistMatchesMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s := openAt(path)
	s.Set("keep", "kt")

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			s.Set("churn", "t")
		}(i)
		go func() {
			defer wg.Done()
			s.Delete("churn")
		}()
	}
	wg.Wait()

	s.mu.Lock()
	want := make(map[string]string, len(s.m))
	for k, v := range s.m {
		want[k] = v
	}
	s.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("store corrupt after churn: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("on-disk store diverged: disk=%v memory=%v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("on-disk store diverged: disk=%v memory=%v", got, want)
		}
	}
}
