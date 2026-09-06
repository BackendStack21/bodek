package tui

import (
	"errors"
	"strings"
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

// TestPlanSurface_SessionResumeResetsAndRefetches pins the resume path:
// handleSessionDetail drops the previous session's plan knowledge and seeds
// a fresh fetch, so the strip reflects the resumed session instead of
// leaking the old one (and never showing anything until a live plan call).
func TestPlanSurface_SessionResumeResetsAndRefetches(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{} // fetch closures are built but never executed
	m.sessionID = "s1"
	acceptPlan(m, planFixture())

	// Resume an interrupted session: transcript swap + adopt.
	cmd := m.handleSessionDetail(sessionDetailMsg{sess: client.Session{ID: "s2"}, token: "a2"})
	if cmd == nil {
		t.Fatal("resume must return the adopt/fetch command batch")
	}
	if m.planInit || m.planVer != 0 {
		t.Fatalf("resume must drop stale plan knowledge: init=%v ver=%d", m.planInit, m.planVer)
	}
	if m.planReqSeq == 0 {
		t.Fatal("resume must seed a plan fetch for the resumed session")
	}

	// Until the resumed session's snapshot arrives, the old plan must not
	// leak into the strip even while busy.
	m.busy = true
	if got := m.planStripLabel(); got != "" {
		t.Fatalf("stale pre-resume plan leaked into the strip: %q", got)
	}

	snap := planFixture()
	snap.SessionID = "s2"
	acceptPlan(m, snap)
	got := m.planStripLabel()
	for _, want := range []string{"plan 1/4", "wire flag parsing"} {
		if !strings.Contains(got, want) {
			t.Errorf("resumed-session strip %q missing %q", got, want)
		}
	}
}

func TestPlanWSTrigger_DebouncedRefresh(t *testing.T) {
	m := &Model{}
	before := m.planDebSeq
	m.handleEvent(planCallEvent("shell"))
	if m.planDebSeq != before {
		t.Fatal("non-plan tool_call must not arm the refresh")
	}

	m.handleEvent(planCallEvent("plan"))
	if m.planDebSeq != before {
		t.Fatal("plan tool_call must not fetch — the store is not written yet")
	}

	m.handleEvent(client.Event{Type: "tool_result", Name: "plan", Data: "{}"})
	if m.planDebSeq != before+1 {
		t.Fatalf("plan tool_result must arm a fresh window: %d → %d", before, m.planDebSeq)
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

func TestPlanMutation_UpdatesStripImmediately(t *testing.T) {
	m := newTestModel()
	m.sessionID = "s1"
	m.busy = true
	acceptPlan(m, planFixture())
	if got := m.planStripLabel(); !strings.Contains(got, "plan 1/4") || !strings.Contains(got, "wire flag parsing") {
		t.Fatalf("precondition strip = %q", got)
	}

	// complete the in-progress step: the strip must move on this frame,
	// not wait for REST.
	if !m.applyPlanMutation(`{"verb":"complete","step_id":"p2"}`) {
		t.Fatal("complete p2 should patch the snapshot")
	}
	got := m.planStripLabel()
	if !strings.Contains(got, "plan 2/4") {
		t.Errorf("strip after complete = %q, want plan 2/4", got)
	}
	if strings.Contains(got, "wire flag parsing") {
		t.Errorf("completed step still shown as active: %q", got)
	}

	if !m.applyPlanMutation(`{"verb":"update","updates":[{"id":"p4","status":"in_progress"}]}`) {
		t.Fatal("update p4 should patch the snapshot")
	}
	got = m.planStripLabel()
	if !strings.Contains(got, "live strip + /plan") {
		t.Errorf("strip after update = %q, want the new active title", got)
	}
}

func TestPlanMutation_CreateSeedsStrip(t *testing.T) {
	m := newTestModel()
	m.busy = true
	if !m.applyPlanMutation(`{"verb":"create","steps":[{"id":"a","title":"first task","status":"in_progress"},{"id":"b","title":"next"}]}`) {
		t.Fatal("create should seed a snapshot")
	}
	got := m.planStripLabel()
	for _, want := range []string{"plan 0/2", "first task"} {
		if !strings.Contains(got, want) {
			t.Errorf("create strip %q missing %q", got, want)
		}
	}
}

func TestPlanMutation_DeadEndpointIgnored(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m.planAvail = planUnavailable
	if m.applyPlanMutation(`{"verb":"create","steps":[{"id":"a","title":"ghost"}]}`) {
		t.Fatal("must not invent a plan when the route is dead")
	}
	if m.planStripLabel() != "" {
		t.Fatal("dead endpoint must keep the strip empty")
	}
}

func TestPlanPoll_LiveWhileBusy(t *testing.T) {
	m := &Model{sessionID: "s1", cl: &client.Client{}, busy: true}
	m.planAvail = planAvailable
	if c := m.armPlanPoll(); c == nil {
		t.Fatal("busy run must arm the live poll")
	}
	if c := m.handlePlanTick(planTickMsg{seq: m.planPollSeq}); c == nil {
		t.Fatal("live tick while busy must refetch (narration strip)")
	}
	m.busy = false
	if c := m.handlePlanTick(planTickMsg{seq: m.planPollSeq}); c != nil {
		t.Fatal("idle + closed tab must stop the live poll")
	}
}

func TestPlanToolCall_AppliesBeforeResult(t *testing.T) {
	m := newTestModel()
	m.sessionID = "s1"
	m.busy = true
	acceptPlan(m, planFixture())
	before := m.planDebSeq
	m.handleEvent(client.Event{
		Type: "tool_call", Name: "plan",
		Data: `{"verb":"complete","step_id":"p2"}`,
	})
	if m.planDebSeq != before {
		t.Fatal("tool_call must not schedule REST — that fetch races the store")
	}
	if !strings.Contains(m.planStripLabel(), "plan 2/4") {
		t.Fatalf("strip did not apply the call: %q", m.planStripLabel())
	}
	if !m.planDirty {
		t.Fatal("tool_call mutation must mark the snapshot dirty")
	}
}

func TestPlanMutation_StaleSnapshotIgnoredWhileDirty(t *testing.T) {
	m := newTestModel()
	m.sessionID = "s1"
	m.busy = true
	snap := planFixture()
	acceptPlan(m, snap)
	if !m.applyPlanMutation(`{"verb":"complete","step_id":"p2"}`) {
		t.Fatal("complete p2 should patch the snapshot")
	}

	// Non-confirm replies — even a newer version — are the pre-write
	// store (create leaves planVer behind). Only the tool_result
	// confirm fetch may land.
	newerPoll := snap
	newerPoll.Version = snap.Version + 1
	m.planReqSeq++
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, snap: newerPoll})
	if !m.planDirty {
		t.Fatal("non-confirm reply must leave the optimistic patch dirty")
	}
	if got := m.planStripLabel(); !strings.Contains(got, "plan 2/4") {
		t.Fatalf("in-flight poll reverted the strip: %q", got)
	}

	newer := snap
	newer.Version = snap.Version + 1
	newer.Steps = append([]client.PlanStep(nil), snap.Steps...)
	for i := range newer.Steps {
		if newer.Steps[i].ID == "p2" {
			newer.Steps[i].Status = client.PlanDone
		}
	}
	m.planReqSeq++
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, confirm: true, snap: newer})
	if m.planDirty || m.planVer != newer.Version {
		t.Fatalf("confirm must land: dirty=%v ver=%d", m.planDirty, m.planVer)
	}
	if got := m.planStripLabel(); !strings.Contains(got, "plan 2/4") {
		t.Fatalf("confirmed strip = %q, want plan 2/4", got)
	}
}

