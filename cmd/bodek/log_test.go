package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRingWriterKeepsRecentLines(t *testing.T) {
	w := newRingWriter(3)
	for i := range 5 {
		if _, err := w.Write([]byte("line" + string(rune('0'+i)) + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	got := w.String()
	want := "line2\nline3\nline4"
	if got != want {
		t.Fatalf("tail = %q, want %q", got, want)
	}
}

func TestRingWriterPartialLine(t *testing.T) {
	w := newRingWriter(5)
	// Write a line split across calls, plus an unterminated tail.
	for _, s := range []string{"hel", "lo\nwor", "ld"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got, want := w.String(), "hello\nworld"; got != want {
		t.Fatalf("tail = %q, want %q", got, want)
	}
}

func TestRingWriterEmpty(t *testing.T) {
	if got := newRingWriter(3).String(); got != "" {
		t.Fatalf("empty tail = %q, want \"\"", got)
	}
}

func TestOpenServerLogTruncates(t *testing.T) {
	// Redirect TempDir to an isolated location for the test.
	t.Setenv("TMPDIR", t.TempDir())

	w, path, closeLog := openServerLog()
	if path == "" {
		t.Fatal("openServerLog returned empty path")
	}
	if filepath.Base(path) != serverLogName {
		t.Fatalf("log name = %q, want %q", filepath.Base(path), serverLogName)
	}
	if _, err := w.Write([]byte("first run\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	closeLog()

	// A second open must truncate the prior contents.
	w2, _, closeLog2 := openServerLog()
	if _, err := w2.Write([]byte("second")); err != nil {
		t.Fatalf("write: %v", err)
	}
	closeLog2()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if got := string(data); got != "second" {
		t.Fatalf("log = %q, want %q (not truncated?)", got, "second")
	}
	if strings.Contains(string(data), "first run") {
		t.Fatal("prior run's log was not truncated")
	}
}
