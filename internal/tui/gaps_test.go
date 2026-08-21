package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestApprovalDecisionLetters(t *testing.T) {
	m := wired(t)
	// Redesign policy: the composer is hidden while an approval is pending,
	// so the dedicated decision letters a/d/t (WebUI parity) resolve it.
	// Every other letter — including y/n — still must never decide.
	m.approvals = []client.Event{{Type: "approval_request", AllowTrust: false}}
	m.Update(key("y"))
	m.Update(key("n"))
	m.Update(key("z"))
	if m.curApproval() == nil {
		t.Fatal("a non-decision letter resolved the approval")
	}
	_, cmd := m.Update(key("a")) // approve
	exec(cmd)
	if m.curApproval() != nil {
		t.Fatal("a did not approve")
	}

	m.approvals = []client.Event{{Type: "approval_request", AllowTrust: false}}
	_, cmd = m.Update(key("d")) // deny
	exec(cmd)
	if m.curApproval() != nil {
		t.Fatal("d did not deny")
	}

	// AllowTrust=false: t does nothing, and the highlight clamps at "deny" —
	// trust is unreachable both ways.
	m.approvals = []client.Event{{Type: "approval_request", AllowTrust: false}}
	m.Update(key("t"))
	if m.curApproval() == nil {
		t.Fatal("t decided without allow_trust")
	}
	m.Update(key("down"))
	m.Update(key("down"))
	if m.apprSel != 1 {
		t.Errorf("apprSel = %d, want clamped at 1 (deny)", m.apprSel)
	}
	_, cmd = m.Update(key("enter"))
	exec(cmd)
	if m.curApproval() != nil {
		t.Error("enter on deny should clear the approval")
	}
}

// TestApprovalQueueFIFO pins the parallel-approval contract: requests are
// answered in arrival order and the panel surfaces the queue depth.
func TestApprovalQueueFIFO(t *testing.T) {
	m, actions := approvalRecorder(t)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1", Command: "rm a"})
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-2", Command: "rm b"})
	if len(m.approvals) != 2 {
		t.Fatalf("queue = %d", len(m.approvals))
	}
	out := plain(m.View())
	if !strings.Contains(out, "1 of 2") {
		t.Errorf("queue position missing from panel:\n%s", out)
	}
	// Deny answers apr-1; apr-2 becomes the head with its own input state.
	_, cmd := m.Update(key("esc"))
	exec(cmd)
	if got := awaitAction(t, actions); got != "deny" {
		t.Fatalf("first answer = %q", got)
	}
	if len(m.approvals) != 1 || m.approvals[0].ID != "apr-2" {
		t.Fatalf("queue after first answer = %+v", m.approvals)
	}
	if m.apprTyped != "" || m.apprSel != 0 {
		t.Error("input state not reset for the new head")
	}
	// A queued friction request engages friction once it reaches the head —
	// letters feed the typed buffer, not decisions.
	_, cmd = m.Update(key("esc")) // deny apr-2 → queue drains
	exec(cmd)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-3", Friction: true, FrictionApprovals: 3})
	_, cmd = m.Update(key("d"))
	exec(cmd)
	if m.apprTyped != "d" {
		t.Fatalf("friction head consumed a letter as decision: typed=%q", m.apprTyped)
	}
	if len(m.approvals) != 1 {
		t.Fatal("letter decided a friction approval")
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
