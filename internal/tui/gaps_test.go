package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestApprovalDecisionLetters(t *testing.T) {
	m := wired(t)
	// Bare decision letters stay in the composer, including when they arrive
	// alongside cursor movement.
	m.approvals = []client.Event{{Type: "approval_request", AllowTrust: false}}
	m.Update(key("y"))
	m.Update(key("n"))
	m.Update(key("z"))
	if m.curApproval() == nil {
		t.Fatal("a non-decision letter resolved the approval")
	}
	if m.ta.Value() != "ynz" {
		t.Fatalf("non-decision letters must type into the composer, got %q", m.ta.Value())
	}
	m.Update(key("a"))
	if m.curApproval() == nil {
		t.Fatal("bare a answered the approval")
	}
	_, cmd := m.Update(key("alt+a")) // approve
	exec(cmd)
	if m.curApproval() != nil {
		t.Fatal("Alt+A did not approve")
	}

	m.approvals = []client.Event{{Type: "approval_request", AllowTrust: false}}
	_, cmd = m.Update(key("alt+d")) // deny
	exec(cmd)
	if m.curApproval() != nil {
		t.Fatal("Alt+D did not deny")
	}

	// AllowTrust=false: Alt+T does nothing, and trust is unreachable.
	m.approvals = []client.Event{{Type: "approval_request", AllowTrust: false}}
	m.Update(key("alt+t"))
	if m.curApproval() == nil {
		t.Fatal("Alt+T decided without allow_trust")
	}
}

// TestApprovalQueueFIFO pins the parallel-approval contract: requests are
// answered in arrival order and the panel surfaces the queue depth.
func TestApprovalQueueFIFO(t *testing.T) {
	m, actions, _ := approvalRecorder(t)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1", Command: "rm a"})
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-2", Command: "rm b"})
	if len(m.approvals) != 2 {
		t.Fatalf("queue = %d", len(m.approvals))
	}
	out := plain(m.View())
	if !strings.Contains(out, "1 queued") {
		t.Errorf("queue depth missing from panel:\n%s", out)
	}
	// Deny answers apr-1; apr-2 becomes the head with its own input state.
	_, cmd := m.Update(key("alt+d"))
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
	// A friction request keeps ordinary letters in the composer until Alt+A
	// explicitly activates its confirmation editor.
	_, cmd = m.Update(key("alt+d")) // deny apr-2 → queue drains
	exec(cmd)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-3", Friction: true, FrictionApprovals: 3})
	m.Update(key("d"))
	if m.apprTyped != "" || m.ta.Value() != "d" {
		t.Fatalf("friction head should keep ordinary typing in composer: typed=%q draft=%q", m.apprTyped, m.ta.Value())
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
		// Tabs survive sanitize — copy fidelity beats display convenience;
		// display paths expand them where cell math happens.
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
