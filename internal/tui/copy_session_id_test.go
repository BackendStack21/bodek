package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/workspace"
)

// /copy-session-id puts the active session id on the clipboard so the
// operator can paste it into `bodek --session <id>` later.

func runCopySessionID(m *Model) {
	for _, c := range slashCommands() {
		if c.name == "copy-session-id" {
			c.run(m, "")
			return
		}
	}
}

func TestCopySessionIDRegistered(t *testing.T) {
	for _, c := range slashCommands() {
		if c.name == "copy-session-id" {
			if c.desc == "" {
				t.Fatal("/copy-session-id registered with an empty description")
			}
			return
		}
	}
	t.Fatal("/copy-session-id is not in the command registry")
}

func TestCopySessionIDCopiesActiveSession(t *testing.T) {
	m := newTestModel()
	m.sessionID = "sess-abc123"
	runCopySessionID(m)
	if !m.copyFlashing() {
		t.Error("copying a session id must arm the ✓ Copied flash")
	}
}

func TestCopySessionIDWithoutSession(t *testing.T) {
	m := newTestModel()
	if cmd := m.copySessionID(); cmd == nil {
		t.Fatal("no-session copy must produce an explanatory note")
	}
	if m.copyFlashing() {
		t.Error("nothing was copied — the flash must stay off")
	}
	// The note must warn and teach: the id only exists once a session is
	// created (first prompt), so the operator knows to send a prompt first.
	if n := len(m.notices); n == 0 {
		t.Fatal("no-session copy must record a visible note")
	} else if got := m.notices[n-1]; !strings.Contains(got, "no session") || !strings.Contains(got, "prompt") {
		t.Errorf("note = %q, want a warning naming the session and a prompt hint", got)
	}
}

// A hostile id must never reach the clipboard — the payload is a
// paste-into-shell target.
func TestCopySessionIDRejectsUnsafeID(t *testing.T) {
	m := newTestModel()
	m.sessionID = "evil\nrm -rf"
	if cmd := m.copySessionID(); cmd == nil {
		t.Fatal("unsafe id must produce a note, not a panic")
	}
	if m.copyFlashing() {
		t.Error("nothing was copied — the flash must stay off")
	}
}

// SessionID() is what the CLI reads at teardown for the exit hint.
func TestSessionIDAccessor(t *testing.T) {
	m := newTestModel()
	if m.SessionID() != "" {
		t.Errorf("fresh model SessionID = %q, want empty", m.SessionID())
	}
	m.sessionID = "sess-9"
	if m.SessionID() != "sess-9" {
		t.Errorf("SessionID = %q, want sess-9", m.SessionID())
	}
}

// Options.ResumeSession resumes one exact session (the --session flag's
// landing spot): it beats the workspace-derived last session; --new still
// wins.
func TestResumeSessionOptionSeedsPendingResume(t *testing.T) {
	t.Setenv("BODEK_WORKSPACE", t.TempDir()+"/workspaces.json")
	ws := workspace.Open()
	m := newTestModel()
	m.ws = ws
	m.opts.CWD = "/tmp/bodek-test-restore"
	m.opts.ResumeSession = "sess-explicit"
	_ = ws.Save(m.opts.CWD, workspace.State{SessionID: "sess-last"})
	m.restoreWorkspace()
	if m.pendingResume != "sess-explicit" {
		t.Errorf("pendingResume = %q, want the explicit --session id", m.pendingResume)
	}
}

func TestResumeSessionFreshStillWins(t *testing.T) {
	t.Setenv("BODEK_WORKSPACE", t.TempDir()+"/workspaces.json")
	ws := workspace.Open()
	m := newTestModel()
	m.ws = ws
	m.opts.CWD = "/tmp/bodek-test-fresh"
	m.opts.ResumeSession = "sess-explicit"
	m.opts.Fresh = true
	m.restoreWorkspace()
	if m.pendingResume != "" {
		t.Errorf("--new must skip resume; pendingResume = %q", m.pendingResume)
	}
}
