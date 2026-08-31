package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// Tests for the drawer Plan tab surface: opener states, Telegram-parity rows + summary badge, detail submode per
// house grammar (⏎ expand / esc fold), and the silent-degrade empty states.
// Snapshots are injected through handlePlanMsg so no server is involved.

var errPlanRoute = errors.New("session plan: status 404 Not Found")

func planFixture() client.PlanSnapshot {
	return client.PlanSnapshot{
		SessionID: "s1", Version: 7, Found: true,
		Steps: []client.PlanStep{
			{ID: "p1", Title: "scaffold command skeleton", Status: client.PlanDone},
			{ID: "p2", Title: "wire flag parsing", Status: client.PlanInProgress},
			{ID: "p3", Title: "resolve config precedence", Status: client.PlanBlocked,
				Note: "license gate needs a human decision before merge"},
			{ID: "p4", Title: "live strip + /plan", Status: client.PlanPending},
		},
	}
}

// acceptPlan feeds one snapshot as the newest in-flight reply and returns
// handlePlanMsg's re-arm command (non-nil while the tab is visible).
func acceptPlan(m *Model, snap client.PlanSnapshot) tea.Cmd {
	m.planReqSeq++
	return m.handlePlanMsg(planMsg{want: m.sessionID, seq: m.planReqSeq, snap: snap})
}

func TestPlanTab_OpenerAndRenderedRows(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{} // fetch closures are built but never executed
	m.sessionID = "s1"

	if c := m.openPlan(); c == nil {
		t.Fatal("openPlan must issue the initial fetch")
	}
	if m.panel != panelPlan || m.panelMsg != "loading plan…" {
		t.Fatalf("opener state = panel %d msg %q", m.panel, m.panelMsg)
	}

	before := m.planPollSeq
	if rearm := acceptPlan(m, planFixture()); rearm == nil || m.planPollSeq == before {
		t.Fatal("accepting while the tab is visible must re-arm the poll")
	}
	if m.panelMsg != "" {
		t.Fatalf("loaded tab shows status %q", m.panelMsg)
	}

	out := plain(m.View())
	for _, want := range []string{
		"plan", "v7 · 1/4 done · 1 blocked",
		"✅ p1", "🔄 p2", "⛔ p3", "⬜ p4",
		"wire flag parsing", "resolve config precedence",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan tab missing %q", want)
		}
	}
	for _, banned := range []string{"{", "verb"} { // no raw JSON on the tab
		if strings.Contains(out, banned) {
			t.Errorf("plan tab leaked raw payload (%q)", banned)
		}
	}
}

func TestPlanTab_UnattachedOpenerNeverShowsEternalLoading(t *testing.T) {
	// Regression: opening the tab with no live session (no client / no
	// session id) used to render "loading plan…" forever — fetchPlan was
	// a silent no-op, so no reply ever resolved the placeholder.
	m := newTestModel()
	if c := m.openPlan(); c != nil {
		t.Fatal("unattached opener must not issue a fetch")
	}
	if m.panel != panelPlan {
		t.Fatalf("opener panel = %d", m.panel)
	}
	if strings.Contains(m.panelMsg, "loading") {
		t.Fatalf("unattached tab shows eternal loading copy %q", m.panelMsg)
	}
	if got := plain(m.View()); !strings.Contains(got, "no active session") {
		t.Errorf("unattached copy missing from view:\n%s", got)
	}
}

func TestPlanTab_DetailFoldHouseGrammar(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{}
	m.sessionID = "s1"
	m.openPlan()
	if c := acceptPlan(m, planFixture()); c == nil {
		t.Fatal("precondition: accept should re-arm on a visible tab")
	}

	m.panelSel = 2 // the blocked step with the note
	m.Update(key("enter"))
	if !m.panelDetail {
		t.Fatal("enter did not open the step detail")
	}
	out := plain(m.View())
	if !strings.Contains(out, "license gate needs a human decision") {
		t.Errorf("detail missing full note:\n%s", out)
	}
	m.Update(key("q")) // q folds like esc (house rule)
	if m.panelDetail {
		t.Fatal("q did not fold the detail")
	}
	if m.panel != panelPlan {
		t.Fatalf("folding left the tab: %d", m.panel)
	}
	m.Update(key("esc"))
	if m.panel != panelNone {
		t.Error("esc did not close the drawer")
	}
}

func TestPlanTab_EmptyAndDegradedStates(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{}
	m.sessionID = "s1"
	m.openPlan()

	// Collapsed all-done plan: newer version, found with zero steps — the
	// header badge (✓ all done · vN) is the collapsed indicator, engine parity.
	allDone := planFixture()
	allDone.Version = 9
	allDone.Steps = []client.PlanStep{}
	acceptPlan(m, allDone)
	if got := plain(m.View()); !strings.Contains(got, "v9 · ✓ all done") {
		t.Errorf("collapsed badge wrong:\n%s", got)
	}

	// found:false — the transcript carries no parseable plan at all.
	acceptPlan(m, client.PlanSnapshot{SessionID: "s1", Version: 10, Found: false})
	if got := plain(m.View()); !strings.Contains(got, "no active plan in this session.") {
		t.Errorf("found:false copy wrong:\n%s", got)
	}

	// Endpoint failure degrades silently; polling drains on the dead route.
	acceptPlanErr(m)
	if got := plain(m.View()); !strings.Contains(got, "plan unavailable on this engine") {
		t.Errorf("degraded state copy wrong:\n%s", got)
	}
	m.planPollSeq++
	if c := m.handlePlanTick(planTickMsg{seq: m.planPollSeq}); c != nil {
		t.Fatal("unavailable endpoint must drain the visible poll")
	}
}

func acceptPlanErr(m *Model) {
	m.planReqSeq++
	m.handlePlanMsg(planMsg{want: m.sessionID, seq: m.planReqSeq, err: errPlanRoute})
}
