package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

// Bubble Tea winning the SIGINT race is still the orderly exit.
func TestRegressTUIInterruptIsCleanExit(t *testing.T) {
	if err := tuiExitErr(tea.ErrInterrupted); err != nil {
		t.Fatalf("interrupt reported as failure: %v", err)
	}
	if err := tuiExitErr(errors.New("boom")); err == nil {
		t.Fatal("real failure swallowed")
	}
}

// -h prints usage once and is not an error exit.
func TestRegressHelpPrintsUsageOnce(t *testing.T) {
	t.Setenv("BODEK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	var out strings.Builder
	_, err := parseConfig([]string{"-h"}, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
	if n := strings.Count(out.String(), "Usage:"); n != 1 {
		t.Fatalf("usage printed %d times", n)
	}
}

// Two instances saving different preferences at once keep both.
func TestRegressConcurrentSettingsSavesKeepBoth(t *testing.T) {
	t.Setenv("BODEK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	for i := 0; i < 20; i++ {
		a, err := parseConfig(nil, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := parseConfig(nil, io.Discard)
		a.persist.Theme = fmt.Sprintf("theme-%d", i)
		b.persist.Verbosity = fmt.Sprintf("v-%d", i)
		done := make(chan struct{})
		go func() { _ = a.save(); close(done) }()
		_ = b.save()
		<-done
		got, err := settings.Load()
		if err != nil {
			t.Fatal(err)
		}
		if got.Theme != a.persist.Theme || got.Verbosity != b.persist.Verbosity {
			t.Fatalf("round %d lost a save: %+v", i, got)
		}
	}
}
