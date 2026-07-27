package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// A disconnect with a Reconnect hook must schedule a redial instead of
// settling straight into the dead "disconnected" state.
func TestDisconnectSchedulesReconnect(t *testing.T) {
	m := newTestModel()
	m.opts.Reconnect = func() (*client.Client, error) { return nil, errors.New("down") }

	_, cmd := m.handleEvent(client.Event{Type: client.EventDisconnected})

	if cmd == nil {
		t.Fatal("expected a reconnect attempt to be scheduled")
	}
	if !strings.Contains(m.status, "reconnecting") {
		t.Errorf("status = %q, want it to mention reconnecting", m.status)
	}
	if !m.disconn {
		t.Error("input must stay blocked while reconnecting")
	}
}

// Without a Reconnect hook the disconnect keeps the terminal behavior.
func TestDisconnectWithoutHookStaysDead(t *testing.T) {
	m := newTestModel()

	_, cmd := m.handleEvent(client.Event{Type: client.EventDisconnected})

	if cmd != nil {
		t.Error("no reconnect hook: expected no scheduled command")
	}
	if m.status != "disconnected" {
		t.Errorf("status = %q, want disconnected", m.status)
	}
}

// A successful redial swaps the client, re-arms the event stream, and
// unblocks input.
func TestReconnectSuccess(t *testing.T) {
	m := newTestModel()
	m.disconn = true
	m.status = "reconnecting…"

	ch := make(chan client.Event, 1)
	cl := &client.Client{Events: ch}
	_, cmd := m.Update(reconnectMsg{attempt: 0, cl: cl})

	if m.disconn {
		t.Error("should be connected again")
	}
	if m.cl != cl {
		t.Error("client was not swapped")
	}
	if m.events != ch {
		t.Error("event channel was not swapped")
	}
	if cmd == nil {
		t.Error("event listener was not re-armed")
	}
	found := false
	for _, n := range m.notices {
		if strings.Contains(n, "reconnected") {
			found = true
		}
	}
	if !found {
		t.Error("expected a reconnected note")
	}
}

// Failed redials retry with backoff until the attempt budget is exhausted,
// then settle into the terminal disconnected state.
func TestReconnectRetriesThenGivesUp(t *testing.T) {
	m := newTestModel()
	m.disconn = true
	m.opts.Reconnect = func() (*client.Client, error) { return nil, errors.New("down") }

	_, cmd := m.Update(reconnectMsg{attempt: 0, err: errors.New("down")})
	if cmd == nil {
		t.Fatal("an early failure should schedule the next attempt")
	}

	_, cmd = m.Update(reconnectMsg{attempt: maxReconnectAttempts - 1, err: errors.New("down")})
	if cmd != nil {
		t.Error("no more attempts once the budget is spent")
	}
	if m.status != "disconnected" {
		t.Errorf("status = %q, want disconnected", m.status)
	}
	if !m.disconn {
		t.Error("input must stay blocked after giving up")
	}
}

// A reconnect result arriving after the disconnect was already resolved is
// dropped, not applied.
func TestReconnectStaleResultIgnored(t *testing.T) {
	m := newTestModel() // disconn == false

	_, cmd := m.Update(reconnectMsg{attempt: 0, cl: &client.Client{Events: make(chan client.Event)}})
	if cmd != nil {
		t.Error("stale reconnect result must not re-arm the listener")
	}
	if m.cl != nil {
		t.Error("stale reconnect result must not swap the client")
	}
}

// While a redial is in flight the badge must say so, not "disconnected".
func TestReconnectingBadge(t *testing.T) {
	m := newTestModel()
	m.disconn = true
	m.status = "reconnecting…"

	badge := plain(m.statusBadge())
	if !strings.Contains(badge, "reconnecting") {
		t.Errorf("badge = %q, want it to mention reconnecting", badge)
	}
	if strings.Contains(badge, "disconnected") {
		t.Errorf("badge = %q, must not read disconnected mid-retry", badge)
	}
}

func TestReconnectBackoff(t *testing.T) {
	if got := reconnectBackoff(0); got != 500*time.Millisecond {
		t.Errorf("backoff(0) = %v, want 500ms", got)
	}
	if got := reconnectBackoff(1); got != time.Second {
		t.Errorf("backoff(1) = %v, want 1s", got)
	}
	if got := reconnectBackoff(20); got != 8*time.Second {
		t.Errorf("backoff(20) = %v, want the 8s cap", got)
	}
}
