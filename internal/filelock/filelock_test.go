package filelock

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Lock excludes a second holder until released (flock locks are per open
// file description, so two Locks in one process still contend).
func TestLockExcludesUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	unlock := Lock(path)
	got := make(chan struct{})
	go func() {
		u := Lock(path)
		close(got)
		u()
	}()
	select {
	case <-got:
		t.Fatal("second Lock acquired while the first was held")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(wait + time.Second):
		t.Fatal("second Lock never acquired after release")
	}
}

// A peer holding the lock past wait degrades to an unlocked write instead
// of hanging persistence.
func TestLockGivesUpAfterWait(t *testing.T) {
	old := wait
	wait = 50 * time.Millisecond
	t.Cleanup(func() { wait = old })
	path := filepath.Join(t.TempDir(), "store.json")
	unlock := Lock(path)
	defer unlock()
	start := time.Now()
	Lock(path)()
	if time.Since(start) > 2*time.Second {
		t.Fatal("Lock blocked far past its wait")
	}
}

// An unopenable lock path and an empty path are no-ops, never panics.
func TestLockBestEffort(t *testing.T) {
	Lock("")()
	Lock(filepath.Join(t.TempDir(), "missing", "dir", "store.json"))()
}

// Serialized increments from many goroutines never lose an update.
func TestLockSerializesReadModifyWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counter")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer Lock(path)()
			b, _ := os.ReadFile(path)
			_ = os.WriteFile(path, append(b, 'x'), 0o600)
		}()
	}
	wg.Wait()
	b, _ := os.ReadFile(path)
	if n := strings.Count(string(b), "x"); n != 20 {
		t.Fatalf("lost updates: %d of 20", n)
	}
}
