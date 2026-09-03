package tui

import (
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// The stamped wake session frame can be missed (lost on a reconnect race, a
// future wire quirk) — the wake turn's streamed events then find no open card
// and would drop silently. In bodek every operator turn starts with a local
// send, so idle + incoming stream PROVES a server-initiated turn: the first
// streaming event must open a card from the wire.

// Idle + streaming with no bg_wake seen: a plain remote card opens (a turn
// started from another client on this session), not wake-marked.
func TestIdleStreamingOpensRemoteCard(t *testing.T) {
	m := newTestModel()
	feed := []client.Event{
		{Type: "thinking", Content: "checking job output"},
		{Type: "token", Content: "PR #176 is green"},
		{Type: "tool_call", Name: "delegate_tasks", Data: `{"tasks":[]}`},
	}
	for _, ev := range feed {
		m.handleEvent(ev)
	}
	i := m.cur()
	if i < 0 {
		t.Fatal("streaming events while idle did not open a card — loop dropped")
	}
	msg := m.msgs[i]
	if !msg.streaming {
		t.Error("lazy card is not streaming")
	}
	if msg.systemWake {
		t.Error("no bg_wake seen: card must not carry the wake marker")
	}
	if len(msg.items) == 0 {
		t.Error("reasoning/reply items did not land on the lazy card")
	}
	if len(msg.steps) != 1 || msg.steps[0].name != "delegate_tasks" {
		t.Errorf("tool step did not land: %+v", msg.steps)
	}

	// The turn finalizes like any other.
	m.handleEvent(client.Event{Type: "done"})
	if m.busy {
		t.Error("done after lazy-open left busy set")
	}
	if m.status != "ready" {
		t.Errorf("status = %q after done, want ready", m.status)
	}
	if m.cur() >= 0 {
		t.Error("done after lazy-open left the card open")
	}
}

// bg_wake armed the marker before the stamp was missed: the lazy card keeps
// the systemWake identity and renders as a wake turn, never as user text.
func TestWakeArmedStreamingOpensWakeCard(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "bg_wake"})
	m.handleEvent(client.Event{Type: "thinking", Content: "resuming after job"})
	i := m.cur()
	if i < 0 {
		t.Fatal("wake-armed streaming did not open a card")
	}
	if !m.msgs[i].systemWake {
		t.Error("bg_wake seen: lazy card must carry the systemWake marker")
	}
	if m.msgs[i].role != roleAsst {
		t.Errorf("wake card role = %v, want assistant (never renders as a user message)", m.msgs[i].role)
	}
	m.handleEvent(client.Event{Type: "token", Content: "done"})
	m.handleEvent(client.Event{Type: "done"})
}

// busy-without-card is unreachable through the normal flow (sendPrompt and
// openWakeTurn both set the card atomically; done/error finalize together) —
// if it is ever observed, the state is corrupt and must heal, not deadlock.
func TestCorruptBusyWithoutCardHeals(t *testing.T) {
	m := newTestModel()
	m.busy = true // corrupt: no open card
	m.curIdx = -1
	m.handleEvent(client.Event{Type: "thinking", Content: "wake stream"})
	if i := m.cur(); i < 0 {
		t.Fatal("corrupt busy state deadlocked the card open")
	}
	if !m.busy {
		t.Error("healed turn must be busy again while streaming")
	}
	m.handleEvent(client.Event{Type: "done"})
	if m.busy || m.status != "ready" {
		t.Errorf("post-heal done: busy=%v status=%q", m.busy, m.status)
	}
}

// A live operator turn keeps absorbing the stream — lazy-open must never
// stack a second card on top of it (belt for the stamped-frame guard).
func TestLazyOpenNeverStacksOnOperatorTurn(t *testing.T) {
	m := newTestModel()
	m.sendPrompt("what time is it?")
	before := len(m.msgs)
	m.handleEvent(client.Event{Type: "thinking", Content: "thinking"})
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"cmd":"date"}`})
	if len(m.msgs) != before {
		t.Errorf("lazy-open stacked a card on a live operator turn: %d -> %d", before, len(m.msgs))
	}
	if i := m.cur(); i < 0 {
		t.Fatal("operator card lost")
	}
	if n := len(m.msgs[m.cur()].steps); n != 1 {
		t.Errorf("operator turn steps = %d, want 1", n)
	}
}

// wakeArmed retires with the turn: a later idle stream is not mislabelled as
// a wake, and a fresh bg_wake re-arms it.
func TestWakeArmedRetiresAndRearms(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "bg_wake"})
	m.handleEvent(client.Event{Type: "thinking", Content: "wake turn"})
	m.handleEvent(client.Event{Type: "done"})
	m.handleEvent(client.Event{Type: "thinking", Content: "unrelated remote turn"})
	if i := m.cur(); i < 0 {
		t.Fatal("second stream did not open a card")
	} else if m.msgs[i].systemWake {
		t.Error("wakeArmed leaked past turn end — card mislabelled as wake")
	}
	m.handleEvent(client.Event{Type: "done"})
	m.handleEvent(client.Event{Type: "bg_wake"})
	m.handleEvent(client.Event{Type: "thinking", Content: "second wake"})
	if i := m.cur(); i < 0 {
		t.Fatal("re-armed stream did not open a card")
	} else if !m.msgs[i].systemWake {
		t.Error("fresh bg_wake did not re-arm the wake marker")
	}
}
