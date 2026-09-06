package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/BackendStack21/bodek/internal/settings"
)

// hermetic points BODEK_CONFIG at a temp path (and clears BODEK_THEME) so
// parseConfig never reads the developer's real ~/.bodek/config.json.
func hermetic(t *testing.T) {
	t.Helper()
	t.Setenv("BODEK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("BODEK_THEME", "")
}

func TestParseConfigDefaults(t *testing.T) {
	hermetic(t)
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if cfg.url != "" || cfg.token != "" || cfg.bin != "" {
		t.Errorf("unexpected non-empty defaults: %+v", cfg)
	}
	if cfg.sandbox || cfg.notify {
		t.Errorf("expected sandbox, notify false by default, got sandbox=%v notify=%v", cfg.sandbox, cfg.notify)
	}
	if !cfg.bel {
		t.Error("expected bel to default to true (attention bell on)")
	}
	if len(cfg.extraArgs) != 0 {
		t.Errorf("expected no extra args, got %v", cfg.extraArgs)
	}
}

func TestParseConfigAttentionFlags(t *testing.T) {
	hermetic(t)
	cfg, err := parseConfig([]string{"--bel=false", "--notify"}, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if cfg.bel {
		t.Error("expected --bel=false to mute the attention bell")
	}
	if !cfg.notify {
		t.Error("expected --notify to enable desktop notifications")
	}
}

func TestParseConfigExtraArgs(t *testing.T) {
	hermetic(t)
	cfg, err := parseConfig([]string{"--notify", "--", "--prompt-caching", "--verbose"}, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if !cfg.notify {
		t.Error("expected --notify to be parsed before --")
	}
	want := []string{"--prompt-caching", "--verbose"}
	if len(cfg.extraArgs) != len(want) {
		t.Fatalf("expected extra args %v, got %v", want, cfg.extraArgs)
	}
	for i := range want {
		if cfg.extraArgs[i] != want[i] {
			t.Errorf("extra arg %d: want %q, got %q", i, want[i], cfg.extraArgs[i])
		}
	}
}

func TestParseConfigUnknownFlag(t *testing.T) {
	hermetic(t)
	_, err := parseConfig([]string{"--unknown"}, io.Discard)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestParseConfigHelp(t *testing.T) {
	hermetic(t)
	var out bytes.Buffer
	_, err := parseConfig([]string{"-h"}, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	if out.Len() == 0 {
		t.Error("expected usage output on -h")
	}
}

func TestBuildProgramOptionsDefault(t *testing.T) {
	opts := buildProgramOptions(false)
	if len(opts) != 3 {
		t.Fatalf("expected 3 default program options (filter, alt-screen, mouse), got %d", len(opts))
	}
	// Sanity check: the option is callable like a real tea.ProgramOption.
	var p tea.Program
	_ = p
	_ = opts[0]
}

func TestBuildProgramOptionsPlain(t *testing.T) {
	if opts := buildProgramOptions(true); len(opts) != 1 {
		t.Fatalf("plain mode must skip alt-screen and mouse (filter only), got %d options", len(opts))
	}
}

func TestApplyNoColor(t *testing.T) {
	defer lipgloss.SetColorProfile(termenv.TrueColor)
	t.Setenv("NO_COLOR", "1")
	applyNoColor()
	if lipgloss.ColorProfile() != termenv.Ascii {
		t.Error("NO_COLOR did not degrade the color profile to Ascii")
	}
}

func TestThemeSeededFromSettings(t *testing.T) {
	hermetic(t)
	if err := settings.Save(settings.Settings{Theme: "classic"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.theme != "classic" {
		t.Errorf("theme = %q, want classic seeded from the settings file", cfg.theme)
	}
}

func TestThemeFlagOverridesSettings(t *testing.T) {
	hermetic(t)
	if err := settings.Save(settings.Settings{Theme: "classic"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := parseConfig([]string{"--theme", "ember-light"}, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.theme != "ember-light" {
		t.Errorf("theme = %q, want the explicit flag to win", cfg.theme)
	}
}

func TestThemeEnvOverridesSettings(t *testing.T) {
	hermetic(t)
	t.Setenv("BODEK_THEME", "high-contrast")
	if err := settings.Save(settings.Settings{Theme: "classic"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.theme != "" {
		t.Errorf("theme = %q, want empty (env wins, resolved inside the tui)", cfg.theme)
	}
}

func TestVerbositySeededFromSettings(t *testing.T) {
	hermetic(t)
	if err := settings.Save(settings.Settings{Verbosity: "quiet"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.verbosity != "quiet" {
		t.Errorf("verbosity = %q, want quiet seeded from the settings file", cfg.verbosity)
	}
}

func TestVerbosityFlagOverridesSettings(t *testing.T) {
	hermetic(t)
	if err := settings.Save(settings.Settings{Verbosity: "quiet"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := parseConfig([]string{"--verbosity", "detailed"}, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.verbosity != "detailed" {
		t.Errorf("verbosity = %q, want the explicit flag to win", cfg.verbosity)
	}
}

func TestSettingsBooleansSeedDefaults(t *testing.T) {
	hermetic(t)
	off := false
	if err := settings.Save(settings.Settings{Plain: &off, Bell: &off}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.plain {
		t.Error("plain = true, want false from the settings file")
	}
	if cfg.bel {
		t.Error("bel = true, want false from the settings file")
	}
	if cfg.notify {
		t.Error("notify = true, want the built-in default")
	}
}

func TestParseConfigBrokenSettingsWarns(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("BODEK_CONFIG", cfgPath)
	t.Setenv("BODEK_THEME", "")
	if err := os.WriteFile(cfgPath, []byte("{bogus"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cfg, err := parseConfig(nil, &out)
	if err != nil {
		t.Fatalf("a broken settings file must not block startup: %v", err)
	}
	if !strings.Contains(out.String(), "ignoring settings file") {
		t.Errorf("output = %q, want a warning", out.String())
	}
	if cfg.bel != true { // built-in default after the file is dropped
		t.Error("bel should fall back to the built-in default")
	}
}
