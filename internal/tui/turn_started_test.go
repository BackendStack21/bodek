package tui

import (
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// odek ≥ the turn_started protocol announces EVERY turn right after the
// session frame: wake turns carry initiated=system, operator turns
// operator. The frame becomes the primary card-opening signal — the
// stamped session frame and the lazy ensureWireTurn fallback stay as
// belt-and-suspenders.

// A wake announcement opens the wake-marked streaming card from the wire.
func TestTurnStartedSystemOpensWakeCard(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "turn_started", TurnID: "t_ab12", SessionID: "s1", Initiated: "system", Model: "glm"})

	i := m.cur()
	if i < 0 {
		t.Fatal("turn_started(system) did not open a streaming card")
	}
	if !m.msgs[i].systemWake {
		t.Error("system turn not marked systemWake")
	}
	if !m.busy {
		t.Error("turn_started(system) did not set busy")
	}
	m.handleEvent(client.Event{Type: "thinking", Content: "on the job"})
	if i := m.cur(); i < 0 || len(m.msgs[i].items) == 0 {
		t.Fatal("streamed reasoning did not land on the turn_started card")
	}
}

// A foreign operator turn (prompted from another client on this session)
// opens a plain remote card — visible, never wake-marked.
func TestTurnStartedOperatorOpensPlainRemoteCard(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "turn_started", TurnID: "t_cd34", SessionID: "s1", Initiated: "operator", Model: "glm"})

	i := m.cur()
	if i < 0 {
		t.Fatal("turn_started(operator) while idle did not open a card")
	}
	if m.msgs[i].systemWake {
		t.Error("operator turn mislabelled as wake")
	}
	if !m.busy {
		t.Error("turn_started(operator) did not set busy")
	}
}

// R2 idempotency: a replayed turn_started must not stack a second card.
func TestTurnStartedIdempotent(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "turn_started", TurnID: "t_ab12", Initiated: "system"})
	m.handleEvent(client.Event{Type: "turn_started", TurnID: "t_ab12", Initiated: "system"})
	if n := len(m.msgs); n != 1 {
		t.Fatalf("replayed turn_started stacked cards: len(msgs) = %d", n)
	}
	if m.cur() != 0 {
		t.Fatalf("cur = %d, want 0", m.cur())
	}
}

// bodek's own send path already opened the card before the frame arrives —
// the announcement must be a no-op there.
func TestTurnStartedSuppressedOnOwnTurn(t *testing.T) {
	m := newTestModel()
	m.sendPrompt("what time is it?")
	before := len(m.msgs)
	m.handleEvent(client.Event{Type: "turn_started", TurnID: "t_ef56", Initiated: "operator"})
	if len(m.msgs) != before {
		t.Fatalf("own turn's turn_started stacked a card: %d -> %d", before, len(m.msgs))
	}
}

// A wake announcement during a live operator turn opens nothing — odek
// wakes only idle connections, and the client-side guard keeps the
// transcript honest under any interleaving.
func TestTurnStartedSuppressedWhileBusy(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "turn_started", TurnID: "t_gh78", Initiated: "system"})
	if len(m.msgs) != 1 {
		t.Errorf("turn_started while busy opened a card: len(msgs) = %d", len(m.msgs))
	}
}
