package tui

import "testing"

// A queue drain sends the queued prompt without wiping what the user is
// typing meanwhile (sendPrompt used to reset the composer on every path).
func TestRegressQueueDrainKeepsDraft(t *testing.T) {
	m := newTestModel()
	m.queue = []string{"queued B"}
	m.ta.SetValue("half typed follow-up")
	if cmd := m.sendQueued(); cmd == nil {
		t.Fatal("queued prompt was not sent")
	}
	if got := m.ta.Value(); got != "half typed follow-up" {
		t.Fatalf("draft = %q after drain", got)
	}
	if m.lastPrompt != "queued B" {
		t.Fatalf("lastPrompt = %q", m.lastPrompt)
	}
}
