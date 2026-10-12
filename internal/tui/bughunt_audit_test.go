package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// Lone 8-bit CSI/OSC bytes (invalid UTF-8) and invisible tag characters
// must not slip through sanitize's fast path.
func TestSanitizeInvalidUTF8AndInvisibles(t *testing.T) {
	for _, in := range []string{"ok\x9b2J", "ok\x9d0;title\x07", "a\U000E0041b", "a\u061cb", "a\u2062b", "a\ufffab"} {
		out := sanitize(in)
		// ContainsAny would decode a raw 0x9b as U+FFFD and match the
		// (correct) replacement, so check bytes and runes separately.
		if !utf8.ValidString(out) || strings.IndexByte(out, 0x9b) >= 0 || strings.IndexByte(out, 0x9d) >= 0 ||
			strings.ContainsAny(out, "\u061c\u2062\ufffa\U000E0041\x07") {
			t.Errorf("sanitize(%q) = %q, still carries a control/invisible", in, out)
		}
	}
	if got := sanitize("plain text"); got != "plain text" {
		t.Errorf("readable text changed: %q", got)
	}
}

// Wire ids reach the terminal and filenames through shortID.
func TestShortIDAndExportNameAreSafe(t *testing.T) {
	if got := shortID("\x1b[2J\x1b[Hsess"); strings.Contains(got, "\x1b") {
		t.Fatalf("shortID kept an escape: %q", got)
	}
	if got := shortID(strings.Repeat("é", 20)); !strings.HasSuffix(got, "…") || strings.ContainsRune(got, '�') {
		t.Fatalf("shortID split a rune: %q", got)
	}
	dir := t.TempDir()
	path, err := writeExport(dir, "/../../../../tmp/x", "md", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("export escaped its directory: %s", path)
	}
}

// A sub-agent envelope's status is wire text like every other card field.
func TestAgentResultStatusSanitized(t *testing.T) {
	r := parseAgentResult(`{"status":"done\u001b]52;c;Zm9v\u0007","summary":"x"}`)
	if r == nil {
		t.Fatal("envelope not parsed")
	}
	if strings.ContainsAny(r.status, "\x1b\x07") {
		t.Fatalf("status carries escapes: %q", r.status)
	}
}

// The panel status line may quote raw server bodies.
func TestPanelMsgRenderedSanitized(t *testing.T) {
	m := newTestModel()
	m.runCommandLine("/queue")
	m.panelMsg = "promote failed: \x1b]52;c;Zm9v\x07"
	if v := m.View(); strings.Contains(v, "]52;") && strings.Contains(v, "\x1b]52") {
		t.Fatal("panel message rendered a raw OSC sequence")
	}
}

// Draining the head under an open /queue panel keeps the highlight on the
// same prompt and disarms a pending delete.
func TestQueueDrainDisarmsDeleteAndKeepsSelection(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m.queue = []string{"A", "B", "C"}
	m.runCommandLine("/queue")
	m.Update(key("down"))
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if m.confirm != confirmQueueDelete {
		t.Fatalf("delete not armed: %v", m.confirm)
	}
	m.busy = false
	_ = m.sendQueued()
	if m.confirm != confirmNone {
		t.Fatal("drain left the queue delete armed")
	}
	if m.queue[m.panelSel] != "B" {
		t.Fatalf("selection moved to %q, want B", m.queue[m.panelSel])
	}
}

// A runs poll that reorders rows must not slide approve/cancel onto
// another run.
func TestRunsPollAnchorsSelectionByID(t *testing.T) {
	m := newTestModel()
	m.panel = panelRuns
	m.runs = []client.Run{{ID: "r1"}, {ID: "r2"}}
	m.panelSel = 1
	m.handleRunsMsg(runsMsg{runs: []client.Run{{ID: "r0"}, {ID: "r1"}, {ID: "r2"}}})
	if r := m.selectedRun(); r == nil || r.ID != "r2" {
		t.Fatalf("selection = %+v, want r2", r)
	}
}

// Reopening the runs tab must not anchor the first poll on the old list.
func TestRunsReopenDropsStaleList(t *testing.T) {
	m := newTestModel()
	m.runs = []client.Run{{ID: "old"}}
	_ = m.openRuns()
	if m.selectedRun() != nil {
		t.Fatal("a stale run is actionable before the first poll lands")
	}
}

