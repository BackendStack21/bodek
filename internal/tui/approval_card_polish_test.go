package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// approvalCardRows returns the card body rows as plain text.
func approvalCardRows(m *Model) []string {
	return strings.Split(plain(m.approvalBody()), "\n")
}

func rowContaining(rows []string, want string) (string, bool) {
	for _, r := range rows {
		if strings.Contains(r, want) {
			return r, true
		}
	}
	return "", false
}

// TestApprovalActionRowListsDecisions checks the in-card decision row keeps
// allow-once and deny at both narrow and wide widths, and offers trust only
// when the server allows it and the row has room.
func TestApprovalActionRowListsDecisions(t *testing.T) {
	for _, width := range []int{40, 100} {
		m := newTestModel()
		m.resize(width, 30)
		busyTurn(m)
		m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Name: "shell",
			Risk: "shell_exec", Command: "rm -rf build/", Description: "clean build", AllowTrust: true})
		row, ok := rowContaining(approvalCardRows(m), "allow once")
		if !ok {
			t.Fatalf("%d cols: card has no action row:\n%s", width, plain(m.approvalBody()))
		}
		if !strings.Contains(row, "d deny") {
			t.Fatalf("%d cols: action row lost deny: %q", width, row)
		}
		if width == 100 && !strings.Contains(row, "t trust class") {
			t.Fatalf("100 cols: trust offered but missing from action row: %q", row)
		}
		if width == 40 && strings.Contains(row, "trust") {
			t.Fatalf("40 cols: trust must drop before allow-once or deny: %q", row)
		}
		if lipgloss.Width(row) > m.cardInner() {
			t.Fatalf("%d cols: action row %d cells exceeds card inner %d: %q", width, lipgloss.Width(row), m.cardInner(), row)
		}
	}
}

// TestApprovalActionRowFrictionPath checks friction cards advertise the typed
// confirmation path and Alt+D rather than trust.
func TestApprovalActionRowFrictionPath(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Name: "shell",
		Risk: "shell_exec", Command: "make", AllowTrust: true, Friction: true, FrictionApprovals: 3})
	row, ok := rowContaining(approvalCardRows(m), "type approve")
	if !ok {
		t.Fatalf("friction card has no typed-confirmation row:\n%s", plain(m.approvalBody()))
	}
	if !strings.Contains(row, "Alt+D deny") || strings.Contains(row, "trust") {
		t.Fatalf("friction action row = %q, want Alt+D deny and no trust", row)
	}
}

// TestApprovalReasonHiddenWhenRepeated checks a reason that only restates the
// tool name or the Action text is dropped, while a distinct reason stays.
func TestApprovalReasonHiddenWhenRepeated(t *testing.T) {
	cases := []struct {
		name, reason string
		expanded     bool
		wantShown    bool
	}{
		{"equals tool name", "shell", false, false},
		{"equals tool name case-insensitively", "SHELL", false, false},
		{"equals action text", "shell commands", false, false},
		{"distinct reason shown", "clean build artifacts", false, true},
		{"equals tool name expanded", "shell", true, false},
		{"distinct reason expanded", "clean build artifacts", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			m.resize(100, 30)
			m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Name: "shell",
				Risk: "shell_exec", Command: "rm -rf build/", Description: tc.reason})
			m.apprExpanded = tc.expanded
			body := plain(m.approvalBody())
			if got := strings.Contains(body, "Reason:"); got != tc.wantShown {
				t.Fatalf("Reason shown = %v, want %v:\n%s", got, tc.wantShown, body)
			}
		})
	}
}

// TestApprovalCardFitsTerminalHeight checks the approval card, with its new
// action row, never pushes View past the terminal height.
func TestApprovalCardFitsTerminalHeight(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{80, 16}, {40, 12}} {
		m := newTestModel()
		m.resize(sz.w, sz.h)
		busyTurn(m)
		m.handleEvent(client.Event{Type: "approval_request", ID: "approval", Name: "shell",
			Risk: "shell_exec", Command: "rm -rf build/", Description: "clean build artifacts", AllowTrust: true})
		view := plain(m.View())
		if rows := strings.Count(view, "\n") + 1; rows > sz.h {
			t.Fatalf("%dx%d: View uses %d rows with approval pending:\n%s", sz.w, sz.h, rows, view)
		}
		if !strings.Contains(view, "allow once") {
			t.Fatalf("%dx%d: approval action row not visible:\n%s", sz.w, sz.h, view)
		}
	}
}
