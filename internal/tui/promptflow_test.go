package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// busyTurn puts the model mid-turn with an open streaming assistant message.
func busyTurn(m *Model) {
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "first"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true
}

// TestSubmitWhileBusyQueues verifies that ⏎ mid-turn queues the prompt
// instead of silently dropping it, and that the footer says so.
func TestSubmitWhileBusyQueues(t *testing.T) {
	m := newTestModel()
	busyTurn(m)

	m.ta.SetValue("follow up")
	if cmd := m.submit(); cmd != nil {
		t.Error("queueing a prompt should not send anything yet")
	}
	if len(m.queue) != 1 || m.queue[0] != "follow up" {
		t.Fatalf("queue = %v, want [follow up]", m.queue)
	}
	if m.ta.Value() != "" {
		t.Error("input should reset after queueing")
	}
	if len(m.msgs) != 2 {
		t.Error("queued prompt must not enter the transcript before it is sent")
	}
	if foot := plain(m.footer()); !strings.Contains(foot, "1 queued") {
		t.Errorf("footer missing queued indicator: %q", foot)
	}
}

// TestQueuedPromptSendsOnDone verifies the queue drains automatically when
// the running turn ends.
func TestQueuedPromptSendsOnDone(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.ta.SetValue("follow up")
	m.submit()

	_, cmd := m.handleEvent(client.Event{Type: "done", Latency: 1})
	if cmd == nil {
		t.Fatal("done should drain the queued prompt")
	}
	// The returned cmd wraps SendPrompt; newTestModel has no client, so it is
	// deliberately not executed — state assertions suffice.
	if len(m.queue) != 0 {
		t.Errorf("queue not drained: %v", m.queue)
	}
	if !m.busy {
		t.Error("model should be busy again with the queued turn")
	}
	if len(m.msgs) != 4 || m.msgs[2].content != "follow up" {
		t.Fatalf("queued turn not appended: %+v", m.msgs)
	}
}

// TestCancelRestoresQueue verifies that esc hands queued prompts back to the
// input instead of firing them into a cancelled session.
func TestCancelRestoresQueue(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.sessionID = "s1"
	m.queue = []string{"one", "two"}

	m.Update(key("esc"))
	if len(m.queue) != 0 {
		t.Errorf("queue should be handed back, got %v", m.queue)
	}
	if got := m.ta.Value(); got != "one\ntwo" {
		t.Errorf("input = %q, want queued drafts restored", got)
	}

	// The done that follows the cancel must not fire anything.
	m.handleEvent(client.Event{Type: "done", Latency: 1})
	if len(m.msgs) != 2 {
		t.Error("no turn should start after cancel restored the queue")
	}
}
