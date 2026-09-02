package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── bg job completion visibility (J1 + J2) ──────────────────────────────────
//
// A finished background job used to surface as a 3-second transient note —
// on a 10-second poll cadence, with no command context and no attention
// trigger. Practically invisible. The contract now: terminal transitions
// post alert-tier notes that name the command, and fire the attention
// layer (bell / OSC 9) so the operator is actually told.

// exitedJob is a finished job snapshot with a recognizable command head.
func exitedJob(status string, code int) client.Job {
	c := code
	return client.Job{ID: "bg_0000abcd", Command: "gh pr checks 63 --watch",
		Status: status, RuntimeS: 104, ExitCode: &c}
}

// seedRunning primes the diff map so the next applyJobs sees a transition.
func seedRunning(t *testing.T) *Model {
	t.Helper()
	m := newTestModel()
	m.applyJobs([]client.Job{{ID: "bg_0000abcd", Command: "gh pr checks 63 --watch",
		Status: "running", RuntimeS: 3}}, nil)
	return m
}

// TestJobExitAlertTier pins J1: a terminal transition posts an alert-tier
// note (dwell in (noticeTTL, alertTTL]), not a 3s transient.
func TestJobExitAlertTier(t *testing.T) {
	m := seedRunning(t)
	m.applyJobs([]client.Job{exitedJob("exited", 0)}, nil)

	note, exp := lastNoteMatching(m, "bg_0000abcd")
	if note == "" {
		t.Fatal("exit transition posted no note")
	}
	if dwell := time.Until(exp); dwell <= noticeTTL || dwell > alertTTL {
		t.Errorf("exit note dwell = %v, want alert tier (%v, %v]", dwell, noticeTTL, alertTTL)
	}
}

// TestJobExitNoteNamesCommand pins the command context: the exit note
// carries the sanitized command head so the operator knows WHICH job.
func TestJobExitNoteNamesCommand(t *testing.T) {
	m := seedRunning(t)
	m.applyJobs([]client.Job{exitedJob("exited", 0)}, nil)

	note, _ := lastNoteMatching(m, "bg_0000abcd")
	for _, want := range []string{"gh pr checks 63 --watch", "exited 0", "1m44s"} {
		if !strings.Contains(note, want) {
			t.Errorf("exit note missing %q: %q", want, note)
		}
	}
}

// TestJobExitAttentionCmd pins J2: a transition fires the attention layer;
// a steady-state re-apply fires nothing.
func TestJobExitAttentionCmd(t *testing.T) {
	m := seedRunning(t)
	var attn tea.Cmd
	attn = m.applyJobs([]client.Job{exitedJob("exited", 0)}, nil)
	if attn == nil {
		t.Fatal("terminal transition fired no attention")
	}

	if attn = m.applyJobs([]client.Job{exitedJob("exited", 0)}, nil); attn != nil {
		t.Error("steady-state re-apply must not re-fire attention")
	}
}

// TestJobAttentionKinds covers the new kinds' rendering plan: ✓ for clean
// exits, ✗ for failures; notification and bell stay user-gated.
func TestJobAttentionKinds(t *testing.T) {
	if jobAttentionKind(exitedJob("exited", 0)) != attentionJobDone {
		t.Error("clean exit should map to attentionJobDone")
	}
	for _, s := range []string{"failed", "timeout", "killed"} {
		if jobAttentionKind(exitedJob(s, 1)) != attentionJobFailed {
			t.Errorf("%s should map to attentionJobFailed", s)
		}
	}

	m := newTestModel()
	m.notify = true
	if a := m.attentionFor(attentionJobDone); !strings.Contains(a.sequence(), "bg job done") {
		t.Errorf("done attention missing title/notify: %q", a.sequence())
	}
	if a := m.attentionFor(attentionJobFailed); !strings.Contains(a.sequence(), "bg job failed") {
		t.Errorf("failed attention missing title/notify: %q", a.sequence())
	}
	m.notify = false
	if a := m.attentionFor(attentionJobDone); strings.Contains(a.sequence(), "\x1b]9;") {
		t.Errorf("notify disabled but OSC 9 emitted: %q", a.sequence())
	}
}
