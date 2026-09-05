package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── transcript: user prompts must wrap, never truncate ──────────────────────
// The composer and the transcript must show the same text: clampLines
// hard-truncates over-wide rows, so an unwrapped long prompt silently loses
// its tail in the transcript view.

func TestUserPromptWrapsInTranscript(t *testing.T) {
	m := newTestModel()
	long := strings.Repeat("word ", 30) + "tail-marker-42"
	m.msgs = []message{{role: roleUser, content: long}}

	out, _ := m.renderMessage(m.msgs[0], 0, 0)
	for _, ln := range strings.Split(plain(out), "\n") {
		if w := lipgloss.Width(ln); w > m.vp.Width {
			t.Fatalf("renderMessage emitted a %d-cell line (viewport %d): %q", w, m.vp.Width, ln)
		}
	}

	// The user-visible bug: conversation() clamps per line, so the tail of an
	// unwrapped prompt disappears from the transcript entirely.
	if conv := plain(m.conversation()); !strings.Contains(conv, "tail-marker-42") {
		t.Fatal("long user prompt tail truncated in transcript view")
	}
}

func TestUserPromptWrapsUnbrokenToken(t *testing.T) {
	m := newTestModel()
	m.msgs = []message{{role: roleUser, content: strings.Repeat("x", 300)}}

	out, _ := m.renderMessage(m.msgs[0], 0, 0)
	for _, ln := range strings.Split(plain(out), "\n") {
		if w := lipgloss.Width(ln); w > m.vp.Width {
			t.Fatalf("unbroken token emitted a %d-cell line (viewport %d)", w, m.vp.Width)
		}
	}
}

// ── composer: the input box must auto-fit its content ───────────────────────
// Constants pinned as literals on purpose: the box rests at 3 rows and grows
// to at most 12, and the tests must catch a silent constant change.

func TestShiftEnterInsertsNewline(t *testing.T) {
	m := newTestModel()
	m.handleKey(key("h"))
	m.handleKey(key("i"))
	m.handleKey(key("shift+enter"))
	m.handleKey(key("t"))
	if got := m.ta.Value(); got != "hi\nt" {
		t.Fatalf("shift+enter value = %q, want %q", got, "hi\nt")
	}
	m.handleKey(key("alt+enter"))
	if got := m.ta.Value(); got != "hi\nt\n" {
		t.Fatalf("alt+enter value = %q, want %q", got, "hi\nt\n")
	}
}

func TestFilterShiftEnterRewritesCSI(t *testing.T) {
	for _, seq := range []string{"\x1b[13;2u", "\x1b[13;2;1u", "\x1b[27;2;13~"} {
		msg := FilterShiftEnter(nil, []byte(seq))
		km, ok := msg.(tea.KeyMsg)
		if !ok || km.String() != "shift+enter" {
			t.Errorf("FilterShiftEnter(%q) = %#v, want shift+enter", seq, msg)
		}
	}
	if msg := FilterShiftEnter(nil, key("enter")); msg.(tea.KeyMsg).String() != "enter" {
		t.Error("FilterShiftEnter must leave enter alone")
	}
}

func TestComposerGrowsWithNewlines(t *testing.T) {
	m := newTestModel()
	if m.ta.Height() != 3 {
		t.Fatalf("initial composer height = %d, want 3", m.ta.Height())
	}
	for i := 0; i < 6; i++ {
		m.handleKey(key("ctrl+j"))
	}
	if m.ta.Height() != 7 {
		t.Fatalf("composer height after 6 newlines = %d, want 7", m.ta.Height())
	}
	// The layout must give the grown box its rows: 30 - header(2) - footer(1)
	// - input area (7 text rows + 2 border rows) = 18.
	if m.vp.Height != 18 {
		t.Fatalf("viewport height after growth = %d, want 18", m.vp.Height)
	}
	if m.inputAreaHeight() != m.ta.Height()+2 {
		t.Fatalf("inputAreaHeight = %d, want %d (box + border)", m.inputAreaHeight(), m.ta.Height()+2)
	}
}

func TestComposerGrowsForLongLine(t *testing.T) {
	m := newTestModel()
	// 300 cells in a 96-cell box → ceil(300/96) = 4 wrapped rows.
	m.ta.SetValue(strings.Repeat("word ", 60))
	m.handleKey(key("x"))
	if m.ta.Height() != 4 {
		t.Fatalf("composer height for one 300-cell line = %d, want 4", m.ta.Height())
	}
}

func TestComposerGrowthCaps(t *testing.T) {
	m := newTestModel()
	m.ta.SetValue(strings.Repeat("l\n", 60))
	m.handleKey(key("x"))
	if m.ta.Height() != 12 {
		t.Fatalf("capped composer height = %d, want 12", m.ta.Height())
	}
}

func TestComposerClampedByTerminalRoom(t *testing.T) {
	m := newTestModel()
	m.resize(100, 12) // 12 rows: 12 - header(2) - footer(1) - margin(6) = 3
	m.ta.SetValue(strings.Repeat("l\n", 60))
	m.handleKey(key("x"))
	if m.ta.Height() != 3 {
		t.Fatalf("composer height in a 12-row terminal = %d, want 3", m.ta.Height())
	}
	if m.inputAreaHeight() != 5 {
		t.Fatalf("inputAreaHeight in a 12-row terminal = %d, want 5", m.inputAreaHeight())
	}
}

func TestComposerShrinksOnHistoryRecall(t *testing.T) {
	m := newTestModel()
	m.ta.SetValue(strings.Repeat("l\n", 10))
	m.handleKey(key("x"))
	if m.ta.Height() != 11 {
		t.Fatalf("precondition: composer height = %d, want 11", m.ta.Height())
	}
	m.history = []string{"short prompt"}
	m.historyPrev()
	if m.ta.Height() != 3 {
		t.Fatalf("composer height after history recall = %d, want 3", m.ta.Height())
	}
}

func TestComposerResetsAfterQueueSubmit(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m.ta.SetValue(strings.Repeat("l\n", 10))
	m.handleKey(key("x"))
	if m.ta.Height() != 11 {
		t.Fatalf("precondition: composer height = %d, want 11", m.ta.Height())
	}
	m.submit()
	if m.ta.Height() != 3 {
		t.Fatalf("composer height after queued submit = %d, want 3", m.ta.Height())
	}
	if m.ta.Value() != "" {
		t.Fatalf("composer not cleared after queued submit: %q", m.ta.Value())
	}
}
