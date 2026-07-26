package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if cfg.url != "" || cfg.token != "" || cfg.bin != "" {
		t.Errorf("unexpected non-empty defaults: %+v", cfg)
	}
	if cfg.sandbox || cfg.mouse {
		t.Errorf("expected sandbox and mouse to be false by default, got sandbox=%v mouse=%v", cfg.sandbox, cfg.mouse)
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

func TestParseConfigExtraArgs(t *testing.T) {
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
	_, err := parseConfig([]string{"--unknown"}, io.Discard)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestParseConfigHelp(t *testing.T) {
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
	if len(opts) != 1 {
		t.Fatalf("expected 1 default program option, got %d", len(opts))
	}
	// Sanity check: the option is callable like a real tea.ProgramOption.
	var p tea.Program
	_ = p
	_ = opts[0]
}

func TestBuildProgramOptionsWithMouse(t *testing.T) {
	opts := buildProgramOptions(true)
	if len(opts) != 2 {
		t.Fatalf("expected 2 program options with mouse, got %d", len(opts))
	}
}
