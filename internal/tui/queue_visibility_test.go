package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestQueuedCountOnStatusLine verifies the in-flight status row — the line
// the eyes are on while a turn runs — carries the queue depth, and that the
// count clears once the turn ends and the queue drains into the next turn.
func TestQueuedCountOnStatusLine(t *testing.T) {
	m := newTestModel()
	busyTurn(m)

	for _, p := range []string{"first follow-up", "second follow-up"} {
		m.ta.SetValue(p)
		m.submit()
	}
	if line := plain(m.statusLine()); !strings.Contains(line, "2 queued") {
		t.Errorf("status line missing queued count: %q", line)
	}
	if foot := plain(m.footer()); !strings.Contains(foot, "2 queued") {
		t.Errorf("footer missing queued count: %q", foot)
	}

	// Each turn-end drains exactly one queued prompt: the count steps down
	// until the queue is empty, then disappears from the row.
	m.handleEvent(client.Event{Type: "done", Latency: 1})
	if len(m.queue) != 1 {
		t.Fatalf("one done should drain one prompt, queue = %v", m.queue)
	}
	if line := plain(m.statusLine()); !strings.Contains(line, "1 queued") {
		t.Errorf("status line should show the remaining prompt: %q", line)
	}
	m.handleEvent(client.Event{Type: "done", Latency: 1})
	if len(m.queue) != 0 {
		t.Fatalf("queue should be empty now, got %v", m.queue)
	}
	if line := plain(m.statusLine()); strings.Contains(line, "queued") {
		t.Errorf("status line still shows a queue after the drain: %q", line)
	}
}

// TestQueuedPromptAcknowledged verifies a mid-turn ⏎ tells the user the
// prompt was held — the input clearing with zero feedback reads as a lost
// message (same rationale as the disconnected-draft warning and the
// retry-queued note).
func TestQueuedPromptAcknowledged(t *testing.T) {
	m := newTestModel()
	busyTurn(m)

	m.ta.SetValue("follow up")
	if cmd := m.submit(); cmd == nil {
		t.Error("queueing should return the notice-sweep cmd so the note can fade")
	}
	if n := len(m.notices); n == 0 {
		t.Fatal("no acknowledgment note after queueing a prompt mid-turn")
	}
	if got := m.notices[len(m.notices)-1]; !strings.Contains(got, "queued") {
		t.Errorf("acknowledgment note = %q, want it to mention the queue", got)
	}
	// The acknowledgment is not a dispatch: nothing was sent.
	if m.lastPrompt == "follow up" {
		t.Error("queueing must not dispatch the prompt")
	}
}
