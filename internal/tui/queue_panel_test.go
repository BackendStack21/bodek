package tui

import (
	"strings"
	"testing"
)

// Tests for the /queue management panel: the panel reorders and deletes
// queued prompts behind the same gates as every other management surface,
// and every listed prompt renders collapsed to a single line.

// TestQueueCommandOpensPanel verifies /queue opens the dedicated management
// panel, that it replaces the strip while open, and that esc closes it.
func TestQueueCommandOpensPanel(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"one", "two", "three"}
	m.refresh()
	if !m.queueStripVisible() {
		t.Fatal("precondition: strip visible with queued prompts")
	}

	m.runCommandLine("/queue")
	if m.panel != panelQueue {
		t.Fatalf("panel = %v, want panelQueue", m.panel)
	}
	if got := m.panelLen(); got != 3 {
		t.Errorf("panelLen = %d, want 3", got)
	}
	if m.queueStripVisible() {
		t.Error("the strip must stand down while the /queue panel owns the queue")
	}
	if v := plain(m.View()); strings.Contains(v, "▲ ▼ ✕") {
		t.Error("View must not render strip controls while the panel is open")
	}

	m.Update(key("esc"))
	if m.panel != panelNone {
		t.Errorf("esc should close the panel, got %v", m.panel)
	}
}

// TestQueuePanelEmptyState opens the manager with nothing queued.
func TestQueuePanelEmptyState(t *testing.T) {
	m := newTestModel()
	m.runCommandLine("/queue")
	if m.panel != panelQueue {
		t.Fatalf("panel = %v, want panelQueue", m.panel)
	}
	if v := plain(m.View()); !strings.Contains(strings.ToLower(v), "empty") {
		t.Errorf("empty queue should say so, got %q", v)
	}
	// Every key must be a safe no-op with no rows to act on.
	for _, k := range []string{"j", "k", "d", "y", "h", "l", "enter"} {
		m.Update(key(k))
	}
	if len(m.queue) != 0 || m.confirm != confirmNone {
		t.Errorf("empty-panel keys must not mutate state: queue=%v confirm=%v", m.queue, m.confirm)
	}
}

// TestQueuePanelReorderPriority drives priority changes: ←/h raises a prompt
// (sends sooner), →/l lowers it, and the selection rides the moved item.
func TestQueuePanelReorderPriority(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"a", "b", "c"}
	m.runCommandLine("/queue")

	m.Update(key("down"))
	m.Update(key("down"))
	if m.panelSel != 2 {
		t.Fatalf("panelSel = %d, want 2", m.panelSel)
	}

	// ← raises "c" above "b": send order becomes a, c, b.
	m.Update(key("left"))
	if strings.Join(m.queue, ",") != "a,c,b" {
		t.Errorf("after ←: queue = %v, want [a c b]", m.queue)
	}
	if m.panelSel != 1 {
		t.Errorf("selection must ride the moved item, got %d want 1", m.panelSel)
	}

	// h is the same verb; → l lowers past the tail.
	m.Update(key("h"))
	if strings.Join(m.queue, ",") != "c,a,b" {
		t.Errorf("after h: queue = %v, want [c a b]", m.queue)
	}
	m.Update(key("right"))
	m.Update(key("l"))
	if strings.Join(m.queue, ",") != "a,b,c" {
		t.Errorf("after → l: queue = %v, want [a b c]", m.queue)
	}

	// The head is already top priority — ← there is a no-op.
	m.Update(key("up"))
	m.Update(key("up"))
	m.Update(key("left"))
	if strings.Join(m.queue, ",") != "a,b,c" {
		t.Errorf("← at the head must not reorder, got %v", m.queue)
	}
}

// TestQueuePanelDeleteGate exercises the two-step delete: d arms the gate,
// any other key disarms, y confirms, and the selection clamps.
func TestQueuePanelDeleteGate(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"keep", "doomed", "after"}
	m.runCommandLine("/queue")

	m.Update(key("down")) // select "doomed"
	m.Update(key("d"))
	if m.confirm != confirmQueueDelete {
		t.Fatalf("d must arm the queue delete gate, got %v", m.confirm)
	}
	if msg := strings.ToLower(m.panelMsg); !strings.Contains(msg, "delete") {
		t.Errorf("gate must say what it arms, got %q", m.panelMsg)
	}

	// Any other key disarms — navigation must not delete through the gate.
	m.Update(key("k"))
	if m.confirm != confirmNone {
		t.Fatal("navigation must disarm the gate")
	}
	if len(m.queue) != 3 {
		t.Fatalf("disarmed gate deleted a row: %v", m.queue)
	}

	m.Update(key("d"))
	m.Update(key("y"))
	if strings.Join(m.queue, ",") != "keep,after" {
		t.Errorf("y should delete the selected prompt, got %v", m.queue)
	}
	if m.panelSel != 1 {
		t.Errorf("selection must clamp onto the successor, got %d", m.panelSel)
	}
	if m.qsel < 0 || m.qsel >= len(m.queue) {
		t.Errorf("strip selection must stay in range: %d of %d", m.qsel, len(m.queue))
	}
}

