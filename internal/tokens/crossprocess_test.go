package tokens

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Two instances persisting concurrently must not drop each other's tokens:
// the in-process mutex cannot order writes across processes.
func TestRegressConcurrentInstancesKeepEveryToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	a, b := openAt(path), openAt(path)
	var wg sync.WaitGroup
	for _, s := range []struct {
		st     *Store
		prefix string
	}{{a, "a"}, {b, "b"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				s.st.Set(fmt.Sprintf("%s%d", s.prefix, i), "tok")
			}
		}()
	}
	wg.Wait()
	if got := len(openAt(path).m); got != 80 {
		t.Fatalf("store kept %d of 80 tokens", got)
	}
}

// A symlinked store (dotfile managers) stays a symlink across saves.
func TestRegressSymlinkedStoreSurvivesSave(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "tokens.json")
	if err := os.WriteFile(real, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unsupported")
	}
	openAt(link).Set("s1", "tok")
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("save replaced the symlink with a regular file")
	}
	if openAt(real).Get("s1") != "tok" {
		t.Fatal("token not written through the link")
	}
}
