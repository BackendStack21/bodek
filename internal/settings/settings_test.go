package settings

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// homeDir points HOME at a fresh temp dir so tests never touch the real
// ~/.bodek, and returns the expected config path.
func homeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("BODEK_CONFIG", "") // never honor an inherited override in tests
	return filepath.Join(dir, ".bodek", "config.json")
}

func TestLoadMissingFile(t *testing.T) {
	path := homeDir(t)
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.Theme != "" {
		t.Errorf("Theme = %q, want empty", got.Theme)
	}
	if got.Bell != nil || got.Notify != nil || got.Plain != nil {
		t.Errorf("boolean settings = %+v, want all unset", got)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("Load created %s, want read-only behavior", path)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	homeDir(t)
	in := Settings{
		Theme:     "ember-light",
		Bell:      ptr(false),
		Notify:    ptr(true),
		Plain:     ptr(false),
		Verbosity: "quiet",
	}
	if err := Save(in); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Theme != in.Theme {
		t.Errorf("Theme = %q, want %q", got.Theme, in.Theme)
	}
	if got.Verbosity != "quiet" {
		t.Errorf("Verbosity = %q, want quiet", got.Verbosity)
	}
	for name, pair := range map[string][2]*bool{
		"Bell":   {got.Bell, in.Bell},
		"Notify": {got.Notify, in.Notify},
		"Plain":  {got.Plain, in.Plain},
	} {
		if pair[0] == nil || pair[1] == nil || *pair[0] != *pair[1] {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	homeDir(t)
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid-JSON error")
	}
}

func TestPathUnderHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("BODEK_CONFIG", "")
	want := filepath.Join(dir, ".bodek", "config.json")
	if got := Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestSaveCreatesDir(t *testing.T) {
	homeDir(t)
	if err := Save(Settings{Theme: "classic"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if fi, err := os.Stat(Path()); err != nil || fi.IsDir() {
		t.Fatalf("Save did not create %s: %v", Path(), err)
	}
}

func TestSaveNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("BODEK_CONFIG", "")
	if err := Save(Settings{Theme: "classic"}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Save() with no home = %v, want os.ErrNotExist", err)
	}
}

func TestSaveUnwritableDir(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("BODEK_CONFIG", filepath.Join(blocker, "config.json")) // a file can't be a dir
	if err := Save(Settings{}); err == nil {
		t.Error("Save() under an unwritable path = nil error, want failure")
	}
}

func TestBool(t *testing.T) {
	s := Settings{}
	if s.Bool(nil, true) != true || s.Bool(nil, false) != false {
		t.Error("nil pointer must resolve to the default")
	}
	on := true
	if !(Settings{}).Bool(&on, false) {
		t.Error("pointer must win over the default")
	}
	off := false
	if (Settings{}).Bool(&off, true) {
		t.Error("explicit false must survive")
	}
}

func TestLoadUnreadableFile(t *testing.T) {
	// A directory errors on read with something other than NotExist — that
	// error must surface, not silently read as defaults.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("BODEK_CONFIG", filepath.Join(dir, "config.json"))
	if err := os.MkdirAll(Path(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || os.IsNotExist(err) {
		t.Errorf("Load() over a directory = %v, want a read error", err)
	}
}

func TestBODEKConfigOverride(t *testing.T) {
	dir := t.TempDir()
	alt := filepath.Join(dir, "custom.json")
	t.Setenv("HOME", dir)
	t.Setenv("BODEK_CONFIG", alt)
	if err := Save(Settings{Theme: "high-contrast"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".bodek")); !os.IsNotExist(err) {
		t.Errorf("Save touched ~/.bodek despite BODEK_CONFIG override")
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Theme != "high-contrast" {
		t.Errorf("Theme = %q, want high-contrast", got.Theme)
	}
}

func ptr(b bool) *bool { return &b }
