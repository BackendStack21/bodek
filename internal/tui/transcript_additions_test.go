package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── A1: reduced-motion mode ─────────────────────────────────────────────────

// TestReduceMotionClockInterval: with reduceMotion on, the transcript clock
// lane must tick at >= 2s instead of the default 250ms — a 1s advance must
// not repaint the head counter, a 2s advance must.
func TestReduceMotionClockInterval(t *testing.T) {
	m := newTestModel()
	if d := m.tailClockTick(); d != tailClockInterval {
		t.Errorf("default cadence must stay %v, got %v", tailClockInterval, d)
	}
	m.reduceMotion = true
	if d := m.tailClockTick(); d < 2*time.Second {
		t.Errorf("reduceMotion cadence must be >= 2s, got %v", d)
	}
}

// TestReduceMotionSteadyOutputRow: reduceMotion disables the new-output row's
// busy accent — it renders the same dim style as the idle placeholder.
func TestReduceMotionSteadyOutputRow(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m.reduceMotion = true
	tallTranscript(m)
	m.vp.GotoTop()
	if m.vp.AtBottom() {
		t.Fatal("precondition: scrolled away from the bottom")
	}
	if m.outputRowAccent() {
		t.Error("reduceMotion must keep the new-output row steady (no accent)")
	}
	if foot := plain(m.footer()); !strings.Contains(foot, "↓ new output") {
		t.Errorf("new-output row missing its dim render under reduceMotion: %q", foot)
	}
	// The busy accent returns once the mode is off.
	m.reduceMotion = false
	if !m.outputRowAccent() {
		t.Error("busy turns outside reduceMotion must accent the row")
	}
}

// ── A2: folded turn summary tally ───────────────────────────────────────────

// TestFoldedTurnTally: a finalized assistant head carries the compact
// 'N tools · M agents · T' tally; zero-step turns drop the tools segment.
func TestFoldedTurnTally(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "go"},
		message{role: roleAsst, streaming: false},
	)
	i := 1
	msg := &m.msgs[i]
	msg.steps = append(msg.steps,
		step{name: "read_file", done: true, dur: 1200 * time.Millisecond},
		step{name: "shell", done: true, dur: 800 * time.Millisecond},
	)
	msg.steps[0].agents = append(msg.steps[0].agents, &agentCard{})
	msg.items = append(msg.items,
		turnItem{stepIdx: 0},
		turnItem{stepIdx: 1},
	)

	rendered, _ := m.renderMessage(m.msgs[i], i, 0)
	head := plain(rendered)
	if !strings.Contains(head, "2 tools") {
		t.Errorf("folded head missing tool tally: %q", head)
	}
	if !strings.Contains(head, "1 agent") {
		t.Errorf("folded head missing agent tally: %q", head)
	}
	if !strings.Contains(head, "2.0s") {
		t.Errorf("folded head missing duration segment: %q", head)
	}

	// Sub-1s sealed total renders as '<1s'.
	msg.steps[0].dur = 300 * time.Millisecond
	msg.steps[1].dur = 400 * time.Millisecond
	rendered, _ = m.renderMessage(m.msgs[i], i, 0)
	if !strings.Contains(plain(rendered), "<1s") {
		t.Errorf("sub-1s tally must render '<1s': %q", plain(rendered))
	}

	// Zero steps: no tools segment at all.
	msg.steps = nil
	msg.items = nil
	rendered, _ = m.renderMessage(m.msgs[i], i, 0)
	head = plain(rendered)
	if strings.Contains(head, "tools") {
		t.Errorf("zero-step turn must not show a tools segment: %q", head)
	}
}

// ── A3: terminal bell on failure and approval urgency ──────────────────────

// TestFailureBellOnce: a failed turn rings the bell exactly once — the guard
// flag latches and a second error event does not re-fire.
func TestFailureBellOnce(t *testing.T) {
	m := newTestModel()
	m.bell = true
	busyTurn(m)
	m.handleEvent(client.Event{Type: "error", Message: "boom"})
	if !m.failBellFired {
		t.Fatal("failure must latch the bell guard (fired once)")
	}
	if !m.msgs[1].failed {
		t.Fatal("precondition: the error marked the turn failed")
	}
	a := m.attentionFor(attentionFailed)
	if !a.bell {
		t.Error("failure attention plan must carry the bell")
	}
	// Second failure on the same turn must not re-fire.
	m.handleEvent(client.Event{Type: "error", Message: "boom again"})
	if !m.failBellFired {
		t.Error("bell guard must stay latched for the same turn")
	}
}

// TestApprovalUrgentBellOnce: crossing into the urgent window (<10s) rings
// once per approval; ticks inside the window do not re-fire, and a new
// approval re-arms.
func TestApprovalUrgentBellOnce(t *testing.T) {
	m := newTestModel()
	m.bell = true
	ev := client.Event{Type: "approval_request", ID: "a1"}
	m.approvals = append(m.approvals, ev)
	m.stampApprovalDeadline(client.Event{ID: "a1", TimeoutSeconds: 60})
	// Shrink the deadline into the urgent window.
	m.apprDeadlines[0] = time.Now().Add(9 * time.Second)

	m.handleApprovalExpiry(time.Now())
	if !m.apprBellFired {
		t.Fatal("urgent countdown must latch the bell guard (fired once)")
	}
	// A further tick in the same window does not re-fire.
	m.handleApprovalExpiry(time.Now())
	if !m.apprBellFired {
		t.Error("bell guard must stay latched inside the window")
	}
	// A fresh approval re-arms the transition.
	m.approvals = append(m.approvals, client.Event{Type: "approval_request", ID: "a2"})
	m.apprDeadlines = append(m.apprDeadlines, time.Now().Add(9*time.Second))
	m.stampApprovalDeadline(client.Event{ID: "a2", TimeoutSeconds: 60})
	if m.apprBellFired {
		t.Error("a new approval must reset the urgent-bell guard")
	}
}
