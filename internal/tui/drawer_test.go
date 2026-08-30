package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestDrawerRunsTab opens the runs tab via /runs, verifies the rows render
// (status glyphs, approval badge), and drives a remote approval + cancel.
func TestDrawerRunsTab(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openRuns()))
	if m.panel != panelRuns {
		t.Fatalf("panel = %d, want runs", m.panel)
	}
	if len(m.runs) != 2 {
		t.Fatalf("runs = %d", len(m.runs))
	}
	out := plain(m.View())
	for _, want := range []string{"runs", "sessions", "events", "⏳", "waiting_approval", "✓", "approval"} {
		if !strings.Contains(out, want) {
			t.Errorf("runs tab missing %q:\n%s", want, out)
		}
	}

	// A/D/T answer the highlighted run's pending approval; a deny flows to
	// the remote bridge and refreshes the list.
	_, cmd := m.Update(key("d"))
	m.Update(exec(cmd))
	m.Update(exec(nil)) // runActionMsg → refetch
	if len(m.runs) != 2 {
		t.Errorf("runs refresh lost entries: %d", len(m.runs))
	}

	// Trust without allow_trust never fires.
	m.runs[1].PendingApprovals = []client.RunApproval{{ID: "ap-9", AllowTrust: false}}
	m.panelSel = 1
	if cmd := m.answerSelectedRunApproval("trust"); cmd != nil {
		t.Error("trust answered without allow_trust")
	}

	// Cancel targets only non-terminal runs.
	m.panelSel = 0 // waiting_approval
	if cmd := m.cancelSelectedRun(); cmd == nil {
		t.Error("cancel did not fire for a running run")
	}
	m.panelSel = 1 // completed
	if cmd := m.cancelSelectedRun(); cmd != nil {
		t.Error("cancel fired for a terminal run")
	}
}

// TestDrawerEventsTab verifies the events feed renders the ring with tools
// and timestamps.
func TestDrawerEventsTab(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openEvents()))
	if m.panel != panelEvents {
		t.Fatalf("panel = %d, want events", m.panel)
	}
	if len(m.feed) != 3 {
		t.Fatalf("feed = %d", len(m.feed))
	}
	out := plain(m.View())
	for _, want := range []string{"run_started", "tool_call_started", "shell"} {
		if !strings.Contains(out, want) {
			t.Errorf("events tab missing %q:\n%s", want, out)
		}
	}
}

// TestDrawerTabCycling verifies ]/[ and digit jumps move between ALL nine
// drawer tabs (management panels included — they are full tabs, not loose
// overlays) and esc closes from any of them.
func TestDrawerTabCycling(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openRuns()))

	// ] walks the full ring: runs → agents → events → plan → memory →
	// skills → tools → config → sessions.
	want := []panelMode{panelAgents, panelEvents, panelPlan, panelMemory, panelSkills, panelTools, panelConfig, panelSessions}
	for _, w := range want {
		_, cmd := m.Update(key("]"))
		m.Update(exec(cmd))
		if m.panel != w {
			t.Fatalf("] chain: panel = %d, want %d", m.panel, w)
		}
	}
	// [ wraps back: sessions → config.
	_, cmd := m.Update(key("["))
	m.Update(exec(cmd))
	if m.panel != panelConfig {
		t.Errorf("[ did not wrap to config: %d", m.panel)
	}
	// Digits jump straight to any tab.
	for d, w := range map[string]panelMode{
		"1": panelSessions, "2": panelRuns, "3": panelAgents, "4": panelEvents,
		"5": panelPlan, "6": panelMemory, "7": panelSkills, "8": panelTools,
		"9": panelConfig,
	} {
		_, cmd := m.Update(key(d))
		m.Update(exec(cmd))
		if m.panel != w {
			t.Errorf("digit %s: panel = %d, want %d", d, m.panel, w)
		}
	}
	// The strip renders every tab name, and r refreshes a management tab
	// the same as a core tab (they are drawer tabs now).
	out := plain(m.View())
	for _, name := range []string{"sessions", "runs", "agents", "events", "plan", "memory", "skills", "tools", "config"} {
		if !strings.Contains(out, name) {
			t.Errorf("tab strip missing %q:\n%s", name, out)
		}
	}
	m.Update(exec(m.fetchSessionsPage("", 0, false)))
	_, cmd = m.Update(key("6")) // memory tab (agents shifted the digits)
	m.Update(exec(cmd))
	_, cmd = m.Update(key("r"))
	if cmd == nil {
		t.Error("r did not refresh the memory tab")
	} else {
		m.Update(exec(cmd))
	}
	// Enter no longer refreshes — it expands the selected row into the
	// detail view (the readable half of the promote gate). Esc folds the
	// detail; a second esc closes the drawer.
	m.Update(key("enter"))
	if !m.panelDetail {
		t.Error("enter did not open the memory detail view")
	}
	m.Update(key("esc"))
	if m.panelDetail {
		t.Error("esc did not fold the detail view")
	}
	if m.panel != panelMemory {
		t.Errorf("folding the detail left the tab: %d", m.panel)
	}
	m.Update(key("esc"))
	if m.panel != panelNone {
		t.Error("esc did not close the drawer")
	}
}

