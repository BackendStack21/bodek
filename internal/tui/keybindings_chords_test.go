package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── enhanced-key chord fixes (plan: .plans/KEY_BINDINGS_FIX_PLAN.md) ────────
// RED-first: ctrl+enter must be distinguishable from plain enter, shifted
// CSI letter chords must reach the composer, and the shift+enter sentinel
// must not leak into the friction confirmation buffer.

// TestFilterRewritesCtrlEnterCSI pins the decode of Ctrl+Enter from kitty
// CSI-u and xterm modifyOtherKeys encodings: today the ctrl modifier is
// discarded and it collapses into plain enter (accidental submit).
func TestFilterRewritesCtrlEnterCSI(t *testing.T) {
	for _, seq := range []string{
		"\x1b[13;5u", "\x1b[13;5;1u", "\x1b[27;5;13~", "\x1b[27;5;13u",
	} {
		msg := FilterShiftEnter(nil, []byte(seq))
		km, ok := msg.(tea.KeyMsg)
		if !ok || km.String() != "ctrl+enter" {
			t.Errorf("FilterShiftEnter(%q) = %#v, want ctrl+enter", seq, msg)
		}
	}
}

// TestCtrlEnterInsertsNewlineNotSubmit: the composer treats ctrl+enter as a
// newline chord like shift+enter — never a submit.
func TestCtrlEnterInsertsNewlineNotSubmit(t *testing.T) {
	m := newTestModel()
	m.handleKey(key("h"))
	m.handleKey(key("i"))
	m.handleKey(key("ctrl+enter"))
	m.handleKey(key("t"))
	if got := m.ta.Value(); got != "hi\nt" {
		t.Fatalf("ctrl+enter value = %q, want %q", got, "hi\nt")
	}
	if len(m.msgs) != 0 {
		t.Fatal("ctrl+enter must not submit the draft")
	}
}

// TestFilterRewritesShiftedLetterCSI: Shift+printable chords (modifyOtherKeys
// level 2 and kitty CSI-u) decode to their uppercase rune instead of being
// silently dropped.
func TestFilterRewritesShiftedLetterCSI(t *testing.T) {
	for _, seq := range []string{"\x1b[97;2u", "\x1b[27;2;97~"} {
		msg := FilterShiftEnter(nil, []byte(seq))
		km, ok := msg.(tea.KeyMsg)
		if !ok || km.Type != tea.KeyRunes || string(km.Runes) != "A" {
			t.Errorf("FilterShiftEnter(%q) = %#v, want runes \"A\"", seq, msg)
		}
	}
}

// TestShiftedLetterCSIReachesComposer: end-to-end — a decoded Shift+a chord
// types into the draft.
func TestShiftedLetterCSIReachesComposer(t *testing.T) {
	m := newTestModel()
	msg := FilterShiftEnter(nil, []byte("\x1b[97;2u"))
	if msg == nil {
		t.Fatal("shift+a CSI dropped before Update")
	}
	m.Update(msg.(tea.KeyMsg))
	if got := m.ta.Value(); got != "A" {
		t.Fatalf("composer value = %q, want %q", got, "A")
	}
}

// TestFrictionShiftEnterNoLeak: the synthesized shift+enter sentinel must not
// append its literal runes into the friction confirmation editor.
func TestFrictionShiftEnterNoLeak(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Friction: true})
	m.Update(key("shift+enter"))
	if got := m.apprTyped; got != "" {
		t.Fatalf("friction buffer = %q, want empty (no shift+enter leak)", got)
	}
	m.Update(key("ctrl+enter"))
	if got := m.apprTyped; got != "" {
		t.Fatalf("friction buffer = %q, want empty (no ctrl+enter leak)", got)
	}
}

// TestPlainEnterStillSubmits: regression guard — plain enter keeps submitting.
func TestPlainEnterStillSubmits(t *testing.T) {
	m := newTestModel()
	m.handleKey(key("h"))
	m.handleKey(key("i"))
	m.handleKey(key("enter"))
	if m.ta.Value() != "" {
		t.Fatal("plain enter must still submit and clear the draft")
	}
	if len(m.msgs) != 2 || m.msgs[0].content != "hi" {
		t.Fatalf("plain enter must submit the draft, got %d msgs", len(m.msgs))
	}
}
