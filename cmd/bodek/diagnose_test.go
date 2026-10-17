package main

import (
	"bytes"
	"errors"
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

// An invalid --url is a typo no retry fixes: no startup card, no loop.
func TestConnectWithDiagnosisInvalidURLSkipsRetry(t *testing.T) {
	var stderr bytes.Buffer
	_, err := connectWithDiagnosis(server.Options{URL: "ftp://127.0.0.1:1"}, &stderr, bytes.NewReader(nil))
	if !errors.Is(err, server.ErrInvalidURL) {
		t.Fatalf("err = %v, want ErrInvalidURL", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("printed a startup card for a bad --url: %q", stderr.String())
	}
}
