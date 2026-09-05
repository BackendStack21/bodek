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

// Without a Reconnect hook the disconnect stays dead — no reconnect is
// scheduled, only the (expiring) notes and their sweep ride along.
func TestDisconnectWithoutHookStaysDead(t *testing.T) {
	m := newTestModel()

	_, cmd := m.handleEvent(client.Event{Type: client.EventDisconnected})

	if cmd == nil {
		t.Error("the disconnect notes should arm their expiry sweep")
	}
	if !m.disconn {
		t.Error("input must stay blocked — no reconnect hook means the drop is final")
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

// A hook that returns (nil, nil) is a failed redial — success requires a
// live client. The give-up path used to deref msg.err unconditionally and
// panic when the hook reported neither a client nor an error.
func TestReconnectNilClientNoPanic(t *testing.T) {
	m := newTestModel()
	m.disconn = true
	m.opts.Reconnect = func() (*client.Client, error) { return nil, nil }

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("reconnect (nil, nil) panicked: %v", r)
		}
	}()
	_, cmd := m.Update(reconnectMsg{attempt: maxReconnectAttempts - 1})
	if cmd != nil {
		t.Error("no more attempts once the budget is spent")
	}
	if m.status != "disconnected" {
		t.Errorf("status = %q, want disconnected", m.status)
	}
	if !m.disconn {
		t.Error("input must stay blocked after giving up")
	}
	found := false
	for _, n := range m.notices {
		if strings.Contains(n, "reconnect failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a reconnect-failed note, got %v", m.notices)
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
	if badge != lampReconnect {
		t.Errorf("badge = %q, want %q", badge, lampReconnect)
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

// The scheduled redial cmd runs the hook after its backoff tick and yields a
// reconnectMsg with the outcome.
func TestScheduleReconnectTick(t *testing.T) {
	m := newTestModel()
	called := false
	m.opts.Reconnect = func() (*client.Client, error) { called = true; return nil, errors.New("down") }

	msg := exec(m.scheduleReconnect(0)) // blocks for the 500ms attempt-0 backoff
	rm, ok := msg.(reconnectMsg)
	if !ok {
		t.Fatalf("tick yielded %T, want reconnectMsg", msg)
	}
	if !called || rm.err == nil {
		t.Errorf("hook did not run: called=%v, err=%v", called, rm.err)
	}
}

// A disconnect mid-turn closes the turn out with an interrupted marker
// instead of leaving it streaming forever; a repeat disconnect is a no-op.
func TestDisconnectFinalizesTurn(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.msgs[1].content = "partial answer"

	m.handleEvent(client.Event{Type: client.EventDisconnected})
	msg := m.msgs[1]
	if msg.streaming {
		t.Error("disconnect should finalize the in-flight turn")
	}
	if m.curIdx != -1 {
		t.Error("disconnect should close the open turn index")
	}
	if !strings.Contains(msg.content, "partial answer") || !strings.Contains(msg.content, "**Interrupted:**") {
		t.Errorf("interrupted marker missing: %q", msg.content)
	}

	// Idempotent: a second disconnect must not corrupt the finalized turn.
	m.handleEvent(client.Event{Type: client.EventDisconnected})
	if strings.Count(m.msgs[1].content, "Interrupted") != 1 {
		t.Errorf("repeat disconnect corrupted the turn: %q", m.msgs[1].content)
	}

	// A turn that never streamed anything: the marker is the whole content.
	m2 := newTestModel()
	busyTurn(m2)
	m2.handleEvent(client.Event{Type: client.EventDisconnected})
	if got := m2.msgs[1].content; got != "**Interrupted:** connection lost" {
		t.Errorf("empty turn marker = %q", got)
	}
}

// Approvals die with the socket — the same contract /new already documents.
// Leaving the queue armed after a drop captures the keyboard (and the
// footer) so ⏎ retry never runs.
func TestDisconnectClearsStaleApprovals(t *testing.T) {
	m := newTestModel()
	m.opts.Reconnect = func() (*client.Client, error) { return nil, errors.New("down") }
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1",
		Risk: "shell_exec", Command: "rm x"})
	if m.curApproval() == nil {
		t.Fatal("precondition: approval_request must arm the queue")
	}

	m.handleEvent(client.Event{Type: client.EventDisconnected})

	if m.curApproval() != nil {
		t.Fatal("disconnect must drop stale approvals")
	}
	if len(m.apprDeadlines) != 0 {
		t.Fatalf("disconnect left %d approval deadlines", len(m.apprDeadlines))
	}
	foot := plain(m.footer())
	if strings.Contains(foot, "pprove") || strings.Contains(foot, "eny") {
		t.Errorf("footer still shows approval hints after disconnect: %q", foot)
	}
	if !strings.Contains(foot, "retry") {
		t.Errorf("footer missing ⏎ retry after disconnect: %q", foot)
	}
}
