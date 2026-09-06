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
	if m.status != "ready" {
		t.Fatalf("status after idle expiry = %q, want ready", m.status)
	}
	if m.approvalSweep() != nil {
		t.Fatal("approval sweep must stop itself once the queue is empty")
	}
	if cmd == nil {
		t.Fatal("idle expiry must arm the notice sweep so the note fades")
	}
	if strings.Contains(plain(m.View()), "approval required") {
		t.Fatal("expired card still rendered")
	}
	if strings.Contains(plain(m.footer()), "pprove") {
		t.Fatal("expired footer still shows A/D decision hints")
	}
	if m.modeName() == "approval" {
		t.Fatal("mode must leave approval after autoclose")
	}
	m.Update(key("a"))
	if got := m.ta.Value(); got != "a" {
		t.Fatalf("a must type into the composer after autoclose, got %q", got)
	}
}

// Autoclose must yank the reader to the newest transcript message: they
// may have scrolled up while the card was open, and refresh() alone only
// sticks when already at the bottom. focusIdx parks so alt+y copies that
// turn (or falls back if it is not a copyable reply).
func TestApprovalExpiryFocusesLatestTranscript(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleUser, content: "do it"})
	tallTranscript(m)
	m.focusIdx = 0
	m.vp.GotoTop()
	if m.vp.AtBottom() {
		t.Fatal("precondition: must be away from the latest message")
	}
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	m.Update(approvalExpireMsg{})

	if m.curApproval() != nil {
		t.Fatal("expired form must autoclose")
	}
	if !m.vp.AtBottom() {
		t.Fatalf("viewport must jump to the latest message, yoffset=%d", m.vp.YOffset)
	}
	if want := len(m.msgs) - 1; m.focusIdx != want {
		t.Fatalf("focusIdx = %d, want %d (latest message)", m.focusIdx, want)
	}
}

// Quiet must not swallow the autoclose notice: the card the operator was
// staring at vanished. transientNoteCmd is the operator path.
func TestApprovalExpiryNoticeBypassesQuiet(t *testing.T) {
	m := newTestModel()
	m.verbosity = verbosityQuiet
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	m.Update(approvalExpireMsg{})
	if !strings.Contains(strings.Join(m.notices, "\n"), "expired") {
		t.Fatalf("quiet swallowed the expiry notice: %q", m.notices)
	}
}

// A mid-turn expiry still parks on the in-flight assistant card so later
// tokens stick at the bottom (refresh only sticks when already there).
func TestApprovalExpiryBusyFocusesLatest(t *testing.T) {
	m := newTestModel()
	tallTranscript(m)
	busyTurn(m)
	m.vp.GotoTop()
	if m.vp.AtBottom() {
		t.Fatal("precondition: must be away from the latest message")
	}
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	m.Update(approvalExpireMsg{})
	if m.status != "thinking" {
		t.Fatalf("busy expiry status = %q, want thinking", m.status)
	}
	if !m.vp.AtBottom() {
		t.Fatalf("viewport must jump to the latest message, yoffset=%d", m.vp.YOffset)
	}
	if want := len(m.msgs) - 1; m.focusIdx != want {
		t.Fatalf("focusIdx = %d, want %d (latest message)", m.focusIdx, want)
	}
}

// A successor still waiting must not steal scrollback — the form is still
// the focus, and yanking would fight the same contract beginWireTurn keeps.
func TestApprovalExpirySuccessorKeepsScroll(t *testing.T) {
	m := newTestModel()
	tallTranscript(m)
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-1", Risk: "shell_exec", Command: "rm x"})
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-2", Risk: "network_egress", Command: "curl x"})
	m.vp.GotoTop()
	off := m.vp.YOffset
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	m.apprDeadlines[1] = time.Now().Add(30 * time.Second)
	m.Update(approvalExpireMsg{})

	if a := m.curApproval(); a == nil || a.ID != "apr-2" {
		t.Fatalf("successor must remain the form, got %+v", a)
	}
	if m.vp.YOffset != off {
		t.Fatalf("successor form must not yank scrollback: yoffset=%d, was=%d", m.vp.YOffset, off)
	}
}

