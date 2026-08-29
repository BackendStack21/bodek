package tui

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// ^Y / /copy put the latest assistant reply on the system clipboard via
// OSC 52 — written straight to the terminal with tea.Exec, because bodek
// runs on the alt-screen where tea.Println output is dropped entirely.

func TestOsc52Sequence(t *testing.T) {
	got := ansi.SetSystemClipboard("hi")
	want := "\x1b]52;c;aGk=\x07"
	if got != want {
		t.Errorf("osc52 sequence = %q, want %q", got, want)
	}
}

func TestClipboardWriteRun(t *testing.T) {
	var buf bytes.Buffer
	c := &clipboardWrite{seq: "SEQ"}
	c.SetStdin(nil)
	c.SetStdout(&buf)
	c.SetStderr(io.Discard)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if buf.String() != "SEQ" {
		t.Errorf("Run wrote %q, want %q", buf.String(), "SEQ")
	}
}

func TestLastReplyPicksLatestFinalized(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "q"},
		message{role: roleAsst, streaming: true},
	)
	if got := m.lastReply(); got != "" {
		t.Errorf("streaming turn not skipped: %q", got)
	}
	m.msgs[len(m.msgs)-1] = message{role: roleAsst, content: "first"}
	m.msgs = append(m.msgs, message{role: roleAsst, content: "second"})
	if got := m.lastReply(); got != "second" {
		t.Errorf("lastReply = %q, want %q", got, "second")
	}
}

func TestCopyLastReplyGuards(t *testing.T) {
	m := newTestModel()

	// Nothing to copy: the guard note must still fire (non-nil cmd).
	if cmd := m.copyLastReply(); cmd == nil {
		t.Error("empty transcript returned nil cmd; want the nothing-to-copy notice")
	}

	// Oversized reply: refuse instead of silently truncating.
	m.msgs = append(m.msgs, message{role: roleAsst, content: strings.Repeat("x", osc52Cap+1)})
	if cmd := m.copyLastReply(); cmd == nil {
		t.Error("oversized reply returned nil cmd; want the refusal notice")
	}
}
