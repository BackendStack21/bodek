package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── F1: the queue count has a single owner — the shelf chip ────────────────

// TestQueueCountSingleOwner: with prompts queued mid-turn, the shelf chip is
// the ONLY surface carrying the count — the status line and the footer must
// not repeat it.
func TestQueueCountSingleOwner(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	for _, p := range []string{"first follow-up", "second follow-up"} {
		m.ta.SetValue(p)
		m.submit()
	}
	if shelf := plain(m.shelfView()); !strings.Contains(shelf, "2 queued") {
		t.Errorf("shelf chip missing the queue count: %q", shelf)
	}
	if line := plain(m.statusLine()); strings.Contains(line, "queued") {
		t.Errorf("status line must not repeat the queue count: %q", line)
	}
	if foot := plain(m.footer()); strings.Contains(foot, "queued") {
		t.Errorf("footer must not repeat the queue count: %q", foot)
	}
}

// ── F2: the status line never hides on disconnect ──────────────────────────

// TestStatusLineReconnectState: while disconnected the status line renders
// the reconnect state in-place instead of vanishing — and never shows the
// normal busy spinner label.
func TestStatusLineReconnectState(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m.disconn = true
	m.status = "reconnecting…"
	m.reconnAttempt = 0

	line := plain(m.statusLine())
	if !strings.Contains(line, "reconnecting") || !strings.Contains(line, "backoff") {
		t.Errorf("disconnected status line missing reconnect state: %q", line)
	}
	for _, banned := range []string{"reasoning", "composing"} {
		if strings.Contains(line, banned) {
			t.Errorf("disconnected status line shows busy label %q: %q", banned, line)
		}
	}
	if !m.statusLineVisible() {
		t.Error("status line must keep its row while disconnected")
	}

	// Budget spent: the row keeps the terminal disconnected state.
	m.status = "disconnected"
	line = plain(m.statusLine())
	if !strings.Contains(line, "disconnected") {
		t.Errorf("terminal-disconnect status line missing state: %q", line)
	}
	if m.statusLine() != "" && !strings.HasPrefix(m.statusLine(), "\n") {
		t.Error("status line must keep its leading separator row")
	}
}

// ── F3: one steady new-output row — no insert/remove reflow ────────────────

// TestNewOutputRowSteady: the new-output indicator lives on ONE footer row
// that never inserts or removes a line — the layout height must not change
// when the busy state toggles while scrolled up.
func TestNewOutputRowSteady(t *testing.T) {
	m := newTestModel()
	tallTranscript(m)
	m.vp.GotoTop()

	m.busy = true
	if foot := plain(m.footer()); !strings.Contains(foot, "new output") {
		t.Errorf("busy scrolled-up footer missing new-output: %q", foot)
	}
	if shelf := plain(m.shelfView()); strings.Contains(shelf, "new output") {
		t.Errorf("shelf must not duplicate the new-output row: %q", shelf)
	}
	busyFoot := m.footer()
	busyShelf := m.shelfHeight()

	m.busy = false
	if !strings.Contains(plain(m.footer()), "new output") {
		t.Errorf("idle scrolled-up footer dropped the steady row placeholder")
	}
	// No layout change on the toggle: the row persists in-place (color-only
	// pulse), so the footer stays a single row and the shelf never grows.
	if lineCount(m.footer()) != lineCount(busyFoot) {
		t.Errorf("footer row count changed on toggle: %d → %d", lineCount(busyFoot), lineCount(m.footer()))
	}
	if m.shelfHeight() != busyShelf {
		t.Errorf("shelf height changed on new-output toggle: %d → %d", busyShelf, m.shelfHeight())
	}
}

// ── F4: a failed turn marks its head ───────────────────────────────────────

// TestFailedTurnHeadMarked: an error event on the streaming turn sets a
// sanitized failed flag that paints ✗ on the turn head — and survives
// finalization (history rendering keeps it).
func TestFailedTurnHeadMarked(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "do it"},
		message{role: roleAsst, content: "partial", streaming: true},
	)
	m.curIdx = 1
	m.busy = true
	m.runStart = time.Now()

	m.handleEvent(client.Event{Type: "error", Message: "boom"})
	if !m.msgs[1].failed {
		t.Fatal("error event must mark the turn message failed")
	}
	out, _ := m.renderMessage(m.msgs[1], 1, 0)
	if !strings.Contains(plain(out), "✗") {
		t.Errorf("failed turn head missing ✗:\n%s", plain(out))
	}

	// Finalized (history) rendering keeps the mark.
	m.msgs[1].streaming = false
	m.busy = false
	out, _ = m.renderMessage(m.msgs[1], 1, 0)
	if !strings.Contains(plain(out), "✗") {
		t.Errorf("finalized failed turn head lost ✗:\n%s", plain(out))
	}

	// A healthy turn never carries it.
	m.msgs[1].failed = false
	out, _ = m.renderMessage(m.msgs[1], 1, 0)
	if strings.Contains(plain(out), "✗") {
		t.Errorf("healthy turn head carries ✗:\n%s", plain(out))
	}
}

// ── F5: visible notices are capped to one line ─────────────────────────────

// TestNoticeCapOneLine: only the latest unexpired notice renders, folded into
// a single line; older ones collapse into a count instead of stacking.
func TestNoticeCapOneLine(t *testing.T) {
	m := newTestModel()
	m.addNote("first problem")
	m.addNote("second problem")
	m.addNote("latest problem")

	out := plain(m.renderNotices())
	if !strings.Contains(out, "latest problem") {
		t.Errorf("latest notice must render: %q", out)
	}
	if strings.Contains(out, "first problem") || strings.Contains(out, "second problem") {
		t.Errorf("older notices must not stack: %q", out)
	}
	if !strings.Contains(out, "2 notes") {
		t.Errorf("folded notice count missing: %q", out)
	}
	if n := lineCount(out); n != 1 {
		t.Errorf("notice strip must be one line, got %d", n)
	}

	// Expiry still sweeps under the cap.
	m.pruneNotices(time.Now().Add(alertTTL + time.Minute))
	if len(m.notices) != 0 {
		t.Errorf("sweep must prune expired notices, got %v", m.notices)
	}
}
