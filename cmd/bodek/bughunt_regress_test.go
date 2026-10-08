package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/BackendStack21/bodek/internal/settings"
)

// Another instance's saved preference must survive this instance's save.
func TestRegressSettingsSaveKeepsOtherInstanceChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("BODEK_CONFIG", path)
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	// Instance A saves a theme after this instance started.
	if err := settings.Save(settings.Settings{Theme: "ember-light"}); err != nil {
		t.Fatal(err)
	}
	// This instance changes only verbosity.
	cfg.persist.Verbosity = "quiet"
	if err := cfg.save(); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Theme != "ember-light" {
		t.Fatalf("theme saved by the other instance was lost: %+v", got)
	}
}

// The log holds odek's `WS token:` line; a pre-existing permissive file must
// not be reused as-is.
func TestRegressServerLogNotReusedWhenPermissive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	p := filepath.Join(dir, serverLogName)
	if err := os.WriteFile(p, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o666); err != nil {
		t.Fatal(err)
	}
	_, path, closeFn := openServerLog()
	defer closeFn()
	if path == "" {
		return // refusing the file is acceptable
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("token-bearing log is group/world accessible: %v", fi.Mode().Perm())
	}
}