// TestQueuePanelEnterSendsSelected makes ⏎ the "this one next" verb: idle it
// sends the selected prompt immediately; mid-turn it refuses and teaches.
func TestQueuePanelEnterSendsSelected(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"head", "chosen", "tail"}
	m.runCommandLine("/queue")
	m.Update(key("down"))

	m.Update(key("enter"))
	if !m.busy {
		t.Fatal("mid-turn ⏎ must not start a turn")
	}
	if strings.Join(m.queue, ",") != "head,chosen,tail" {
		t.Errorf("mid-turn ⏎ must keep the queue intact, got %v", m.queue)
	}
	if m.panel != panelQueue {
		t.Error("mid-turn ⏎ should leave the panel open")
	}

	// Settle the turn, then ⏎ sends the selected prompt now.
	m.busy = false
	m.Update(key("enter"))
	if !m.busy {
		t.Error("idle ⏎ on a queued prompt must send it")
	}
	if m.lastPrompt != "chosen" {
		t.Errorf("sent the wrong prompt: %q", m.lastPrompt)
	}
	if strings.Join(m.queue, ",") != "head,tail" {
		t.Errorf("sent prompt must leave the queue, got %v", m.queue)
	}
	if m.panel != panelNone {
		t.Error("the panel must close when the turn starts — the transcript is the point")
	}
}

// TestQueuePanelDrainClampsSelection pins the drain-vs-panel race: a turn
// ending (or a reconnect) drains the queue head while the panel is open —
// the panel selection must clamp, never strand past the last row.
func TestQueuePanelDrainClampsSelection(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"head", "mid", "tail"}
	m.runCommandLine("/queue")
	m.Update(key("down"))
	m.Update(key("down"))
	if m.panelSel != 2 {
		t.Fatalf("panelSel = %d, want 2", m.panelSel)
	}

	// The turn ends: the head drains and sends under the open panel.
	// (The returned send cmd needs a live client — the state change is
	// what's under test, so the cmd is dropped, mirroring Update().)
	m.busy = false
	_ = m.sendQueued()
	if strings.Join(m.queue, ",") != "mid,tail" {
		t.Fatalf("drain left %v", m.queue)
	}
	if m.panelSel != 1 {
		t.Errorf("panel selection must clamp to %d after the drain, got %d", len(m.queue)-1, m.panelSel)
	}

	// Draining everything leaves the panel empty and selectable.
	m.busy = false
	_ = m.sendQueued()
	m.busy = false
	_ = m.sendQueued()
	if len(m.queue) != 0 || m.panelLen() != 0 {
		t.Fatalf("queue not drained: %v", m.queue)
	}
	if m.panelSel != 0 {
		t.Errorf("empty-panel selection must rest at 0, got %d", m.panelSel)
	}
}

// TestQueuePanelSendRefusals pins the two guarded send-now paths.
func TestQueuePanelSendRefusals(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"one"}
	m.runCommandLine("/queue")

	// Mid-turn: refused, queue intact, panel stays.
	if cmd := m.queueSendSelected(); cmd == nil {
		t.Fatal("mid-turn refusal must acknowledge with a note cmd")
	}
	if !m.busy || len(m.queue) != 1 || m.panel != panelQueue {
		t.Fatalf("mid-turn send mutated state: busy=%v queue=%v panel=%v", m.busy, m.queue, m.panel)
	}

	// Disconnected: refused the same way.
	m.busy = false
	m.disconn = true
	if cmd := m.queueSendSelected(); cmd == nil {
		t.Fatal("disconnected refusal must acknowledge with a note cmd")
	}
	if len(m.queue) != 1 || m.panel != panelQueue {
		t.Fatalf("disconnected send mutated state: queue=%v panel=%v", m.queue, m.panel)
	}
}

// TestQueuePanelRowsOneLine applies the one-line rule inside the panel: every
// listed prompt renders collapsed to a single row.
func TestQueuePanelRowsOneLine(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.queue = []string{"alpha\nbeta", "gamma\ndelta\nepsilon"}
	m.runCommandLine("/queue")

	rows := m.queuePanelRows(80)
	if len(rows) != 2 {
		t.Fatalf("panel rows = %d, want 2 (one per queued prompt)", len(rows))
	}
	for i, r := range rows {
		if strings.Contains(r, "\n") {
			t.Errorf("row %d spans multiple lines: %q", i, plain(r))
		}
	}
	v := plain(strings.Join(rows, "\n"))
	for _, want := range []string{"alpha beta", "gamma delta epsilon"} {
		if !strings.Contains(v, want) {
			t.Errorf("panel rows missing collapsed %q: %q", want, v)
		}
	}
}