func TestPlanMutation_ConfirmRevertsRejectedWrite(t *testing.T) {
	m := newTestModel()
	m.sessionID = "s1"
	m.busy = true
	snap := planFixture()
	acceptPlan(m, snap)
	if !m.applyPlanMutation(`{"verb":"complete","step_id":"p2"}`) {
		t.Fatal("complete p2 should patch the snapshot")
	}
	// Engine rejected the write: store version unchanged. The confirm
	// fetch must still land so the strip does not stay on plan 2/4.
	// Fresh fixture — applyPlanMutation mutates the accepted slice in place.
	m.planReqSeq++
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, confirm: true, snap: planFixture()})
	if m.planDirty {
		t.Fatal("confirm of a rejected write must clear dirty")
	}
	if got := m.planStripLabel(); !strings.Contains(got, "plan 1/4") {
		t.Fatalf("rejected write must revert the strip: %q", got)
	}
}

func TestPlanMutation_CreateIgnoresInFlightSnapshot(t *testing.T) {
	m := newTestModel()
	m.sessionID = "s1"
	m.busy = true
	if !m.applyPlanMutation(`{"verb":"create","steps":[{"id":"a","title":"first task","status":"in_progress"}]}`) {
		t.Fatal("create should seed a snapshot")
	}
	// planVer is still 0; a kickPlanLive reply with a previous plan
	// (version > 0) must not overwrite the new steps.
	m.planReqSeq++
	m.handlePlanMsg(planMsg{want: "s1", seq: m.planReqSeq, snap: planFixture()})
	if !m.planDirty {
		t.Fatal("in-flight snapshot must not clear a dirty create")
	}
	if got := m.planStripLabel(); !strings.Contains(got, "first task") {
		t.Fatalf("in-flight snapshot overwrote create: %q", got)
	}
}

func TestPlanPoll_SkipsFetchWhileDirty(t *testing.T) {
	m := &Model{sessionID: "s1", cl: &client.Client{}, busy: true}
	m.planAvail = planAvailable
	m.planDirty = true
	if c := m.armPlanPoll(); c == nil {
		t.Fatal("busy run must arm the live poll")
	}
	req := m.planReqSeq
	c := m.handlePlanTick(planTickMsg{seq: m.planPollSeq})
	if c == nil {
		t.Fatal("dirty tick must re-arm rather than drop the chain")
	}
	if m.planReqSeq != req {
		t.Fatal("dirty tick must not issue a fetch (would supersede confirm)")
	}
}

func TestPlanFollowup_SendPromptArmsLivePoll(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{} // fetch closures are built but never executed
	m.sessionID = "s1"
	m.sendPrompt("keep the strip live")
	if !m.busy || !m.planLiveKick {
		t.Fatal("send must arm the live-poll flag (consumed on the first wire event)")
	}
	if c := m.planFollowup(); c == nil || m.planLiveKick {
		t.Fatal("planFollowup must consume the kick and return fetch+tick")
	}
}
