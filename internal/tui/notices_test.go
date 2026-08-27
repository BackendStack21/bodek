package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// assertFadingNotice pins the transient-note contract for one posting path:
// the note exists, it carries an expiry (not sticky), the path returned the
// sweep cmd (an un-armed transient note lingers until unrelated activity),
// and after the TTL it is gone from the rendered transcript.
func assertFadingNotice(t *testing.T, m *Model, cmdReturned bool, substr string) {
	t.Helper()
	note, exp := lastNoteMatching(m, substr)
	if note == "" {
		t.Fatalf("notice %q not posted: %v", substr, m.notices)
	}
	if exp.IsZero() {
		t.Fatalf("notice %q is sticky — it would never disappear", note)
	}
	if !cmdReturned {
		t.Errorf("notice %q posted without the sweep cmd — nothing triggers its removal", note)
	}
	m.pruneNotices(time.Now().Add(2 * noticeTTL))
	if note2, _ := lastNoteMatching(m, substr); note2 != "" {
		t.Errorf("notice %q survived the TTL sweep", note2)
	}
}

// assertAlertDwell pins the alert-tier contract for one posting path: the
// note exists, it carries an expiry inside (noticeTTL, alertTTL] — errors
// must outlive the 3s info traces, yet nothing in the strip is sticky — the
// path armed the sweep (an un-armed note lingers on an idle TUI), and after
// alertTTL it is gone from the strip.
func assertAlertDwell(t *testing.T, m *Model, cmdArmed bool, substr string) {
	t.Helper()
	note, exp := lastNoteMatching(m, substr)
	if note == "" {
		t.Fatalf("notice %q not posted: %v", substr, m.notices)
	}
	if exp.IsZero() {
		t.Fatalf("notice %q is sticky — nothing in the strip may be sticky", note)
	}
	if dwell := time.Until(exp); dwell <= noticeTTL || dwell > alertTTL {
		t.Errorf("notice %q dwell = %v, want (noticeTTL, alertTTL]", note, dwell)
	}
	if !cmdArmed {
		t.Errorf("notice %q posted without arming the sweep", note)
	}
	m.pruneNotices(time.Now().Add(alertTTL + time.Second))
	if again, _ := lastNoteMatching(m, substr); again != "" {
		t.Errorf("notice %q survived alertTTL: %q", substr, again)
	}
}

// TestNoticesAutoclose is the regression for the never-disappearing
// "error: iteration 22: llm: stream idle…" notice: every addNote path —
// errors with and without an open turn, disconnects — posts into the strip
// as an alert that fades after alertTTL. Durable state stays in the header
// badge; the strip holds only bounded messages.
func TestNoticesAutoclose(t *testing.T) {
	// Error on a turn that already produced prose → strip note.
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "go"},
		message{role: roleAsst, content: "partial reply", streaming: true})
	m.curIdx = 1
	_, cmd := m.handleEvent(client.Event{Type: "error",
		Message: "iteration 22: llm: stream idle for over 1m0s without an event"})
	assertAlertDwell(t, m, cmd != nil, "error: iteration 22")

	// Error with no open turn → the other addNote path.
	m2 := newTestModel()
	_, cmd = m2.handleEvent(client.Event{Type: "error", Message: "boom"})
	assertAlertDwell(t, m2, cmd != nil, "error: boom")

	// Disconnect without a reconnect hook → the sticky-family notes.
	m3 := newTestModel()
	m3.opts.LogPath = "/tmp/bodek.log"
	_, cmd = m3.handleEvent(client.Event{Type: client.EventDisconnected})
	if cmd == nil {
		t.Error("disconnect notes posted without arming the sweep")
	}
	for _, substr := range []string{"disconnected from odek serve", "server log · /tmp/bodek.log"} {
		note, exp := lastNoteMatching(m3, substr)
		if note == "" {
			t.Fatalf("notice %q not posted: %v", substr, m3.notices)
		}
		if dwell := time.Until(exp); dwell <= noticeTTL || dwell > alertTTL {
			t.Errorf("notice %q dwell = %v, want (noticeTTL, alertTTL]", note, dwell)
		}
	}
	m3.pruneNotices(time.Now().Add(alertTTL + time.Second))
	if len(m3.notices) != 0 {
		t.Errorf("disconnect notes survived alertTTL: %v", m3.notices)
	}
}

