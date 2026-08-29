package tui

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/BackendStack21/bodek/internal/client"
)

// The attention layer keeps backgrounded panes truthful: when a turn
// completes or an approval starts waiting, bodek sets the terminal window
// title (silent, always on), rings the terminal bell (default on,
// --bel=false mutes it), and optionally raises a desktop notification via
// OSC 9 (--notify). All effects ride a single raw write through tea.Exec
// so the sequence lands verbatim and frames can't interleave — the same
// escape hatch the OSC 52 clipboard write uses.

func TestAttentionPlanApproval(t *testing.T) {
	m := newTestModel()
	m.model = "deepseek-v4"
	m.bell = true
	m.notify = true

	a := m.attentionFor(attentionApproval)
	if !strings.HasPrefix(a.title, "⚠ approval needed — ") {
		t.Errorf("approval title = %q, want the ⚠ approval-needed prefix", a.title)
	}
	if !strings.Contains(a.title, "deepseek-v4") {
		t.Errorf("approval title %q missing the model name", a.title)
	}
	if !a.bell {
		t.Error("approval plan must ring the bell when enabled")
	}
	if a.notify == "" {
		t.Error("approval plan must carry a desktop notification when --notify is on")
	}
}

func TestAttentionPlanDone(t *testing.T) {
	m := newTestModel()
	m.model = "deepseek-v4"
	m.bell = true
	m.notify = true

	a := m.attentionFor(attentionDone)
	if !strings.HasPrefix(a.title, "✓ done — ") {
		t.Errorf("done title = %q, want the ✓ done prefix", a.title)
	}
	if !strings.Contains(a.title, "deepseek-v4") {
		t.Errorf("done title %q missing the model name", a.title)
	}
	if !a.bell || a.notify == "" {
		t.Errorf("done plan = %+v, want bell and notify when enabled", a)
	}
}

func TestAttentionPlanMuted(t *testing.T) {
	m := newTestModel()
	m.model = "m"
	m.bell = false
	m.notify = false

	for _, kind := range []attentionKind{attentionApproval, attentionDone} {
		a := m.attentionFor(kind)
		if a.bell {
			t.Errorf("kind %d: bell fired while muted", kind)
		}
		if a.notify != "" {
			t.Errorf("kind %d: notification fired while disabled", kind)
		}
		if a.title == "" {
			// The title is the silent half of the signal: it stays on even
			// with the bell muted, so tmux window lists remain truthful.
			t.Errorf("kind %d: muted plan still carries a title", kind)
		}
	}
}

func TestAttentionPlanEmptyModel(t *testing.T) {
	// No model name known yet (pre-session): the title must stand alone
	// instead of trailing a dangling separator.
	m := newTestModel()
	m.bell = false

	a := m.attentionFor(attentionDone)
	if a.title != "✓ done" {
		t.Errorf("done title with no model = %q, want %q", a.title, "✓ done")
	}
}

func TestAttentionPlanSanitizesModelName(t *testing.T) {
	// The model name arrives from the wire: control bytes must not survive
	// into terminal escape sequences (title or OSC 9 payload).
	m := newTestModel()
	m.model = "evil\x1b]0;pwned\x07"
	m.bell = true
	m.notify = true

	a := m.attentionFor(attentionApproval)
	if strings.ContainsAny(a.title, "\x1b\x07\n\t") {
		t.Errorf("title carried control bytes from the wire: %q", a.title)
	}
	if strings.ContainsAny(a.notify, "\x1b\x07\n\t") {
		t.Errorf("notify text carried control bytes from the wire: %q", a.notify)
	}
}

func TestAttentionSequence(t *testing.T) {
	// One raw write: title first, notification second, bell last.
	got := (attention{title: "T", notify: "N", bell: true}).sequence()
	want := ansi.SetWindowTitle("T") + "\x1b]9;N\x07" + "\a"
	if got != want {
		t.Errorf("sequence = %q, want %q", got, want)
	}

	titleOnly := (attention{title: "T"}).sequence()
	if titleOnly != ansi.SetWindowTitle("T") {
		t.Errorf("title-only sequence = %q, want %q", titleOnly, ansi.SetWindowTitle("T"))
	}
}

func TestAttentionEmptyPlanIsSilent(t *testing.T) {
	a := attention{}
	if !a.empty() {
		t.Error("zero plan must report empty")
	}
	if seq := a.sequence(); seq != "" {
		t.Errorf("empty plan produced sequence %q", seq)
	}
	if cmd := newTestModel().attentionCmd(a); cmd != nil {
		t.Error("empty plan returned a non-nil cmd")
	}
}

func TestAttentionCmdWrapsTheRawWrite(t *testing.T) {
	m := newTestModel()
	if cmd := m.attentionCmd(attention{title: "T", bell: true}); cmd == nil {
		t.Fatal("enabled plan returned a nil cmd")
	}
	if cmd := m.attentionCmd(attention{bell: true}); cmd == nil {
		t.Error("bell-only plan returned a nil cmd")
	}
}

func TestRawSeqRunWritesVerbatim(t *testing.T) {
	// The attention write shares the clipboard's raw-seq escape hatch; the
	// writer must land the sequence verbatim and stay silent headless.
	var buf bytes.Buffer
	w := &rawSeq{seq: "SEQ"}
	w.SetStdin(nil)
	w.SetStderr(io.Discard)
	w.SetStdout(&buf)
	if err := w.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if buf.String() != "SEQ" {
		t.Errorf("Run wrote %q, want %q", buf.String(), "SEQ")
	}

	w.SetStdout(nil)
	if err := w.Run(); err != nil {
		t.Errorf("headless Run = %v, want nil", err)
	}
}

func TestHandleEventWiresAttention(t *testing.T) {
	// Both terminal states must produce the attention cmd (non-nil even
	// with bell+notify off: the silent title always rides along).
	m := newTestModel()
	m.model = "deepseek-v4"
	if _, cmd := m.handleEvent(client.Event{Type: "approval_request", ID: "a1",
		Risk: "shell_exec", Command: "rm -rf x"}); cmd == nil {
		t.Error("approval_request produced no cmd")
	}

	m2 := newTestModel()
	m2.model = "deepseek-v4"
	if _, cmd := m2.handleEvent(client.Event{Type: "done"}); cmd == nil {
		t.Error("done produced no cmd")
	}
}
