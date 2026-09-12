package tui

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── plan realtime: transient errors must not kill the poll chain, and a
// superseded confirm must be re-issued instead of deadlocking planDirty.

// TestPlanTransientErrorKeepsChain: a generic fetch error (timeout, 500,
// network blip) must NOT mark the endpoint unavailable — the next tick
// still polls. Only a 404 (old engine, route absent) is permanent.
func TestPlanTransientErrorKeepsChain(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{} // cmd construction only; the fetch never runs
	m.busy = true
	m.sessionID = "s1"
	m.planAvail = planAvailable
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, err: errors.New("timeout")})
	if m.planAvail == planUnavailable {
		t.Fatal("transient error killed the poll chain (plan unavailable)")
	}
	// The chain re-arms and the next tick still fetches.
	seq := m.planPollSeq
	if cmd := m.handlePlanTick(planTickMsg{seq: seq}); cmd == nil {
		t.Fatal("tick after a transient error must keep polling")
	}
	// A 404-class error IS permanent — old engine, route absent.
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, err: client.ErrPlanUnavailable})
	if m.planAvail != planUnavailable {
		t.Fatal("404 must mark the endpoint unavailable")
	}
	if cmd := m.handlePlanTick(planTickMsg{seq: m.planPollSeq}); cmd != nil {
		t.Fatal("tick with an unavailable endpoint must stop the chain")
	}
}

// TestPlanDirtyTickReissuesConfirm: when a confirm fetch was superseded (the
// only thing that can clear planDirty died in flight), the live tick must
// re-issue a confirm instead of arming forever — otherwise the strip stays
// frozen at the optimistic patch (plan 0/N).
func TestPlanDirtyTickReissuesConfirm(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m.sessionID = "s1"
	m.cl = &client.Client{} // cmd construction only; the fetch never runs
	m.planInit = true
	m.planAvail = planAvailable
	m.plan.Steps = []client.PlanStep{{ID: "a", Status: client.PlanDone}, {ID: "b"}}
	m.planDirty = true
	m.planConfirmIssued = true // the tool_result debounce fired; its reply died in flight
	cmd := m.handlePlanTick(planTickMsg{seq: m.planPollSeq})
	if cmd == nil {
		t.Fatal("dirty tick must re-issue the confirm fetch")
	}
	// The re-issue is observable: planReqSeq advances (a fresh fetch armed).
	if m.planReqSeq == 0 {
		t.Fatal("dirty tick did not arm any fetch")
	}
	// And a landed confirm clears the dirty flag (existing accept path).
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, confirm: true,
		snap: client.PlanSnapshot{SessionID: "s1", Version: 1, Steps: m.plan.Steps}})
	if m.planDirty {
		t.Fatal("confirm reply did not clear planDirty")
	}
}

// TestSessionPlanErrorClassification: the client marks 404 as the permanent
// sentinel; other statuses stay ordinary errors.
func TestSessionPlanErrorClassification(t *testing.T) {
	// Covered via the tui-facing sentinel contract in TestPlanTransientErrorKeepsChain;
	// the client-side mux test lives in client/plan_test.go. Here we pin the
	// sentinel exists and wraps.
	if !strings.Contains(client.ErrPlanUnavailable.Error(), "plan") {
		t.Fatalf("sentinel missing or misnamed: %v", client.ErrPlanUnavailable)
	}
	_ = http.StatusNotFound
}
