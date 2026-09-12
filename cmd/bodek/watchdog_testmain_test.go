package main

import (
	"os"
	"testing"
)

// TestMain routes a hidden-subcommand re-exec of the test binary into the
// orphan-guard path (mirroring run()), instead of letting the testing
// package reject the non-flag argv.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == watchdogSubcommandName {
		runWatchdog(os.Args[2:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}