// TestNoticeSweepRearms pins the sweep lifecycle: schedule at the earliest
// pending expiry, prune-and-rearm on every tick, stop once the strip is
// clean — so the timer chain cannot pile up or die early.
func TestNoticeSweepRearms(t *testing.T) {
	m := newTestModel()
	if cmd := m.noticeSweep(); cmd != nil {
		t.Error("empty strip must not arm the sweep")
	}
	m.addTransientNote("info trace")
	m.addNote("error: boom")
	if cmd := m.noticeSweep(); cmd == nil {
		t.Fatal("pending notices must arm the sweep")
	}
	// A tick landing between the two expiries drops the trace, keeps the
	// alert, and re-arms for the remaining expiry.
	m.noticeExp[0] = time.Now().Add(-time.Second)
	_, cmd := m.Update(noticeExpireMsg{})
	if len(m.notices) != 1 || m.notices[0] != "error: boom" {
		t.Fatalf("first sweep dropped the wrong notes: %v", m.notices)
	}
	if cmd == nil {
		t.Fatal("sweep must re-arm while an alert is still pending")
	}
	// Everything expired: the strip empties and the sweep stops.
	m.noticeExp[0] = time.Now().Add(-time.Second)
	if _, cmd := m.Update(noticeExpireMsg{}); cmd != nil {
		t.Error("clean strip must not re-arm the sweep")
	}
	if len(m.notices) != 0 {
		t.Errorf("expired alert survived the sweep: %v", m.notices)
	}
}

func lastNoteMatching(m *Model, substr string) (string, time.Time) {
	for i := len(m.notices) - 1; i >= 0; i-- {
		if strings.Contains(m.notices[i], substr) {
			return m.notices[i], m.noticeExp[i]
		}
	}
	return "", time.Time{}
}

// TestStatusConfirmationsFade drives every model-switch path plus the
// reconnect confirmation — the notices reported as never disappearing.
func TestStatusConfirmationsFade(t *testing.T) {
	// /model <name>
	m := newTestModel()
	cmd := m.runCommand("model", "glm-5.3")
	assertFadingNotice(t, m, cmd != nil, "model set to glm-5.3")
	if m.model != "glm-5.3" {
		t.Fatalf("/model did not apply: %q", m.model)
	}

	// Models panel selection.
	m2 := newTestModel()
	m2.panel = panelModels
	m2.models = []client.ModelInfo{{ID: "dsv4", Current: true}}
	cmd = m2.panelSelect()
	assertFadingNotice(t, m2, cmd != nil, "model set to dsv4")
	if m2.panel != panelNone {
		t.Error("panelSelect left the models panel open")
	}

	// Palette model entry.
	m3 := newTestModel()
	m3.models = []client.ModelInfo{{ID: "glm-x", Current: true}}
	var run func(*Model) tea.Cmd
	for _, e := range m3.basePaletteEntries() {
		if e.kind == "model" && strings.Contains(e.title, "glm-x") {
			run = e.run
		}
	}
	if run == nil {
		t.Fatal("palette has no glm-x model entry")
	}
	cmd = run(m3)
	assertFadingNotice(t, m3, cmd != nil, "model set to glm-x")

	// /thinking toggle.
	m4 := newTestModel()
	cmd = m4.runCommand("thinking", "on")
	assertFadingNotice(t, m4, cmd != nil, "thinking on")

	// Unknown command feedback.
	m5 := newTestModel()
	cmd = m5.runCommand("nosuch", "")
	assertFadingNotice(t, m5, cmd != nil, "unknown command")

	// Reconnect confirmation (against the live stand-in).
	m6 := wired(t)
	m6.disconn = true
	_, cmd = m6.Update(reconnectMsg{attempt: 0, cl: m6.cl})
	assertFadingNotice(t, m6, cmd != nil, "reconnected to odek serve")
}

// TestActionableNotesFade verifies the ⏎-retry hint after reconnects give
// up is alert-tier: it dwells past a glance away but autocloses like every
// strip note — the durable affordances are the header's disconnected badge
// and the footer's r-retry hint, not the strip.
func TestActionableNotesFade(t *testing.T) {
	m := wired(t)
	m.disconn = true
	m.Update(reconnectMsg{attempt: maxReconnectAttempts, err: errTest{}})
	note, exp := lastNoteMatching(m, "⏎ to retry")
	if note == "" {
		t.Fatalf("retry hint not posted: %v", m.notices)
	}
	if exp.IsZero() {
		t.Error("the retry hint should carry an expiry — nothing in the strip is sticky")
	}
	if !m.disconn {
		t.Error("the disconnected badge must outlive the fading hint")
	}
	m.pruneNotices(time.Now().Add(alertTTL + time.Second))
	if note2, _ := lastNoteMatching(m, "⏎ to retry"); note2 != "" {
		t.Errorf("retry hint survived alertTTL: %q", note2)
	}
}
