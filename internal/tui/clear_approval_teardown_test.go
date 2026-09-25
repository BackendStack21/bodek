package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// Regression: the errMsg teardown cleared approvals and deadlines but left
// apprBells armed, breaking the documented lockstep invariant — the next
// approval popped a stale bell latch.
func TestErrMsgTeardownKeepsBellLockstep(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	if len(m.apprBells) != len(m.apprDeadlines) {
		t.Fatalf("precondition: bells/deadlines out of lockstep: %d/%d",
			len(m.apprBells), len(m.apprDeadlines))
	}

	m.Update(errMsg{err: errors.New("write failed")})

	if len(m.approvals) != 0 || len(m.apprDeadlines) != 0 {
		t.Fatalf("teardown left approvals armed: %d/%d", len(m.approvals), len(m.apprDeadlines))
	}
	if len(m.apprBells) != 0 {
		t.Fatalf("teardown left bells armed (lockstep broken): %d", len(m.apprBells))
	}
}

// Regression: /clear mid-turn wiped the transcript but left the approval
// queue, deadlines, bells, and clarify armed over a dead request — the
// keyboard stayed gated by a request that no longer had a turn behind it.
// Every other teardown path (done/error/disconnect) clears them explicitly.
func TestClearConversationDropsPendingApprovals(t *testing.T) {
	m := newTestModel()
	feedApproval(t, m, client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.apprDeadlines[0] = time.Now().Add(time.Minute)

	m.clearConversation()

	if len(m.approvals) != 0 || len(m.apprDeadlines) != 0 || len(m.apprBells) != 0 {
		t.Fatalf("/clear left approvals armed: appr=%d dl=%d bells=%d",
			len(m.approvals), len(m.apprDeadlines), len(m.apprBells))
	}
	if m.clarify != nil || m.clarifyBuf != "" {
		t.Fatalf("/clear left clarify armed: q=%v buf=%q", m.clarify != nil, m.clarifyBuf)
	}
}
