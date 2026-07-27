package main

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

// version is stamped by GoReleaser via -X main.version; empty for local builds.
var version string

// currentVersion reports the running build's version: the GoReleaser-stamped
// value, the module version for `go install …@latest` builds, or "dev" for
// anything else (plain `go build`, `go run`).
func currentVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// handleSubcommand runs the bare subcommands (`bodek version`,
// `bodek upgrade`) before flag parsing kicks in. It reports whether args
// named a subcommand; anything else falls through to the normal TUI path.
func handleSubcommand(args []string, stdout io.Writer) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "version":
		if v := currentVersion(); v == "dev" {
			_, _ = fmt.Fprintln(stdout, "bodek dev (built from source)")
		} else {
			_, _ = fmt.Fprintf(stdout, "bodek v%s\n", strings.TrimPrefix(v, "v"))
		}
		return true, nil
	case "upgrade":
		return true, runUpgrade(stdout)
	}
	return false, nil
}
