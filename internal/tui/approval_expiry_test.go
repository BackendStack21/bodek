package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// The approval form must reflect the server-side approval lifetime: a live
// countdown in the panel head and an autoclose that drops expired requests
// (the engine already failed them with "approval timeout" and picks an
// alternative path — a lingering form only invites approving a dead prompt).

func feedApproval(t *testing.T, m *Model, ev client.Event) {
	t.Helper()
	m.handleEvent(ev)
}

func TestApprovalCountdownDefaultTTL(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	out := m.approvalBody()
	if !strings.Contains(out, "expires in 60s") {
		t.Fatalf("default 60s countdown missing from panel: %q", out)
	}
	// The deadline rides the arrival clock: ~60s out.
	d := time.Until(m.apprDeadlines[0])
	if d <= 0 || d > 60*time.Second {
		t.Fatalf("deadline not stamped on arrival: %v", d)
	}
}

func TestApprovalCountdownFrameTimeout(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "network_egress",
		Command: "curl x", TimeoutSeconds: 120})
	if out := m.approvalBody(); !strings.Contains(out, "expires in 120s") {
		t.Fatalf("frame-provided TTL not rendered: %q", out)
	}
	d := time.Until(m.apprDeadlines[0])
	if d <= 119*time.Second || d > 120*time.Second {
		t.Fatalf("frame TTL not honoured: %v", d)
	}
}

func TestApprovalCountdownTickKeepsFutureHead(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.apprDeadlines[0] = time.Now().Add(42 * time.Second)
	m.Update(approvalExpireMsg{})
	if len(m.approvals) != 1 {
		t.Fatalf("future deadline must not drop the request: %d", len(m.approvals))
	}
	if out := m.approvalBody(); !strings.Contains(out, "expires in 42s") {
		t.Fatalf("countdown not re-rendered by the sweep tick: %q", out)
	}
}

func TestApprovalAutocloseOnExpiry(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	_, cmd := m.Update(approvalExpireMsg{})
	if len(m.approvals) != 0 || len(m.apprDeadlines) != 0 {
		t.Fatalf("expired request not dropped: %d approvals, %d deadlines",
			len(m.approvals), len(m.apprDeadlines))
	}
	if !strings.Contains(strings.Join(m.notices, "\n"), "expired") {
		t.Fatalf("no expiry notice pushed: %q", m.notices)
	}
	if m.status != "thinking" {
		t.Fatalf("status must mirror answer() on drain, got %q", m.status)
	}
	if cmd != nil {
		t.Fatal("sweep must stop itself once the queue is empty")
	}
}

func TestApprovalExpiryQueuedSuccessor(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-1", Risk: "shell_exec", Command: "rm x"})
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-2", Risk: "network_egress", Command: "curl x"})
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	m.apprDeadlines[1] = time.Now().Add(30 * time.Second)
	_, cmd := m.Update(approvalExpireMsg{})
	if a := m.curApproval(); a == nil || a.ID != "apr-2" {
		t.Fatalf("successor must become the queue head, got %+v", a)
	}
	if m.status != "approval required" {
		t.Fatalf("status with a remaining request, got %q", m.status)
	}
	if out := m.approvalBody(); !strings.Contains(out, "expires in 30s") {
		t.Fatalf("successor countdown not rendered: %q", out)
	}
	if n := strings.Count(strings.Join(m.notices, "\n"), "expired"); n != 1 {
		t.Fatalf("expected exactly one expiry notice, got %d", n)
	}
	if cmd == nil {
		t.Fatal("sweep must re-arm while a form is open")
	}
}

func TestAnswerDropsDeadline(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-1", Risk: "shell_exec", Command: "rm x"})
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-2", Risk: "network_egress", Command: "curl x"})
	_ = m.answer("deny")
	if len(m.apprDeadlines) != 1 {
		t.Fatalf("deadline queue out of sync with approvals: %d", len(m.apprDeadlines))
	}
	if a := m.curApproval(); a == nil || a.ID != "apr-2" {
		t.Fatalf("unexpected queue head after answer: %+v", a)
	}
}

// Approvals that never got a stamped deadline (e.g. state built directly in
// tests) must render no countdown and never expire — guessing a TTL is
// worse than showing nothing.
func TestApprovalCountdownRequiresDeadline(t *testing.T) {
	m := newTestModel()
	m.approvals = []client.Event{{Type: "approval_request", ID: "x"}}
	if strings.Contains(m.approvalBody(), "expires in") {
		t.Fatal("countdown rendered without a stamped deadline")
	}
	m.Update(approvalExpireMsg{})
	if len(m.approvals) != 1 {
		t.Fatal("request without a deadline must not be dropped")
	}
}
