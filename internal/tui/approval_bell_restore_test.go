package tui

import (
	"errors"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestApprovalSendErrRestoresBellLatch guards the parallel-array contract:
// apprBells must stay 1:1 with apprDeadlines when a failed send restores
// the popped head — otherwise the urgent-window BEL latch reads a
// neighbor's flag and fires for the wrong request for the rest of the
// session.
func TestApprovalSendErrRestoresBellLatch(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1", Risk: "shell_exec", Command: "echo a"})
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-2", Risk: "shell_exec", Command: "echo b"})
	if len(m.approvals) != 2 || len(m.apprBells) != 2 {
		t.Fatalf("precondition: 2 approvals expected, got %d/%d", len(m.approvals), len(m.apprBells))
	}

	// Mirror answer()'s pop of the head before the wire write fails:
	// head + deadline + bell leave the queues, then the restore must put
	// all three back in lockstep.
	ev := *m.curApproval()
	dl := m.apprDeadlines[0]
	m.approvals = m.approvals[1:]
	m.apprDeadlines = m.apprDeadlines[1:]
	m.apprBells = m.apprBells[1:]

	m.Update(approvalSendErrMsg{ev: ev, dl: dl, err: errors.New("send failed")})

	if len(m.approvals) != 2 || len(m.apprDeadlines) != 2 || len(m.apprBells) != 2 {
		t.Fatalf("send-fail restore lost an array: appr=%d dl=%d bells=%d",
			len(m.approvals), len(m.apprDeadlines), len(m.apprBells))
	}
}
