package tui

import (
	"testing"
)

// Regression: clearConversation reset every session-scoped index except
// focusIdx. A transcript that regrows after /clear recycles indexes, and a
// stale focusIdx makes alt+y copy the wrong turn's reply (focusedReply
// bounds-checks, so no panic — just silently wrong output) and feeds
// moveInspect a dead anchor.
func TestClearConversationResetsFocusIdx(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst})
	m.msgs = append(m.msgs, message{role: roleAsst})
	m.focusIdx = 1 // user clicked/focused the second turn

	m.clearConversation()

	if m.focusIdx != -1 {
		t.Errorf("focusIdx = %d after clear, want -1 (stale anchor copies the wrong turn once the transcript regrows)", m.focusIdx)
	}
}
