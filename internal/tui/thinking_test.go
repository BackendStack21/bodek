package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestThinkingCap verifies that the live reasoning excerpt is capped so a long
// thinking stream cannot grow without bound.
func TestThinkingCap(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	// Stream a thinking chunk well over the cap.
	chunk := strings.Repeat("word ", 200)
	m.handleEvent(client.Event{Type: "thinking", Content: chunk})

	if m.thinking.Len() > maxThinkingLen*2 {
		t.Errorf("thinking excerpt grew too large: %d", m.thinking.Len())
	}

	// The visible excerpt should end with the tail of the latest input.
	out := m.thinking.String()
	if !strings.HasSuffix(out, "word ") {
		t.Errorf("thinking excerpt lost the tail: %q", out)
	}

	// A subsequent event should keep replacing/capping from the end.
	m.handleEvent(client.Event{Type: "thinking", Content: "final thought"})
	if !strings.Contains(m.thinking.String(), "final thought") {
		t.Errorf("latest thinking not retained: %q", m.thinking.String())
	}
}
