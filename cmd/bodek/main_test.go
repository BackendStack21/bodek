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
	if cfg.sandbox || cfg.mouse || cfg.notify {
		t.Errorf("expected sandbox, mouse, notify false by default, got sandbox=%v mouse=%v notify=%v", cfg.sandbox, cfg.mouse, cfg.notify)
	}
	if !cfg.bel {
		t.Error("expected bel to default to true (attention bell on)")
	}
	if len(cfg.extraArgs) != 0 {
		t.Errorf("expected no extra args, got %v", cfg.extraArgs)
	}
}

func TestParseConfigMouseFlag(t *testing.T) {
	cfg, err := parseConfig([]string{"--mouse"}, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if !cfg.mouse {
		t.Error("expected --mouse to set mouse=true")
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
	cfg, err := parseConfig([]string{"--mouse", "--", "--prompt-caching", "--verbose"}, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if !cfg.mouse {
		t.Error("expected --mouse to be parsed before --")
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
	opts := buildProgramOptions(false, false)
	if len(opts) != 1 {
		t.Fatalf("expected 1 default program option, got %d", len(opts))
	}
	// Sanity check: the option is callable like a real tea.ProgramOption.
	var p tea.Program
	_ = p
	_ = opts[0]
}

func TestBuildProgramOptionsWithMouse(t *testing.T) {
	opts := buildProgramOptions(true, false)
	if len(opts) != 2 {
		t.Fatalf("expected 2 program options with mouse, got %d", len(opts))
	}
}

func TestBuildProgramOptionsPlain(t *testing.T) {
	if opts := buildProgramOptions(false, true); len(opts) != 0 {
		t.Fatalf("plain mode must skip the alt-screen, got %d options", len(opts))
	}
	if opts := buildProgramOptions(true, true); len(opts) != 1 {
		t.Fatalf("plain+mouse = %d options, want mouse only", len(opts))
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

func TestSettingsBooleansSeedDefaults(t *testing.T) {
	hermetic(t)
	on, off := true, false
	if err := settings.Save(settings.Settings{Mouse: &on, Plain: &off, Bell: &off}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if !cfg.mouse {
		t.Error("mouse = false, want true from the settings file")
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
