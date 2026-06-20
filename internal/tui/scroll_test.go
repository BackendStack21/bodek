package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestUpDownScrollTranscript verifies that ↑/↓ scroll the conversation when
// the textarea cursor is at the corresponding edge of the input.
func TestUpDownScrollTranscript(t *testing.T) {
	m := newTestModel()

	// Build a tall, markdown-heavy transcript so the viewport can scroll.
	md := "# Heading\n\nThis is **bold** and *italic*.\n\n```go\nfunc main() {}\n```\n\n- one\n- two\n- three\n\n" + strings.Repeat("More text. ", 30)
	for i := 0; i < 4; i++ {
		m.msgs = append(m.msgs, message{role: roleUser, content: "prompt"})
		m.msgs = append(m.msgs, message{role: roleAsst, content: md, rendered: m.render(md)})
	}
	m.refresh()

	if m.vp.TotalLineCount() <= m.vp.Height {
		t.Fatal("test transcript should be taller than the viewport")
	}
	if !m.vp.AtBottom() {
		t.Fatal("transcript should start pinned to the bottom")
	}
	bottom := m.vp.YOffset

	// With an empty single-line input, pressing Up should scroll the viewport.
	m.Update(key("up"))
	if m.vp.YOffset >= bottom {
		t.Errorf("up did not scroll transcript up: yoffset=%d, was=%d", m.vp.YOffset, bottom)
	}

	// And Down should return to the bottom.
	m.Update(key("down"))
	if !m.vp.AtBottom() {
		t.Errorf("down did not return transcript to bottom: yoffset=%d", m.vp.YOffset)
	}
}

// TestUpDownEditMultiLine verifies that ↑/↓ still edit a multi-line input
// when the cursor is not at the corresponding edge.
func TestUpDownEditMultiLine(t *testing.T) {
	m := newTestModel()

	// Two-line input with the cursor on the first line.
	m.ta.Focus()
	m.ta.SetValue("line one\nline two")
	m.ta.CursorStart()
	m.ta.CursorUp()
	if m.ta.Line() != 0 {
		t.Fatalf("expected cursor on line 0, got %d", m.ta.Line())
	}

	// Up from the first line should scroll, but with no transcript there is
	// nowhere to scroll; the viewport stays at top and the key is consumed.
	before := m.vp.YOffset
	m.Update(key("up"))
	if m.vp.YOffset != before {
		t.Errorf("up scrolled an empty transcript unexpectedly")
	}

	// Down from the first line of a two-line input should move the cursor down,
	// not scroll the transcript.
	m.Update(key("down"))
	if m.ta.Line() != 1 {
		t.Errorf("down should move cursor to line 1, got line %d", m.ta.Line())
	}

	// Another down from the last line should scroll (empty transcript: no-op).
	m.Update(key("down"))
	if m.ta.Line() != 1 {
		t.Errorf("down from last line changed cursor unexpectedly to %d", m.ta.Line())
	}
}

// TestMouseWheelScrollsTranscript verifies that wheel events scroll the
// transcript once mouse reporting is enabled by the program.
func TestMouseWheelScrollsTranscript(t *testing.T) {
	m := newTestModel()

	md := "# Section\n\n" + strings.Repeat("Paragraph. ", 40)
	for i := 0; i < 5; i++ {
		m.msgs = append(m.msgs, message{role: roleUser, content: "q"})
		m.msgs = append(m.msgs, message{role: roleAsst, content: md, rendered: m.render(md)})
	}
	m.refresh()

	if !m.vp.AtBottom() {
		t.Fatal("transcript should start at the bottom")
	}
	bottom := m.vp.YOffset

	_, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.vp.YOffset >= bottom {
		t.Errorf("mouse wheel up did not scroll transcript up: yoffset=%d, was=%d", m.vp.YOffset, bottom)
	}

	_, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if !m.vp.AtBottom() {
		t.Errorf("mouse wheel down did not return to bottom: yoffset=%d", m.vp.YOffset)
	}
}
