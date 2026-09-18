package tui

import (
	"errors"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestManualRetryInvalidatesPendingBackoffTick guards the reconnect
// generation: a manual ⏎ retry while a backoff tick is pending must
// supersede that chain — the old chain's reconnectMsg must be dropped (and
// its socket closed) instead of racing the new dial into a double adopt.
func TestManualRetryInvalidatesPendingBackoffTick(t *testing.T) {
	m := newTestModel()
	m.disconn = true
	m.opts.Reconnect = func() (*client.Client, error) { return nil, errors.New("dial refused") }

	// Chain 1: scheduled, tick still in flight.
	if cmd := m.scheduleReconnect(0); cmd == nil {
		t.Fatal("scheduleReconnect returned no cmd")
	}
	gen1 := m.reconnGen

	// Manual retry bumps the generation.
	_ = m.scheduleReconnect(0)
	gen2 := m.reconnGen
	if gen2 == gen1 {
		t.Fatal("manual retry did not bump the reconnect generation")
	}

	// The stale chain's outcome arrives: must be dropped, socket closed.
	stale := reconnectMsg{attempt: 0, gen: gen1, err: errors.New("superseded")}
	mm, _ := m.handleReconnect(stale)
	if mm.(*Model) != m {
		t.Fatal("stale reconnectMsg changed the model")
	}
	// disconn untouched: the current chain stays armed.
	if !m.disconn {
		t.Fatal("stale reconnectMsg must not clear disconn")
	}

	// The current chain's outcome is adopted. It dials and fails (hook
	// error), so the retry chain continues — but the result is consumed,
	// not dropped.
	// The current chain's outcome is consumed: the failure schedules
	// attempt+1, which opens the next generation and keeps disconn armed.
	mm2, cmd2 := m.handleReconnect(reconnectMsg{attempt: 0, gen: gen2, err: errors.New("dial refused")})
	if !mm2.(*Model).disconn {
		t.Fatal("failed redial must stay disconnected")
	}
	if cmd2 == nil {
		t.Fatal("failed redial did not schedule the next attempt")
	}
	if mm2.(*Model).reconnGen != gen2+1 {
		t.Fatalf("next attempt not scheduled in a fresh generation: %d", mm2.(*Model).reconnGen)
	}
}
