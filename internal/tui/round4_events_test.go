package tui

import (
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// Round-4 wave-1 regressions: runCtxCum reset on error, usage straggler
// must not open an orphan turn, stale wakeArmed must not mislabel.

func TestErrorResetsRunCtxCum(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	// Pre-v2.3 cumulative gauge mid-run.
	m.handleEvent(client.Event{Type: "usage", ContextTokens: 1500})
	if m.runCtxCum != 1500 {
		t.Fatalf("precondition: runCtxCum = %d", m.runCtxCum)
	}
	m.handleEvent(client.Event{Type: "error", Message: "boom"})
	if m.runCtxCum != 0 {
		t.Fatalf("error path leaked runCtxCum: %d", m.runCtxCum)
	}
	// Next run's cumulative restarts from its own baseline.
	m.handleEvent(client.Event{Type: "session", SessionID: "s2"})
	m.sendPrompt("next")
	m.handleEvent(client.Event{Type: "usage", ContextTokens: 1600})
	if m.winCtxTok != 1600 {
		t.Fatalf("gauge fill after error+new run = %d, want 1600", m.winCtxTok)
	}
}

func TestUsageStragglerDoesNotOpenOrphanTurn(t *testing.T) {
	m := newTestModel()
	m.sendPrompt("hi")
	m.handleEvent(client.Event{Type: "thinking", Content: "x"})
	m.handleEvent(client.Event{Type: "done"}) // finalize: idle, no open card
	n := len(m.msgs)

	// Straggler usage lands after finalize (batch ordering).
	m.handleEvent(client.Event{Type: "usage", ContextTokens: 100})
	if len(m.msgs) != n {
		t.Fatal("trailing usage opened an orphan turn card")
	}
	if m.busy {
		t.Fatal("trailing usage wedged the model busy")
	}
}

func TestStaleWakeArmedDoesNotSurviveFinalize(t *testing.T) {
	m := newTestModel()
	m.sendPrompt("long running turn")
	m.handleEvent(client.Event{Type: "thinking", Content: "x"})
	// bg_wake lands mid-turn: its wake turn opens later; the arm must not
	// outlive this turn's close-out.
	m.handleEvent(client.Event{Type: "bg_wake"})
	if !m.wakeArmed {
		t.Fatal("precondition: bg_wake mid-turn should arm")
	}
	m.handleEvent(client.Event{Type: "done"})
	if m.wakeArmed {
		t.Fatal("stale wakeArmed survived finalize — next idle-gap turn would be mislabeled as a wake")
	}
}
