package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Two instances saving different directories concurrently keep both.
func TestRegressConcurrentInstancesKeepEveryWorkspace(t *testing.T) {
	t.Setenv("BODEK_WORKSPACE", filepath.Join(t.TempDir(), "workspace.json"))
	a, b := Open(), Open()
	var wg sync.WaitGroup
	for _, s := range []struct {
		st     *Store
		prefix string
	}{{a, "/a"}, {b, "/b"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				_ = s.st.Save(fmt.Sprintf("%s%d", s.prefix, i), State{Draft: "d"})
			}
		}()
	}
	wg.Wait()
	if got := len(Open().all); got != 60 {
		t.Fatalf("workspace kept %d of 60 directories", got)
	}
}

// A symlinked workspace file stays a symlink across saves.
func TestRegressSymlinkedWorkspaceSurvivesSave(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "workspace.json")
	if err := os.WriteFile(real, []byte(`{"workspaces":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unsupported")
	}
	t.Setenv("BODEK_WORKSPACE", link)
	if err := Open().Save("/x", State{Draft: "d"}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("save replaced the symlink with a regular file")
	}
}
