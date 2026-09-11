package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── adversarial-review hardening (R1/R2/R3 findings) ────────────────────────

// R1-F2/R1-F4: shift+digit and shift+punctuation chords must NOT decode to
// the unshifted glyph (keyboard-layout-dependent) — they surface as the
// unmapped sentinel; unmodified CSI-u printables pass through as typed text.
func TestShiftDigitBecomesSentinelNotGlyph(t *testing.T) {
	msg := FilterShiftEnter(nil, []byte("\x1b[27;2;51~")) // Shift+3
	if km, ok := msg.(tea.KeyMsg); !ok || km.String() != "alt+unmapped-chord" {
		t.Fatalf("shift+3 CSI = %#v, want alt+unmapped-chord sentinel", msg)
	}
	// Shift+letter still decodes to the uppercase rune.
	if km, ok := FilterShiftEnter(nil, []byte("\x1b[97;2u")).(tea.KeyMsg); !ok || string(km.Runes) != "A" {
		t.Errorf("shift+a CSI decode broken by hardening")
	}
	// Unmodified CSI-u printable passes through as plain text.
	if km, ok := FilterShiftEnter(nil, []byte("\x1b[97u")).(tea.KeyMsg); !ok || string(km.Runes) != "a" {
		t.Errorf("plain CSI-u 'a' = %#v, want runes \"a\"", msg)
	}
}

// R1-F6: kitty key-release reports (event types 2/3) must never decode as a
// fresh keypress — a release of Enter must not submit twice.
func TestKittyReleaseEventsIgnored(t *testing.T) {
	for _, seq := range []string{"\x1b[13;1;2u", "\x1b[13;1;3u", "\x1b[13;5;3u"} {
		if msg := FilterShiftEnter(nil, []byte(seq)); msg != nil {
			if km, ok := msg.(tea.KeyMsg); ok && km.Type == tea.KeyEnter {
				t.Errorf("release event %q decoded as Enter", seq)
			}
		}
	}
	// A press event (type 1) still decodes normally.
	if km, ok := FilterShiftEnter(nil, []byte("\x1b[13;1;1u")).(tea.KeyMsg); !ok || km.Type != tea.KeyEnter {
		t.Error("press event 13;1;1u must still decode as Enter")
	}
}

// R2-F1: the sentinel must never type into any capture surface. It is
// intercepted at the top of handleKey — verify for the AC popup path.
func TestUnmappedChordNeverTypesAnywhere(t *testing.T) {
	m := newTestModel()
	m.ac.open = true // popup capture path
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("unmapped-chord"), Alt: true})
	if got := m.ta.Value(); got != "" {
		t.Fatalf("sentinel typed into composer via AC popup: %q", got)
	}
	// Queue-strip focus must not swallow the note either.
	m2 := newTestModel()
	m2.queue = append(m2.queue, "held")
	unfoldQueue(m2)
	applyQuick(m2, func() tea.Cmd {
		_, c := m2.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("unmapped-chord"), Alt: true})
		return c
	}())
	found := false
	for _, n := range m2.notices {
		if strings.Contains(n, "unmapped") {
			found = true
		}
	}
	if !found {
		t.Error("sentinel note suppressed under queue-strip focus")
	}
}

// R2-F2: a newline chord on the AC popup must insert a newline but NOT
// auto-accept the completion (no expanded completion text splices in) —
// Enter alone accepts. The popup may close naturally when the newline
// ends the completion token, same as typing a space.
func TestACNewlineChordDoesNotAccept(t *testing.T) {
	m := newTestModel()
	m.handleKey(key("@"))
	if !m.ac.open {
		t.Skip("no completion popup in this fixture")
	}
	m.handleKey(key("shift+enter"))
	if got := m.ta.Value(); got != "@\n" {
		t.Fatalf("draft after shift+enter under popup = %q, want %q (no completion text)", got, "@\n")
	}
}

// R3-G5: the ^K gate must also hold while a clarify card is head.
func TestCtrlKGatedDuringClarify(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "clarify_request", ID: "clr", Question: "which one?"})
	if m.clarify == nil {
		t.Fatal("precondition: clarify card must be armed")
	}
	m.Update(key("ctrl+k"))
	if m.pal.open {
		t.Error("ctrl+k opened the palette over a live clarify card")
	}
	m.clarify = nil // answer/expiry is not the point here — just clear the card
	m.Update(key("ctrl+k"))
	if !m.pal.open {
		t.Error("ctrl+k must open once the clarify card is gone")
	}
}

// R3-G4: bare s/x must not answer a suggestion while the queue strip holds
// focus — the strip owns the keyboard until esc/⏎/^Q.
func TestQFocusBlocksPlainSuggestKeys(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested", SkillName: "deploy-helper"})
	m.queue = append(m.queue, "held prompt")
	m.busy = false
	unfoldQueue(m)
	m.Update(key("s"))
	if m.skillSuggest == nil {
		t.Error("bare s answered a suggestion under queue-strip focus")
	}
}