// A memory refresh that drops the confirmed fact must disarm the gate.
func TestFactDeleteDisarmsWhenRowVanishes(t *testing.T) {
	m := newTestModel()
	m.panel = panelMemory
	m.memRows = []memRow{{kind: "user", text: "a"}, {kind: "user", text: "b"}}
	m.panelSel = 1
	m.armConfirm(confirmFactDelete, "user fact")
	var mem client.MemoryView
	mem.Facts = map[string][]string{"user": {"a", "c"}}
	m.handleMgmtMsg(mgmtMsg{tab: panelMemory, mem: mem})
	if m.confirm != confirmNone {
		t.Fatal("delete still armed against a neighbouring fact")
	}
}

// Late session rows re-rank the palette; Enter must keep its target.
func TestPaletteSelectionSurvivesLateSessions(t *testing.T) {
	m := newTestModel()
	m.pal = palState{open: true, mode: palModeCommands, query: "ca"}
	m.pal.all = []palEntry{{title: "xc a run", kind: "action"}}
	m.filterPalette()
	m.pal.sel = 0
	late := client.Session{ID: "s1", Task: "ca"}
	a, _ := fuzzyScore("ca", "resume · ca")
	if b, _ := fuzzyScore("ca", "xc a run"); a <= b {
		t.Fatal("fixture must rank the late session above the selected entry")
	}
	m.handlePalSessions(palSessionsMsg{items: []client.Session{late}})
	if got := m.pal.items[m.pal.sel].title; got != "xc a run" {
		t.Fatalf("highlight moved to %q", got)
	}
}

// A late list or search page that re-ranks rows under an armed session
// delete must disarm it — y would delete a session never confirmed.
func TestSessionDeleteDisarmsOnReorderedPage(t *testing.T) {
	m := newTestModel()
	m.panel = panelSessions
	m.sessions = []client.Session{{ID: "x"}, {ID: "y"}}
	m.panelSel = 0
	m.armConfirm(confirmSessionDelete, "session x")
	m.handleSessionsPageMsg(sessionsPageMsg{page: client.SessionsPage{Sessions: []client.Session{{ID: "y"}, {ID: "x"}}}})
	if m.confirm != confirmNone {
		t.Fatal("delete still armed after the list re-ranked")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}); cmd != nil {
		t.Fatal("y fired a command after the target moved")
	}

	m.sessions = []client.Session{{ID: "x"}, {ID: "y"}}
	m.panelSel = 1
	m.armConfirm(confirmSessionDelete, "session y")
	m.handleSessionsMsg(sessionsMsg{items: []client.Session{{ID: "x"}, {ID: "y"}}}) // selection resets to x
	if m.confirm != confirmNone {
		t.Fatal("initial list reply left the delete armed on a moved target")
	}
}

// An unchanged target keeps the gate (and its prompt).
func TestSessionDeleteStaysArmedWhenTargetUnchanged(t *testing.T) {
	m := newTestModel()
	m.panel = panelSessions
	m.sessions = []client.Session{{ID: "x"}}
	m.armConfirm(confirmSessionDelete, "session x")
	m.handleSessionsPageMsg(sessionsPageMsg{page: client.SessionsPage{Sessions: []client.Session{{ID: "x"}}}})
	if m.confirm != confirmSessionDelete || m.panelMsg == "" {
		t.Fatalf("gate dropped for an unchanged target: confirm=%v msg=%q", m.confirm, m.panelMsg)
	}
}

// A delete reply removing the row a second gate was re-armed on must not
// leave that gate pointing at the next session.
func TestSessionDeletedDisarmsGateOnRemovedRow(t *testing.T) {
	m := newTestModel()
	m.panel = panelSessions
	m.sessions = []client.Session{{ID: "a"}, {ID: "b"}}
	m.armConfirm(confirmSessionDelete, "session a")
	m.handleSessionDeleted(sessionDeletedMsg{id: "a"})
	if m.confirm != confirmNone {
		t.Fatal("gate slid onto the neighbouring session")
	}
}
