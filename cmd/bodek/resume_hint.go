package main

import (
	"fmt"
	"io"
	"regexp"

	"github.com/BackendStack21/bodek/internal/workspace"
)

// fresh reports whether this launch must skip resume: --new always wins,
// and without --resume or --session the default is a fresh start.
func fresh(cfg config) bool {
	return cfg.fresh || (!cfg.resume && cfg.sessionID == "")
}

// sessionIDRe guards ids that reach the printed hint (a paste-into-shell
// target): a corrupted store entry must never smuggle a newline or escape
// into what the operator is told to run.
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// resumeHintLine builds the after-exit resume hint naming the exact session
// that was just closed — the same id /copy-session-id puts on the
// clipboard. The live session id wins; the cwd store is the fallback for
// runs that never reached a session frame. An empty string means there is
// nothing to suggest.
func resumeHintLine(ws *workspace.Store, cwd, liveID string) string {
	id := liveID
	if id == "" && ws != nil && cwd != "" {
		id = ws.Load(cwd).SessionID
	}
	if !sessionIDRe.MatchString(id) {
		return ""
	}
	return fmt.Sprintf("⎸ resume the session you just closed:  bodek --session %s", id)
}

// printResumeHint writes the hint after the TUI has left the alt screen
// (stderr is ours then, next to the shutdown messages).
func printResumeHint(ws *workspace.Store, cwd, liveID string, w io.Writer) {
	if line := resumeHintLine(ws, cwd, liveID); line != "" {
		_, _ = fmt.Fprintln(w, line)
	}
}
