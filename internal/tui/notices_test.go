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

// TestActionableNotesStaySticky verifies the flip side: notes the user must
// act on (the ⏎-retry hint after reconnects give up) never fade.
func TestActionableNotesStaySticky(t *testing.T) {
	m := wired(t)
	m.disconn = true
	m.Update(reconnectMsg{attempt: maxReconnectAttempts, err: errTest{}})
	note, exp := lastNoteMatching(m, "⏎ to retry")
	if note == "" {
		t.Fatalf("retry hint not posted: %v", m.notices)
	}
	if !exp.IsZero() {
		t.Error("the retry hint must stay sticky until acted on")
	}
	m.pruneNotices(time.Now().Add(time.Hour))
	if note2, _ := lastNoteMatching(m, "⏎ to retry"); note2 == "" {
		t.Error("sticky retry hint was pruned")
	}
}