// Expiry on an idle TUI must not flip the badge to "thinking" — the engine
// is not running. answer() uses that label because it resumes a live turn;
// autoclose after the turn already ended must land on ready.
func TestApprovalExpiryIdleStatusReady(t *testing.T) {
	m := newTestModel()
	if m.busy {
		t.Fatal("precondition: newTestModel is idle")
	}
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	m.Update(approvalExpireMsg{})
	if m.status != "ready" {
		t.Fatalf("idle expiry status = %q, want ready", m.status)
	}
}

// Expiry mid-turn still reports thinking: the engine timed the prompt out
// and continues on an alternative path.
func TestApprovalExpiryWhileBusyKeepsThinking(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	m.Update(approvalExpireMsg{})
	if m.status != "thinking" {
		t.Fatalf("busy expiry status = %q, want thinking", m.status)
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

// Mixed TTLs (possible once odek sends per-frame timeout_seconds): a short
// deadline can expire mid-queue while the head is still alive. The sweep
// must prune exactly the expired entries, wherever they sit, and leave the
// living head — input state included — untouched.
func TestApprovalExpiryMidQueuePreservesHead(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-1", Risk: "shell_exec", Command: "rm x"})
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-2", Risk: "network_egress", Command: "curl x"})
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-3", Risk: "shell_exec", Command: "make test"})
	m.apprDeadlines[1] = time.Now().Add(-time.Second) // apr-2 expired mid-queue
	m.apprDeadlines[2] = time.Now().Add(45 * time.Second)
	m.apprSel = 1
	m.apprExpanded = true
	_, cmd := m.Update(approvalExpireMsg{})

	if len(m.approvals) != 2 || m.approvals[0].ID != "apr-1" || m.approvals[1].ID != "apr-3" {
		t.Fatalf("expected survivors [apr-1 apr-3], got %+v", m.approvals)
	}
	if len(m.apprDeadlines) != 2 {
		t.Fatalf("deadline queue out of sync: %d", len(m.apprDeadlines))
	}
	if m.apprSel != 1 || !m.apprExpanded {
		t.Fatal("living head's input state must survive a mid-queue drop")
	}
	if out := m.approvalBody(); !strings.Contains(out, "expires in 60s") {
		t.Fatalf("head countdown disturbed: %q", out)
	}
	if n := strings.Count(strings.Join(m.notices, "\n"), "expired"); n != 1 {
		t.Fatalf("expected exactly one expiry notice, got %d", n)
	}
	if m.status != "approval required" {
		t.Fatalf("status with survivors, got %q", m.status)
	}
	if cmd == nil {
		t.Fatal("sweep must re-arm while a form is open")
	}
}

// Head and tail expired, middle alive: exactly the middle survives and —
// the head having changed — the input state resets.
func TestApprovalExpiryMultiDropSkipsAliveMiddle(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-1", Risk: "shell_exec", Command: "rm x"})
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-2", Risk: "network_egress", Command: "curl x"})
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr-3", Risk: "shell_exec", Command: "make test"})
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	m.apprDeadlines[2] = time.Now().Add(-time.Second)
	m.apprSel = 1
	m.apprExpanded = true
	_, cmd := m.Update(approvalExpireMsg{})

	if len(m.approvals) != 1 || m.approvals[0].ID != "apr-2" {
		t.Fatalf("only the alive middle must survive, got %+v", m.approvals)
	}
	if len(m.apprDeadlines) != 1 {
		t.Fatalf("deadline queue out of sync: %d", len(m.apprDeadlines))
	}
	if m.apprSel != 0 || m.apprExpanded {
		t.Fatal("head changed — input state must reset")
	}
	if m.status != "approval required" {
		t.Fatalf("status with a survivor, got %q", m.status)
	}
	if cmd == nil {
		t.Fatal("sweep must re-arm while a form is open")
	}
}
