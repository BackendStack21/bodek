package tui

import (
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// notice_quiet_test.go — actionable-only notices. The info tier must not
// render signals the operator cannot act on: engine housekeeping
// (agent_signal), skill loads, memory merges, wake handshakes, and stray
// sub-agent frames are all owned by surfaces that already show the state
// (transcript cards, status line, header lamp, drawer tabs). Only failure
// of a user-initiated action keeps answering in the strip.

// TestEngineChatterNeverSurfaces feeds every suppressed engine frame
// through handleEvent and asserts the notice strip stays empty at the
// default (normal) verbosity.
func TestEngineChatterNeverSurfaces(t *testing.T) {
	m := newTestModel()
	for _, ev := range []client.Event{
		{Type: "skill", SubType: "loaded", SkillName: "docker-build"},
		{Type: "memory_event", SubType: "merge", Target: "env"},
		{Type: "agent_signal", SubType: "plan_dirty", Detail: "plan"},
		{Type: "agent_signal", SubType: "context_trimmed"},
		{Type: "agent_signal", SubType: "tool_running"},
		{Type: "bg_wake"},
		{Type: "subagent_log", SubType: "log", Name: "SA1", Data: "working"},
		{Type: "subagent_state", TaskIdx: 0, Status: "running"},
	} {
		m.handleEvent(ev)
	}
	if len(m.notices) != 0 {
		t.Fatalf("non-actionable engine frames must not reach the strip: %v", m.notices)
	}
}

// TestBgWakeStillArmsIdentity pins that suppression is render-only: the
// wake marker must still arm so the unprompted turn keeps its identity
// even though no note announces it.
func TestBgWakeStillArmsIdentity(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "bg_wake"})
	if !m.wakeArmed {
		t.Fatal("bg_wake suppression must not lose the wakeArmed identity marker")
	}
	if len(m.notices) != 0 {
		t.Fatalf("bg_wake must not surface a note: %v", m.notices)
	}
}

// TestJobStartDiffSilenced: a job materializing in the watcher diff is
// tab state, not transcript state — only terminal transitions with a
// non-zero exit (or no reported code) earn an alert-tier note.
func TestJobStartDiffSilenced(t *testing.T) {
	m := newTestModel()
	m.applyJobs([]client.Job{{ID: "j1", Status: "running", Command: "make test"}}, nil)
	if len(m.notices) != 0 {
		t.Fatalf("job start diff must not reach the strip: %v", m.notices)
	}
}

// TestJobCleanExitSilenced: a clean (exit 0) terminal transition is
// reported by the wake card / tab; the strip stays out of it.
func TestJobCleanExitSilenced(t *testing.T) {
	m := newTestModel()
	m.applyJobs([]client.Job{{ID: "j1", Status: "running", Command: "make test"}}, nil)
	zero := 0
	m.applyJobs([]client.Job{{ID: "j1", Status: "exited", Command: "make test", ExitCode: &zero}}, nil)
	if len(m.notices) != 0 {
		t.Fatalf("clean job exit must not reach the strip: %v", m.notices)
	}
}

// TestJobFailedExitKept: a non-zero exit is actionable — keep it, alert tier.
func TestJobFailedExitKept(t *testing.T) {
	m := newTestModel()
	m.applyJobs([]client.Job{{ID: "j1", Status: "running", Command: "make test"}}, nil)
	one := 1
	m.applyJobs([]client.Job{{ID: "j1", Status: "failed", Command: "make test", ExitCode: &one}}, nil)
	if note, _ := lastNoteMatching(m, "j1"); note == "" {
		t.Fatalf("failed job exit must stay in the strip: %v", m.notices)
	}
}

// TestOperatorFeedbackKept: the suppressed classes must not take the
// actionable answers with them — a declined sub-agent stop and an alert
// error still answer.
func TestOperatorFeedbackKept(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "subagent_cancelled", Accepted: false})
	if note, _ := lastNoteMatching(m, "stop declined"); note == "" {
		t.Fatalf("stop-decline feedback must keep surfacing: %v", m.notices)
	}
	m.addNote("error: boom")
	if note, _ := lastNoteMatching(m, "error: boom"); note == "" {
		t.Fatal("alert-tier errors must keep surfacing")
	}
}
