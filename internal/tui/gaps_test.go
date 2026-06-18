package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestApprovalUnmatchedAndNoTrust(t *testing.T) {
	m := wired(t)
	// AllowTrust=false: pressing "t" must NOT resolve the approval.
	m.approval = &client.Event{Type: "approval_request", AllowTrust: false}
	m.Update(key("t"))
	if m.approval == nil {
		t.Error("'t' without AllowTrust should not clear approval")
	}
	// An unrelated key falls through to a no-op.
	m.Update(key("z"))
	if m.approval == nil {
		t.Error("unrelated key should leave approval pending")
	}
}

func TestSyncACUnchangedQuery(t *testing.T) {
	m := wired(t)
	m.ta.SetValue("see @doc")
	m.ac.open = true
	m.ac.query = "doc"
	if cmd := m.syncAC(); cmd != nil {
		t.Error("syncAC with an unchanged query should return nil")
	}
	// No active ref while popup open → closeAC path.
	m.ta.SetValue("plain text")
	m.ac.open = true
	if cmd := m.syncAC(); cmd != nil {
		t.Error("syncAC with no ref should return nil and close")
	}
	if m.ac.open {
		t.Error("syncAC should have closed the popup")
	}
}

func TestArgPreviewURLKey(t *testing.T) {
	if got := argPreview(`{"url":"http://x"}`); got != "http://x" {
		t.Errorf("argPreview url = %q", got)
	}
}

func TestSanitizeStripsControlSequences(t *testing.T) {
	// ESC-based screen clear + OSC 52 clipboard write must be defanged.
	evil := "ok\x1b[2Jclear\x1b]52;c;ZXZpbA==\x07 \x7f\x00 plain\ttab\nnl"
	got := sanitize(evil)
	for _, bad := range []rune{'\x1b', '\x07', '\x00', '\x7f'} {
		if strings.ContainsRune(got, bad) {
			t.Errorf("sanitize left control byte %q in %q", bad, got)
		}
	}
	if !strings.Contains(got, "plain") || !strings.Contains(got, "\t") || !strings.Contains(got, "\n") {
		t.Errorf("sanitize dropped legitimate text/whitespace: %q", got)
	}
	// Fast path: clean input is returned unchanged.
	if sanitize("hello world") != "hello world" {
		t.Error("sanitize altered clean input")
	}
}

func TestUntrustedOutputDefanged(t *testing.T) {
	m := wired(t)
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: "{\"command\":\"x\x1b[2J\"}"})
	m.handleEvent(client.Event{Type: "tool_result", Name: "shell", Data: "out\x1b]0;pwn"})
	m.handleEvent(client.Event{Type: "token", Content: "hi\x1b[31m"})

	if strings.ContainsRune(m.msgs[0].content, '\x1b') {
		t.Error("streamed token escape not sanitized")
	}
	for _, s := range m.msgs[0].steps {
		if strings.ContainsRune(s.arg, '\x1b') || strings.ContainsRune(s.result, '\x1b') {
			t.Error("tool step escape not sanitized")
		}
	}
}
