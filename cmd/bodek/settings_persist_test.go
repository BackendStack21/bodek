package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A malformed settings file must never be overwritten by a later
// preference change: persistence is refused for the run instead of
// saving the zeroed struct as a full-replace write.
func TestParseConfigBrokenFileRefusesPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	broken := "{not json"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BODEK_CONFIG", path)

	var out bytes.Buffer
	cfg, err := parseConfig(nil, &out)
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if !cfg.persistDisabled {
		t.Fatal("parseConfig did not disable persistence for a broken settings file")
	}
	if warning := out.String(); !strings.Contains(warning, "not be saved") {
		t.Fatalf("startup warning %q does not mention persistence being disabled", warning)
	}

	// Simulate a theme change: the save path must leave the broken file
	// untouched rather than replacing it with a minimal settings file,
	// and must report the refusal instead of a silent success.
	cfg.persist.Theme = "dracula"
	if err := cfg.save(); err == nil {
		t.Fatal("save on a disabled persistence run must report an error, not succeed silently")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != broken {
		t.Fatalf("broken settings file was overwritten: %q", data)
	}

	// A healthy settings file keeps persistence enabled.
	ok := filepath.Join(dir, "ok.json")
	t.Setenv("BODEK_CONFIG", ok)
	cfg2, err := parseConfig(nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.persistDisabled {
		t.Fatal("persistence disabled for a valid (missing) settings file")
	}
	cfg2.persist.Theme = "ember-light"
	if err := cfg2.save(); err != nil {
		t.Fatalf("save returned error: %v", err)
	}
	if _, err := os.Stat(ok); err != nil {
		t.Fatalf("expected saved settings file: %v", err)
	}
}

// A file broken mid-run (a hand-edit typo after a clean startup) must not
// be replaced by the next preference change either.
func TestSaveRefusesFileBrokenMidRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("BODEK_CONFIG", path)
	cfg, err := parseConfig(nil, &bytes.Buffer{})
	if err != nil || cfg.persistDisabled {
		t.Fatalf("parseConfig: %v, disabled=%v", err, cfg.persistDisabled)
	}
	broken := `{"theme": "dracula",`
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.persist.Theme = "ember-light"
	if err := cfg.save(); err == nil {
		t.Fatal("save over a broken file must report an error")
	}
	if data, _ := os.ReadFile(path); string(data) != broken {
		t.Fatalf("broken settings file was overwritten: %q", data)
	}
}
