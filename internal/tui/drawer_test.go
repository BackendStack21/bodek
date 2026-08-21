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

// TestDrawerTabCycling verifies ]/[ and digit jumps move between drawer tabs
// and esc closes from any of them.
func TestDrawerTabCycling(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openRuns()))

	_, cmd := m.Update(key("]"))
	m.Update(exec(cmd))
	if m.panel != panelEvents {
		t.Errorf("] did not cycle to events: %d", m.panel)
	}
	_, cmd = m.Update(key("1"))
	m.Update(exec(cmd))
	if m.panel != panelSessions {
		t.Errorf("digit jump did not reach sessions: %d", m.panel)
	}
	m.Update(exec(m.fetchSessionsPage("", 0, false)))
	_, cmd = m.Update(key("["))
	m.Update(exec(cmd))
	if m.panel != panelEvents {
		t.Errorf("[ did not cycle back to events: %d", m.panel)
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
