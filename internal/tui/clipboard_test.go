package tui

import (
	"bytes"
	"errors"
	"io"
	osexec "os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// ^Y / /copy put the latest assistant reply on the system clipboard via
// OSC 52 — written straight to the terminal with tea.Exec, because bodek
// runs on the alt-screen where tea.Println output is dropped entirely.

func TestAfterExecRestoresMouse(t *testing.T) {
	if _, ok := afterExec(nil).(restoreAfterExecMsg); !ok {
		t.Fatal("afterExec must return restoreAfterExecMsg")
	}
	m := newTestModel()
	_, cmd := m.Update(restoreAfterExecMsg{})
	if cmd == nil {
		t.Fatal("alt-screen restore must re-enable mouse cell motion")
	}
	m.plain = true
	_, cmd = m.Update(restoreAfterExecMsg{})
	if cmd != nil {
		t.Fatal("plain mode has no mouse reporting to restore")
	}
}

func TestOsc52Sequence(t *testing.T) {
	got := ansi.SetSystemClipboard("hi")
	want := "\x1b]52;c;aGk=\x07"
	if got != want {
		t.Errorf("osc52 sequence = %q, want %q", got, want)
	}
}

func TestClipboardWriteRun(t *testing.T) {
	var buf bytes.Buffer
	c := &rawSeq{seq: "SEQ"}
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

func TestCopyViaHelperStaysOnAltScreen(t *testing.T) {
	// cat is a stand-in for pbcopy: it consumes stdin and exits 0 without
	// needing a TTY. The helper must be a plain tea.Cmd — calling it
	// returns copyResultMsg directly. tea.ExecProcess would not.
	if _, err := osexec.LookPath("cat"); err != nil {
		t.Skip("cat not on PATH")
	}
	m := newTestModel()
	cmd := m.copyViaExec("cat", "hello")
	if cmd == nil {
		t.Fatal("copyViaExec returned nil")
	}
	msg := cmd()
	got, ok := msg.(copyResultMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want copyResultMsg (tea.Exec would not yield this)", msg)
	}
	if got.err != nil {
		t.Fatalf("cat helper: %v", got.err)
	}
	_, next := m.Update(got)
	if next != nil {
		t.Fatal("successful helper copy must not restoreAfterExec — that flickers the alt-screen")
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

// errWriter always fails, to exercise Run's error propagation.
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("boom") }

func TestClipboardWriteRunner(t *testing.T) {
	c := &rawSeq{seq: "\x1b]52;c;aGk=\x07"}

	// Headless: no writer wired — the setters are no-ops and Run is silent.
	c.SetStdin(nil)
	c.SetStderr(io.Discard)
	if err := c.Run(); err != nil {
		t.Errorf("Run with no writer = %v, want nil", err)
	}

	// Wired: the sequence lands verbatim on the terminal writer.
	var buf bytes.Buffer
	c.SetStdout(&buf)
	if err := c.Run(); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if buf.String() != c.seq {
		t.Errorf("Run wrote %q, want %q", buf.String(), c.seq)
	}

	// A failing writer must surface its error, not swallow it.
	c.SetStdout(errWriter{})
	if err := c.Run(); err == nil {
		t.Error("Run swallowed the writer error")
	}
}
