package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/workspace"
)

// --session <id> resumes one exact session and implies --resume.

func TestSessionFlagParses(t *testing.T) {
	cfg, err := parseConfig([]string{"--session", "sess-abc123"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.sessionID != "sess-abc123" {
		t.Errorf("cfg.sessionID = %q, want sess-abc123", cfg.sessionID)
	}
	if resumes(cfg) {
		t.Error("--session must not flip the bare --resume path (it drives Options.ResumeSession)")
	}
}

func TestSessionFlagDefaultsEmpty(t *testing.T) {
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.sessionID != "" {
		t.Errorf("cfg.sessionID = %q, want empty by default", cfg.sessionID)
	}
}

func TestSessionFlagImpliesResume(t *testing.T) {
	cfg, err := parseConfig([]string{"--session", "sess-abc123"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if fresh(cfg) {
		t.Error("--session must opt into resume-on-start")
	}
	cfgNew, err := parseConfig([]string{"--session", "sess-abc123", "--new"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh(cfgNew) {
		t.Error("--new must force a fresh start even with --session")
	}
}

// isolateWS points BODEK_WORKSPACE at a temp dir so tests never touch the
// real ~/.bodek/workspaces.json.
func isolateWS(t *testing.T) {
	t.Helper()
	t.Setenv("BODEK_WORKSPACE", t.TempDir()+"/workspaces.json")
}

// The Ctrl+C closure hint names the session just closed so the printed
// line is directly runnable: bodek --session <id>. The live id wins over
// the store.

func TestResumeHintWithSavedSession(t *testing.T) {
	isolateWS(t)
	ws := workspace.Open()
	cwd := "/tmp/bodek-hint-test"
	_ = ws.Save(cwd, workspace.State{SessionID: "sess-9"})
	if got := resumeHintLine(ws, cwd, ""); !strings.Contains(got, "bodek --session sess-9") {
		t.Errorf("hint %q must name the exact session", got)
	}
	// The live session id beats a stale store value.
	if got := resumeHintLine(ws, cwd, "sess-live"); !strings.Contains(got, "bodek --session sess-live") {
		t.Errorf("live id must win, got %q", got)
	}
}

func TestResumeHintWithoutSession(t *testing.T) {
	isolateWS(t)
	if got := resumeHintLine(workspace.Open(), "/tmp/bodek-hint-empty", ""); got != "" {
		t.Errorf("no saved session must yield no hint, got %q", got)
	}
	if got := resumeHintLine(nil, "/tmp/bodek-hint-nil", ""); got != "" {
		t.Errorf("nil store must yield no hint, got %q", got)
	}
}

// A hostile id (from a corrupted store) must never reach the printed,
// paste-into-shell hint line.
func TestResumeHintRejectsUnsafeID(t *testing.T) {
	cases := []string{"", "two\nlines", "ansi\x1b[31m", "sp ace", "../../etc"}
	for _, id := range cases {
		if got := resumeHintLine(nil, "", id); got != "" {
			t.Errorf("unsafe id %q produced hint %q", id, got)
		}
	}
}

func TestPrintResumeHint(t *testing.T) {
	isolateWS(t)
	var buf bytes.Buffer
	printResumeHint(nil, "/tmp/bodek-hint-nil", "", &buf)
	if buf.Len() != 0 {
		t.Errorf("nil store must print nothing, got %q", buf.String())
	}
	buf.Reset()
	printResumeHint(workspace.Open(), "/tmp/bodek-hint-print", "sess-9", &buf)
	if got := buf.String(); !strings.Contains(got, "bodek --session sess-9") {
		t.Errorf("hint output %q must carry the full command", got)
	}
}
