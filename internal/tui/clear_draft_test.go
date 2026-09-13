package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── composer clear-draft binding (Ctrl+U, alias Shift+Delete) ──────────────
// Ctrl+U is the readline whole-line kill and reaches every terminal
// identically; Shift+Delete is accepted only where an enhanced-key chord
// decodes (kitty CSI-u / modifyOtherKeys), so degraded terminals degrade to
// plain Delete, never to a wrong whole-draft wipe.

// TestCtrlUClearsDraft: ctrl+u wipes the whole multi-line composer draft and
// leaves history recall intact.
func TestCtrlUClearsDraft(t *testing.T) {
	m := newTestModel()
	m.ta.SetValue("line one\nline two tail")
	_, cmd := m.Update(key("ctrl+u"))
	execAll(cmd)
	if got := m.ta.Value(); got != "" {
		t.Fatalf("ctrl+u left draft %q, want empty", got)
	}
	// History recall still works after a clear (histDraft is untouched).
	m.recordHistory("old prompt")
	m.Update(key("ctrl+p"))
	if got := m.ta.Value(); got != "old prompt" {
		t.Errorf("history recall after ctrl+u clear = %q, want %q", got, "old prompt")
	}
}

// TestCtrlUIgnoredWhileApprovalHead: an approval card captures the keyboard —
// ctrl+u must not wipe the (empty) composer or leak into the card.
func TestCtrlUIgnoredWhileApprovalHead(t *testing.T) {
	m, _, _ := approvalRecorder(t)
	m.approvals = []client.Event{{Type: "approval", Tool: "shell"}}
	m.ta.SetValue("draft under approval")
	m.Update(key("ctrl+u"))
	if got := m.ta.Value(); got != "draft under approval" {
		t.Fatalf("ctrl+u cleared the draft under an approval card: %q", got)
	}
	if m.curApproval() == nil {
		t.Fatal("approval card lost")
	}
}

// TestShiftDeleteAliasClearsDraft: the shift+delete sentinel chord clears the
// draft exactly like ctrl+u.
func TestShiftDeleteAliasClearsDraft(t *testing.T) {
	m := newTestModel()
	m.ta.SetValue("wipe me")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+delete")})
	if got := m.ta.Value(); got != "" {
		t.Fatalf("shift+delete left draft %q, want empty", got)
	}
}

// TestShiftDeleteDecodedFromEnhancedChords: kitty CSI-u (51;2u) and xterm
// modifyOtherKeys (27;2;51~) both decode to the shift+delete sentinel.
func TestShiftDeleteDecodedFromEnhancedChords(t *testing.T) {
	for _, csi := range []string{"\x1b[3;2u", "\x1b[27;2;3~"} {
		msg, ok := parseEnhancedKey(csi)
		if !ok {
			t.Fatalf("%q did not decode", csi)
		}
		if msg.String() != "shift+delete" {
			t.Errorf("%q decoded to %q, want shift+delete", csi, msg.String())
		}
	}
	// Legacy plain-delete CSI 3~ is not an enhanced chord — Bubble Tea maps
	// it natively to plain delete; it must never decode to the sentinel.
	if msg, ok := parseEnhancedKey("\x1b[3~"); ok {
		t.Errorf("plain delete CSI decoded to %q sentinel", msg.String())
	}
	// Kitty encodes the digit '3' as code 51 — shift+3 stays the unmapped
	// sentinel (keyboard-layout glyph), never a whole-draft wipe.
	if msg, ok := parseEnhancedKey("\x1b[51;2u"); ok {
		t.Errorf("shift+3 CSI decoded to %q, want unmapped", msg.String())
	}
}
