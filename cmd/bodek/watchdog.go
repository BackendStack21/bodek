package main

import (
	"context"
	"strconv"

	"github.com/BackendStack21/bodek/internal/watchdog"
)

// watchdogSubcommandName is the hidden re-exec entry the orphan guard uses.
const watchdogSubcommandName = "__bodek-watchdog"

// runWatchdog implements `bodek __bodek-watchdog <parentPID> <serverPID>`:
// a self-terminating guard that kills the spawned odek serve if the bodek
// process that launched it dies for any reason (SIGKILL, crash, lost
// terminal). Never user-facing; returns when the guard's job is done.
func runWatchdog(args []string) {
	if len(args) != 2 {
		return
	}
	parentPID, err1 := strconv.Atoi(args[0])
	serverPID, err2 := strconv.Atoi(args[1])
	if err1 != nil || err2 != nil {
		return
	}
	watchdog.Run(context.Background(), parentPID, serverPID,
		watchdog.DefaultPoll, watchdog.DefaultGrace)
}
