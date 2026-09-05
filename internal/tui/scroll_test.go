package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/BackendStack21/bodek/internal/tokens"
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

// TestBusyRefreshKeepsScrollback verifies that a run in progress does not yank
// the reader to the bottom on every refresh: autoscroll only sticks when the
// viewport is already at the bottom.
func TestBusyRefreshKeepsScrollback(t *testing.T) {
	m := newTestModel()

	md := strings.Repeat("transcript line\n", 60)
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "q"},
		message{role: roleAsst, content: md, rendered: m.render(md)},
	)
	m.refresh()
	if !m.vp.AtBottom() {
		t.Fatal("precondition: transcript should start at the bottom")
	}

	// Scroll up, then stream a turn: refreshes must leave the position alone.
	m.vp.ScrollUp(5)
	top := m.vp.YOffset
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.busy = true
	m.runStart = time.Now()
	m.handleEvent(client.Event{Type: "token", Content: "streaming…"})
	m.Update(renderFlushMsg{seq: m.renderSeq}) // deliver the coalesced render
	if m.vp.YOffset != top {
		t.Errorf("busy refresh yanked scrollback: yoffset %d → %d", top, m.vp.YOffset)
	}

	// Once back at the bottom, the reader follows the stream again.
	m.vp.GotoBottom()
	m.handleEvent(client.Event{Type: "token", Content: "more"})
	m.Update(renderFlushMsg{seq: m.renderSeq}) // deliver the coalesced render
	if !m.vp.AtBottom() {
		t.Error("at-bottom reader should follow the stream")
	}
}

// TestWireTurnKeepsScrollback: a server-opened card (wake / remote /
// lazy ensureWireTurn) must not yank a reader who is up in history.
// refresh() already sticks when AtBottom(); beginWireTurn must not
// override that with GotoBottom.
func TestWireTurnKeepsScrollback(t *testing.T) {
	m := newTestModel()
	tallTranscript(m)
	m.vp.GotoTop()
	if m.vp.AtBottom() {
		t.Fatal("precondition: scrolled off the bottom")
	}

	m.handleEvent(client.Event{Type: "turn_started", TurnID: "t_wake", Initiated: "system"})
	if m.vp.AtBottom() {
		t.Error("wire turn yanked scrollback to the bottom")
	}
	if !m.busy || m.cur() < 0 {
		t.Fatal("turn_started should still open the streaming card")
	}
	if foot := plain(m.footer()); !strings.Contains(foot, "new output") {
		t.Errorf("scrollback should advertise new output, footer=%q", foot)
	}

	m.vp.GotoBottom()
	m.handleEvent(client.Event{Type: "done"})
	if !m.vp.AtBottom() {
		t.Error("at-bottom reader should stay pinned after the wire turn")
	}
}

// TestTranscriptPrefixCached verifies the finalized transcript prefix renders
// once and is reused across streaming ticks, and that the cache invalidates on
// finalize, resize, and wholesale transcript replacement (session resume).
func TestTranscriptPrefixCached(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newTestModel()
	m.tokens = tokens.Open()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "q"},
		message{role: roleAsst, content: "answer", streaming: true},
	)
	m.curIdx = 1
	m.busy = true

	m.refresh()
	if m.convCount != 1 || m.convPrefix == "" {
		t.Fatalf("prefix not cached: count=%d", m.convCount)
	}
	prefix := m.convPrefix
	// A streaming tick re-renders only the tail; the prefix is untouched.
	m.handleEvent(client.Event{Type: "token", Content: "…"})
	m.Update(renderFlushMsg{seq: m.renderSeq}) // deliver the coalesced render
	if m.convPrefix != prefix {
		t.Error("streaming tick rebuilt the finalized prefix")
	}

	// Finalizing extends the prefix to cover the whole transcript.
	m.handleEvent(client.Event{Type: "done"})
	if m.convCount != len(m.msgs) {
		t.Errorf("after finalize convCount=%d, want %d", m.convCount, len(m.msgs))
	}
	if !strings.Contains(plain(m.convPrefix), "answer") {
		t.Error("finalized prefix missing the answer")
	}

	// Resize invalidates (the message bars are width-dependent) and rebuilds.
	wide := m.convPrefix
	m.resize(80, 30)
	if m.convPrefix == wide {
		t.Error("resize did not rebuild the prefix at the new width")
	}
	if m.convCount != len(m.msgs) || !strings.Contains(plain(m.convPrefix), "answer") {
		t.Error("prefix not rebuilt after resize")
	}

	// Resuming a session swaps the transcript (same length here, which a pure
	// count check could not catch) — the stale prefix must not be served.
	m.handleSessionDetail(sessionDetailMsg{sess: client.Session{ID: "s2",
		Messages: []client.SessionMessage{
			{Role: "user", Content: "q"},
			{Role: "assistant", Content: "resumed reply"},
		}}})
	if strings.Contains(plain(m.convPrefix), "answer") ||
		!strings.Contains(plain(m.convPrefix), "resumed reply") {
		t.Errorf("stale prefix after resume: %q", plain(m.convPrefix))
	}
}
