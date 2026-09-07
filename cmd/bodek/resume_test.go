package main

import (
	"io"
	"os"
	"testing"
)

// settingsSave writes a settings file at the hermetic BODEK_CONFIG path.
func settingsSave(t *testing.T, json string) error {
	t.Helper()
	return os.WriteFile(os.Getenv("BODEK_CONFIG"), []byte(json), 0o600)
}

// resumes reports whether the parsed config would actually resume the last
// session — the same expression run() uses to set tui Options.Fresh.
func resumes(cfg config) bool { return !cfg.fresh && cfg.resume }

// Resume is disabled by default: a bare `bodek` must start fresh unless the
// operator opts in via the settings file or --resume.
func TestResumeDisabledByDefault(t *testing.T) {
	hermetic(t)
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if resumes(cfg) {
		t.Error("no flags: last-session resume must be off by default")
	}
	if cfg.resume {
		t.Error("no flags: cfg.resume must default to false")
	}
}

// --resume opts back in for one launch.
func TestResumeFlagOptsIn(t *testing.T) {
	hermetic(t)
	cfg, err := parseConfig([]string{"--resume"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !resumes(cfg) {
		t.Error("--resume must allow last-session resume")
	}
	if !cfg.resume {
		t.Error("--resume must set cfg.resume")
	}
}

// --new still wins over an explicit --resume (and over the settings file).
func TestNewBeatsResume(t *testing.T) {
	hermetic(t)
	cfg, err := parseConfig([]string{"--resume", "--new"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if resumes(cfg) {
		t.Error("--new must force a fresh start even with --resume")
	}
}

// The settings file can opt in persistently: {"resume": true}.
func TestResumeSettingOptsIn(t *testing.T) {
	hermetic(t)
	if err := settingsSave(t, `{"resume": true}`); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !resumes(cfg) {
		t.Error(`settings {"resume": true} must enable last-session resume`)
	}
}

// An explicit --resume=false beats an opted-in settings file.
func TestResumeFlagFalseBeatsSetting(t *testing.T) {
	hermetic(t)
	if err := settingsSave(t, `{"resume": true}`); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseConfig([]string{"--resume=false"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if resumes(cfg) {
		t.Error("--resume=false must disable resume even when the settings file opts in")
	}
}
