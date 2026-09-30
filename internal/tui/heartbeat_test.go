package tui

import (
	"testing"
	"time"
)

// A heartbeat fired while the connection is down must not send a ping
// (the dead client would swallow it) and must not stamp the RTT clock —
// a stamp with no possible pong leaves a bogus latency measurement armed.
func TestHeartbeatSkipsWhileDisconnected(t *testing.T) {
	m := newTestModel()
	m.disconn = true
	m.handleHeartbeat()
	if !m.pingSentAt.IsZero() {
		t.Fatalf("pingSentAt stamped while disconnected: %v", m.pingSentAt)
	}
}

// The reconnect swap must drop any outstanding ping stamp from the dead
// socket, so the first pong on the fresh connection cannot pair with a
// send that left before the swap.
func TestReconnectResetsPingSentAt(t *testing.T) {
	m := wired(t) // live stand-in: m.cl is a real connected client
	m.disconn = true
	m.pingSentAt = time.Now()
	m.handleReconnect(reconnectMsg{attempt: 0, cl: m.cl})
	if !m.pingSentAt.IsZero() {
		t.Fatalf("pingSentAt survived the reconnect swap: %v", m.pingSentAt)
	}
}
