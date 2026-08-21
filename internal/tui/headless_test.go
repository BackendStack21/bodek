package tui

import (
	"strings"
	"testing"
)

// TestHeadlessRunStart drives /run and the palette draft entry: the run is
// submitted (fresh session), acknowledged with a fading note, and the runs
// tab opens onto it.
func TestHeadlessRunStart(t *testing.T) {
	m := wired(t)

	// /run submits the headless prompt; the returned closure resolves to a
	// runStartedMsg, whose handler opens the runs tab.
	cmd := m.runCommand("run", "refactor the auth module")
	msg := exec(cmd)
	if msg == nil {
		t.Fatal("/run returned no work")
	}
	m.Update(msg)                // runStartedMsg: posts the note, queues the tab
	m.Update(exec(m.openRuns())) // drive the tab open + fetch
	if m.panel != panelRuns {
		t.Fatalf("/run did not open the runs tab: %d", m.panel)
	}
	if note, exp := lastNoteMatching(m, "headless run started"); note == "" || exp.IsZero() {
		t.Errorf("start note missing or sticky: %q exp=%v", note, exp)
	}

	// The palette entry sends the composer draft headlessly and clears it.
	m.closePanel()
	m.ta.SetValue("long benchmark sweep")
	for _, e := range m.basePaletteEntries() {
		if strings.Contains(e.title, "run headless") {
			cmd = e.run(m)
			break
		}
	}
	if cmd == nil {
		t.Fatal("palette has no run-headless entry")
	}
	if m.ta.Value() != "" {
		t.Errorf("draft not consumed: %q", m.ta.Value())
	}
	m.Update(exec(m.openRuns()))
	if m.panel != panelRuns {
		t.Fatal("palette entry did not open the runs tab")
	}

	// Without a prompt, /run teaches instead of failing silently. The note
	// is posted at call time; its sweep cmd is a timer we deliberately do
	// not execute (exec would block the TTL and expire it).
	m.closePanel()
	m.runCommand("run", "")
	if note, _ := lastNoteMatching(m, "/run needs a prompt"); note == "" {
		t.Errorf("empty /run gave no guidance: %v", m.notices)
	}
}
