package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
	tea "github.com/charmbracelet/bubbletea"
)

// Regression tests for three TUI defects: the ctrl+enter newline chord in
// the approval composer, the per-approval urgent-window bell, and the
// queue-strip click hit test bleeding into row-body text.

func TestApprovalCtrlEnterInsertsNewline(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.ta.SetValue("draft")
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ctrl+enter")}
	m.handleApprovalKey(msg)
	if v := m.ta.Value(); !strings.Contains(v, "\n") {
		t.Fatalf("ctrl+enter must insert a newline, draft = %q", v)
	}
	if strings.Contains(m.ta.Value(), "ctrl+enter") {
		t.Fatalf("ctrl+enter sentinel leaked into the draft as text: %q", m.ta.Value())
	}
}

func TestApprovalUrgentBellFiresPerEntry(t *testing.T) {
	m := newTestModel()
	m.bell = true
	// Two approvals in flight; the head is already in the urgent window.
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "a1", Risk: "shell_exec", Command: "rm x"})
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "a2", Risk: "shell_exec", Command: "rm y"})
	m.apprDeadlines[0] = time.Now().Add(9 * time.Second)
	m.apprDeadlines[1] = time.Now().Add(90 * time.Second)

	m.handleApprovalExpiry(time.Now())
	if len(m.apprBells) != 2 || !m.apprBells[0] {
		t.Fatalf("head's urgent-window bell must latch per entry: %v", m.apprBells)
	}
	// Same window again: no re-fire for the same entry.
	m.handleApprovalExpiry(time.Now())
	if len(m.apprBells) != 2 || !m.apprBells[0] || m.apprBells[1] {
		t.Fatalf("bell guard must stay latched per entry: %v", m.apprBells)
	}
	// The head expires; the surviving second entry later enters its own
	// urgent window and must ring on its own — a mid-queue expiry bell
	// fires once PER ENTRY, not once total.
	m.apprDeadlines[0] = time.Now().Add(-time.Second)
	m.handleApprovalExpiry(time.Now())
	if len(m.approvals) != 1 {
		t.Fatalf("expired head must drop: %d left", len(m.approvals))
	}
	m.apprDeadlines[0] = time.Now().Add(9 * time.Second)
	m.handleApprovalExpiry(time.Now())
	if !m.apprBells[0] {
		t.Fatal("the surviving entry must fire its own urgent-window bell")
	}
}

func TestQueueStripClickBoundsControlsToTheirCells(t *testing.T) {
	m := newTestModel()
	m.queue = []string{"hello world"}
	m.qfocus = true
	// Locate the controls on the unstyled first row.
	row := unstyle(strings.Split(m.queueStripView(), "\n")[0])
	delC := -1
	cell := 0
	for _, r := range row {
		if r == '✕' && delC < 0 {
			delC = cell
		}
		cell += 1
	}
	if delC < 0 {
		t.Fatal("✕ control not found on the strip row")
	}
	top := m.queueStripTop()
	// A click past the ✕ glyph (row-body / trailing region) must select the
	// row, not arm the delete confirm.
	m.queueStripClick(top, delC+2)
	if m.qarm != -1 {
		t.Fatalf("click past the ✕ glyph must not arm delete (qarm = %d)", m.qarm)
	}
	if m.qsel != 0 {
		t.Fatalf("click past the controls must select the row (qsel = %d)", m.qsel)
	}
	// A click on the ✕ cell itself still arms.
	m.queueStripClick(top, delC)
	if m.qarm != 0 {
		t.Fatalf("click on the ✕ cell must arm delete (qarm = %d)", m.qarm)
	}
}
