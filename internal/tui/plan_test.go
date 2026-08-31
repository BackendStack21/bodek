package tui

import (
	"errors"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// Tests for the planning-surface state machine: WS-trigger
// scheduling + debounce sequencing, monotonic acceptance,
// tab-visible poll lifecycle, and the session-switch reset hook. Assertions
// ride synchronous observables (seq counters, flags, accepted state); fetch
// closures are never executed — the wire contract lives in internal/client,
// and invoking arbitrary batch children (listen…) would block a test.

var errTestPlanRoute = errors.New("session plan: status 404 Not Found")

func planCallEvent(name string) client.Event {
	return client.Event{Type: "tool_call", Name: name, Data: "{}"}
}

func TestPlanWSTrigger_DebouncedRefresh(t *testing.T) {
	m := &Model{}
	before := m.planDebSeq
	m.handleEvent(planCallEvent("shell"))
	if m.planDebSeq != before {
		t.Fatal("non-plan tool_call must not arm the refresh")
	}

	m.handleEvent(planCallEvent("plan"))
	if m.planDebSeq != before+1 {
		t.Fatalf("plan tool_call must arm a fresh window: %d → %d", before, m.planDebSeq)
	}

	// Sequence guard: superseded windows are dropped, the live one fetches
	// (only while attached to a session), and fetching bumps the request seq.
	if c := m.handlePlanDebounce(planDebounceMsg{seq: m.planDebSeq - 1}); c != nil {
		t.Fatal("stale debounce window must be dropped")
	}
	if c := m.handlePlanDebounce(planDebounceMsg{seq: m.planDebSeq}); c != nil {
		t.Fatal("no session attached → no fetch")
	}
	m.sessionID = "s1"
	m.cl = &client.Client{} // non-nil so fetchPlan builds its closure
	reqBefore := m.planReqSeq
	if c := m.handlePlanDebounce(planDebounceMsg{seq: m.planDebSeq}); c == nil {
		t.Fatal("live window must issue the fetch command")
	}
	if m.planReqSeq != reqBefore+1 {
		t.Fatalf("request seq = %d, want %d", m.planReqSeq, reqBefore+1)
	}
}

func TestPlanFollowup_SessionSwitchResetsAndRefetches(t *testing.T) {
	m := &Model{sessionID: "s1"}
	m.planInit = true
	m.planVer = 7
	m.planAvail = planAvailable

	// First contact on a brand-new model must not trip the hook.
	m0 := &Model{cl: &client.Client{}}
	reqAt := m0.planReqSeq
	m0.handleEvent(client.Event{Type: "session", SessionID: "s9"})
	if m0.planResetPending || m0.planReqSeq != reqAt || m0.planInit {
		t.Fatal("initial session event must not schedule a reset")
	}
	// Identical id on the live model: no-op.
	m.handleEvent(client.Event{Type: "session", SessionID: "s1"})
	if m.planResetPending {
		t.Fatal("repeated id must not schedule a reset")
	}

	// Real switch: state resets and the immediate refetch goes out.
	m.cl = &client.Client{} // refetch needs a client handle
	m.handleEvent(client.Event{Type: "session", SessionID: "s42"})
	if m.planInit || m.planVer != 0 || m.planAvail != planUnknown {
		t.Fatalf("state not reset: init=%v ver=%d avail=%d",
			m.planInit, m.planVer, m.planAvail)
	}
	if m.sessionID != "s42" {
		t.Fatalf("session id not adopted: %q", m.sessionID)
	}
	if m.planReqSeq <= reqAt {
		t.Fatal("switch must drain in-flight replies and issue a refetch")
	}
}

func TestPlanAccept_MonotonicGuard(t *testing.T) {
	m := &Model{sessionID: "s1"}
	steps := []client.PlanStep{{ID: "a", Title: "A", Status: client.PlanDone}}

	m.planReqSeq++
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, snap: client.PlanSnapshot{
		SessionID: "s1", Version: 3, Found: true, Steps: steps,
	}})
	if !m.planInit || m.planVer != 3 || len(m.plan.Steps) != 1 || m.planAvail != planAvailable {
		t.Fatalf("first snapshot not accepted: %+v avail=%d", m.plan, m.planAvail)
	}

	// Superseded sequencing / foreign session / version regression: all drops.
	m.planReqSeq++ // superseded sequence
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq - 1,
		snap: client.PlanSnapshot{SessionID: "s1", Version: 4, Found: true}})
	m.planReqSeq++ // foreign session
	m.handlePlanMsg(planMsg{want: "other", seq: m.planReqSeq,
		snap: client.PlanSnapshot{SessionID: "other", Version: 4, Found: true}})
	m.planReqSeq++ // stale not-found
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq,
		snap: client.PlanSnapshot{SessionID: "s1", Version: 2, Found: false}})
	if m.planVer != 3 || len(m.plan.Steps) != 1 {
		t.Fatalf("stale replies mutated state: ver=%d steps=%d", m.planVer, len(m.plan.Steps))
	}
}

func TestPlanUnavailable_SilentDegrade(t *testing.T) {
	m := &Model{sessionID: "s1"}
	m.planReqSeq++
	cmd := m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, err: errTestPlanRoute})
	if m.planAvail != planUnavailable {
		t.Fatalf("avail = %d, want unavailable", m.planAvail)
	}
	if cmd != nil {
		t.Fatal("hidden tab must not re-arm anything on error")
	}
}

func TestPlanPoll_TabVisibleLifecycle(t *testing.T) {
	m := &Model{sessionID: "s1", cl: &client.Client{}}
	m.panel = panelPlan

	cmd := m.armPlanPoll()
	if cmd == nil || m.planPollSeq == 0 {
		t.Fatal("arm must schedule exactly one tick")
	}
	if c := m.handlePlanTick(planTickMsg{seq: m.planPollSeq}); c == nil {
		t.Fatal("fresh tick on visible tab must refetch")
	}
	if c := m.handlePlanTick(planTickMsg{seq: m.planPollSeq - 1}); c != nil {
		t.Fatal("superseded tick must drain")
	}
	m.panel = panelNone
	if c := m.handlePlanTick(planTickMsg{seq: m.planPollSeq}); c != nil {
		t.Fatal("closed tab must stop polling")
	}

	// An error reply stops the chain while the tab stays open.
	m.panel = panelPlan
	m.planAvail = planUnknown
	m.planReqSeq++
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, err: errTestPlanRoute})
	if c := m.handlePlanTick(planTickMsg{seq: m.planPollSeq}); c != nil {
		t.Fatal("unavailable endpoint must not keep polling")
	}
}
