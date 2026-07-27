package main

import (
	"bytes"
	"io"
	"testing"
)

// setVersion stamps the build version for the duration of a test.
func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestCurrentVersionStamped(t *testing.T) {
	setVersion(t, "0.0.10")
	if got := currentVersion(); got != "0.0.10" {
		t.Errorf("expected stamped version 0.0.10, got %q", got)
	}
}

func TestCurrentVersionDev(t *testing.T) {
	// With no stamped version, a `go test` binary reports "(devel)" via
	// ReadBuildInfo, so currentVersion must fall back to "dev".
	setVersion(t, "")
	if got := currentVersion(); got != "dev" {
		t.Errorf("expected dev fallback, got %q", got)
	}
}

func TestHandleSubcommandVersion(t *testing.T) {
	setVersion(t, "0.0.10")
	var out bytes.Buffer
	handled, err := handleSubcommand([]string{"version"}, &out)
	if err != nil || !handled {
		t.Fatalf("expected handled subcommand without error, got handled=%v err=%v", handled, err)
	}
	if got, want := out.String(), "bodek v0.0.10\n"; got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestHandleSubcommandVersionDev(t *testing.T) {
	setVersion(t, "")
	var out bytes.Buffer
	handled, err := handleSubcommand([]string{"version"}, &out)
	if err != nil || !handled {
		t.Fatalf("expected handled subcommand without error, got handled=%v err=%v", handled, err)
	}
	if got, want := out.String(), "bodek dev (built from source)\n"; got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestHandleSubcommandUnhandled(t *testing.T) {
	for _, args := range [][]string{nil, {"--sandbox"}, {"--url", "http://x"}} {
		handled, err := handleSubcommand(args, io.Discard)
		if err != nil || handled {
			t.Errorf("args %v: expected unhandled without error, got handled=%v err=%v", args, handled, err)
		}
	}
}