// TestRunsPollStopsOnClose verifies a stale poll tick is dropped after the
// tab closes (no zombie fetches).
func TestRunsPollStopsOnClose(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openRuns()))
	seq := m.runsSeq
	m.closePanel()
	if cmd := m.handleRunsTick(runsTickMsg{seq: seq}); cmd != nil {
		t.Error("stale tick after close re-armed the poll")
	}
}

// TestRunsEventsDrillIn verifies e on the runs tab jumps to the events tab
// scoped to that run (the run_id filter reaches the server), the filter is
// visible in the title, f replaces it with the session filter, and x
// clears back to the full ring.
func TestRunsEventsDrillIn(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openRuns()))
	m.panelSel = 0 // run-1 (waiting_approval)

	_, cmd := m.Update(key("e"))
	m.Update(exec(cmd)) // eventsMsg
	if m.panel != panelEvents {
		t.Fatalf("e did not drill into events: panel = %d", m.panel)
	}
	if m.evRunFilter != "run-1" {
		t.Fatalf("run filter = %q, want run-1", m.evRunFilter)
	}
	if standInSaw.lastEventRunID != "run-1" {
		t.Fatalf("server saw run_id %q, want run-1", standInSaw.lastEventRunID)
	}
	if len(m.feed) != 2 { // run-1's two events, run-2 excluded
		t.Fatalf("filtered feed = %d events, want 2", len(m.feed))
	}
	if out := plain(m.View()); !strings.Contains(out, "run run-1") {
		t.Errorf("run filter not shown in the title:\n%s", out)
	}

	// f swaps in the session filter (mutually exclusive).
	_, cmd = m.Update(key("f"))
	m.Update(exec(cmd))
	if m.evRunFilter != "" || !m.evSessionFilter {
		t.Fatalf("f must replace the run filter: run=%q session=%v", m.evRunFilter, m.evSessionFilter)
	}

	// x clears every filter and refetches the whole ring.
	_, cmd = m.Update(key("x"))
	m.Update(exec(cmd))
	if m.evRunFilter != "" || m.evSessionFilter {
		t.Fatal("x did not clear the filters")
	}
	if len(m.feed) != 3 {
		t.Fatalf("cleared feed = %d events, want 3", len(m.feed))
	}
}

// TestRunsApprovalsRefresh verifies p re-reads the highlighted run's
// pending approvals through the dedicated endpoint and patches the row.
func TestRunsApprovalsRefresh(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openRuns()))
	m.panelSel = 0
	m.runs[0].PendingApprovals = nil // simulate a stale queue

	_, cmd := m.Update(key("p"))
	m.Update(exec(cmd)) // runApprovalsMsg
	if len(m.runs[0].PendingApprovals) != 1 || m.runs[0].PendingApprovals[0].ID != "ap-1" {
		t.Fatalf("approvals refresh = %+v, want ap-1", m.runs[0].PendingApprovals)
	}
}
