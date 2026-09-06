package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/server"
)

func TestParseConfigNewFlag(t *testing.T) {
	hermetic(t)
	cfg, err := parseConfig([]string{"--new"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.fresh {
		t.Fatal("--new must set fresh")
	}
}

func TestConnectWithDiagnosisNonTTY(t *testing.T) {
	var stderr bytes.Buffer
	_, err := connectWithDiagnosis(server.Options{
		Bin:    "bodek-odek-missing-binary",
		Stderr: io.Discard,
	}, &stderr, bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected connect failure")
	}
	if !strings.Contains(stderr.String(), "bodek could not start") {
		t.Errorf("stderr = %q, want a diagnosis card", stderr.String())
	}
	if !strings.Contains(stderr.String(), "retry: ⏎") {
		t.Errorf("card missing retry: %q", stderr.String())
	}
}
