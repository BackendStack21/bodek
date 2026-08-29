package tui

import (
	"strings"
	"testing"
)

// ^L and /clear are destructive at conversation scope — the whole transcript
// plus every session counter. They must arm the same two-step confirm the
// panel row deletes use (y fires, any other key disarms) instead of wiping
// on a single keypress, and they must stay idle-only like ^L always was.

func seedConversation(m *Model) {
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "hello"},
		message{role: roleAsst, content: "world"},
	)
	m.toolTotal = 3
	m.sessCtxTok = 100
}

func TestClearArmsConfirm(t *testing.T) {
	m := newTestModel()
	seedConversation(m)

	m.Update(key("ctrl+l"))

	if m.confirm != confirmClear {
		t.Fatalf("ctrl+l did not arm confirmClear: %v", m.confirm)
	}
	if len(m.msgs) != 2 {
		t.Fatalf("armed ^L already wiped the transcript: %d msgs", len(m.msgs))
	}
	if got := plain(m.View()); !strings.Contains(got, "clear the conversation?") {
		t.Errorf("footer does not show the clear confirm gate:\n%s", got)
	}

	m.Update(key("y"))
	if m.confirm != confirmNone {
		t.Error("y did not disarm the confirm")
	}
	if len(m.msgs) != 0 || m.toolTotal != 0 || m.sessCtxTok != 0 {
		t.Errorf("y did not clear: msgs=%d tools=%d ctx=%d",
			len(m.msgs), m.toolTotal, m.sessCtxTok)
	}
}

func TestClearConfirmDisarmsOnOtherKey(t *testing.T) {
	m := newTestModel()
	seedConversation(m)

	m.Update(key("ctrl+l"))
	if m.confirm != confirmClear {
		t.Fatalf("ctrl+l did not arm confirmClear: %v", m.confirm)
	}

	m.Update(key("esc"))
	if m.confirm != confirmNone {
		t.Error("esc did not disarm the confirm")
	}
	if len(m.msgs) != 2 {
		t.Errorf("disarm lost the transcript: %d msgs", len(m.msgs))
	}

	// After disarming, printable keys type again — they never clear.
	m.Update(key("x"))
	if len(m.msgs) != 2 {
		t.Errorf("rune keypress cleared the transcript: %d msgs", len(m.msgs))
	}
}

func TestClearIgnoredWhileBusy(t *testing.T) {
	m := newTestModel()
	seedConversation(m)
	m.busy = true

	m.Update(key("ctrl+l"))

	if m.confirm != confirmNone {
		t.Error("^L armed a confirm mid-turn")
	}
	if len(m.msgs) != 2 {
		t.Errorf("^L cleared mid-turn: %d msgs", len(m.msgs))
	}
}

func TestSlashClearArmsConfirm(t *testing.T) {
	m := newTestModel()
	seedConversation(m)

	m.Update(key("/"))
	m.ta.SetValue("/clear")
	m.Update(key("enter"))

	if m.confirm != confirmClear {
		t.Fatalf("/clear did not arm confirmClear: %v", m.confirm)
	}
	if len(m.msgs) != 2 {
		t.Errorf("armed /clear already wiped the transcript: %d msgs", len(m.msgs))
	}

	m.Update(key("y"))
	if len(m.msgs) != 0 {
		t.Errorf("y did not clear after /clear arm: %d msgs", len(m.msgs))
	}
}

func TestSlashClearRefusedWhileBusy(t *testing.T) {
	m := newTestModel()
	seedConversation(m)
	m.busy = true

	m.Update(key("/"))
	m.ta.SetValue("/clear")
	m.Update(key("enter"))

	if m.confirm != confirmNone {
		t.Error("/clear armed a confirm mid-turn")
	}
	if len(m.msgs) != 2 {
		t.Errorf("/clear cleared mid-turn: %d msgs", len(m.msgs))
	}
}
