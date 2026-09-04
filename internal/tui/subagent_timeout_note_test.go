package tui

import (
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// Sub-agent failure notes ("sub-agent SA4 timeout") used to be sticky —
// pushed with a zero expiry and only retired at turn finalize. A turn that
// kept streaming after the sub-agent died kept the note on screen
// indefinitely. The strip is bounded by design: failure notes now dwell at
// alert tier and fade like every other alert. Nothing is lost — the failure
// stays on the card's agent chip and the turn's swarm verdict.
func TestSubagentTimeoutNoteIsBounded(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "tool_call", Name: "delegate_tasks", Data: `{"tasks":[]}`})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "timeout"})

	if n := len(m.notices); n == 0 {
		t.Fatal("timeout note missing")
	}
	// The strip may also carry a JIT hint from the state frame; the
	// timeout note itself must exist verbatim, at alert tier.
	idx := -1
	for i, n := range m.notices {
		if n == "sub-agent SA1 timeout" {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Fatalf("note = %q, want it to contain %q", m.notices, "sub-agent SA1 timeout")
	}
	if exp := m.noticeExp[idx]; exp.IsZero() {
		t.Fatal("timeout note is sticky (zero expiry) — it would never disappear")
	}
	// The failure itself stays on the card's agent chip regardless.
	if i := m.cur(); i < 0 {
		t.Fatal("delegate card lost")
	} else if a := m.msgs[i].steps[0].card("t1"); a == nil || a.status != "timeout" {
		t.Fatalf("agent card status = %+v, want timeout", a)
	}

	// Past the alert dwell the note is gone.
	m.pruneNotices(time.Now().Add(alertTTL + time.Second))
	for _, n := range m.notices {
		if n == "sub-agent SA1 timeout" {
			t.Fatal("timeout note outlived alertTTL")
		}
	}
}

// Every terminal sub-agent note is bounded: errors and timeouts at alert
// tier, user-initiated cancels transient — nothing sticky in the strip.
func TestSubagentFailureNotesAllBounded(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "tool_call", Name: "delegate_tasks", Data: `{"tasks":[]}`})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "error"})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "finished", Status: "cancelled"})
	for i, exp := range m.noticeExp {
		if exp.IsZero() {
			t.Fatalf("notice %q is sticky", m.notices[i])
		}
	}
}
