package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── review round-3 closures: cockpit engine row renders exactly once, live
// rate streams in the sheet, and the planConfirmIssued lifecycle is pinned.

// TestCockpitEngineVersionOnce: the engine version renders exactly once in
// the cockpit (server card dropped its row; the stats sheet owns it).
func TestCockpitEngineVersionOnce(t *testing.T) {
	m := headerModel(t)
	m.popover = true
	v := plain(m.View())
	if n := strings.Count(v, "v1.40.2"); n != 1 {
		t.Fatalf("engine version rendered %d times, want 1:\n%s", n, firstLines(v, 20))
	}
}

// TestCockpitLiveRateWhileStreaming: a busy turn's live rate paints the
// stats sheet mid-stream — the sheet is the rate's home now.
func TestCockpitLiveRateWhileStreaming(t *testing.T) {
	m := headerModel(t)
	m.busy = true
	m.popover = true
	v := plain(m.View())
	if !strings.Contains(v, "42.5 tok/s") || !strings.Contains(v, "live") {
		t.Fatalf("stats sheet missing the live rate while streaming:\n%s", firstLines(v, 22))
	}
	// Idle: no live row (sealed turn rows own the rate after done).
	m.busy = false
	v = plain(m.View())
	if strings.Contains(v, "· live") {
		t.Fatalf("idle sheet must not show the live rate row:\n%s", firstLines(v, 22))
	}
}

// TestPlanConfirmIssuedLifecycle: the debounce sets it, an accepted reply
// clears it, and reset drops it.
func TestPlanConfirmIssuedLifecycle(t *testing.T) {
	m := newTestModel()
	m.sessionID = "s1"
	m.cl = &client.Client{}
	m.planInit = true
	m.planAvail = planAvailable

	// issuePlanFetch(confirm=true) sets the flag.
	_ = m.fetchPlanConfirm()
	if !m.planConfirmIssued {
		t.Fatal("confirm fetch must set planConfirmIssued")
	}
	// A non-confirm fetch must not set it (it is never unset by polls).
	m.planConfirmIssued = false
	_ = m.fetchPlan()
	if m.planConfirmIssued {
		t.Fatal("a poll fetch must not set planConfirmIssued")
	}

	// An accepted confirm reply clears the flag along with planDirty.
	m.planConfirmIssued = true
	m.planDirty = true
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, confirm: true,
		snap: client.PlanSnapshot{SessionID: "s1", Version: 3, Found: true}})
	if m.planConfirmIssued || m.planDirty {
		t.Fatalf("accept must clear both: issued=%v dirty=%v", m.planConfirmIssued, m.planDirty)
	}

	// resetPlanState drops it too.
	m.planConfirmIssued = true
	m.resetPlanState()
	if m.planConfirmIssued {
		t.Fatal("reset must clear planConfirmIssued")
	}
}

// TestPlanFollowupReissuesDeadConfirm: a turn boundary with a dirty strip
// whose confirm died re-issues the fetch instead of waiting for a tick.
func TestPlanFollowupReissuesDeadConfirm(t *testing.T) {
	m := newTestModel()
	m.sessionID = "s1"
	m.cl = &client.Client{}
	m.planInit = true
	m.planAvail = planAvailable
	m.planDirty = true
	m.planConfirmIssued = true // debounce fired; the reply died in flight

	m.planLiveKick = true
	seq := m.planReqSeq
	_ = m.planFollowup()
	if m.planReqSeq == seq {
		t.Fatal("turn boundary must re-issue the dead confirm")
	}
	// Without planConfirmIssued, no confirm re-issue (pre-write guard).
	m.planConfirmIssued = false
	m.planDirty = true
	m.planLiveKick = true
	seq = m.planReqSeq
	_ = m.planFollowup()
	if m.planReqSeq != seq {
		t.Fatal("turn boundary must not fetch a pre-write confirm")
	}
}
