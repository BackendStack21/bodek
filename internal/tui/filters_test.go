package tui

import (
	"strings"
	"testing"
)

// TestEventsSessionFilter verifies f toggles the ring filter: the header
// marks "this session", the fetch carries session_id, and toggling back
// restores the full feed.
func TestEventsSessionFilter(t *testing.T) {
	m := wired(t)
	m.sessionID = "s1"
	m.Update(exec(m.openEvents()))
	if len(m.feed) != 3 {
		t.Fatalf("unfiltered feed = %d events, want 3", len(m.feed))
	}

	_, cmd := m.Update(key("f"))
	m.Update(exec(cmd))
	if !m.evSessionFilter {
		t.Fatal("f did not enable the session filter")
	}
	if !strings.Contains(plain(m.View()), "this session") {
		t.Error("events header missing the filter marker")
	}
	if got := len(m.feed); got != 2 {
		t.Errorf("filtered feed = %d events, want 2 (s1 only)", got)
	}

	_, cmd2 := m.Update(key("f"))
	m.Update(exec(cmd2))
	if m.evSessionFilter || len(m.feed) != 3 {
		t.Errorf("second f did not restore the full feed: filter=%v n=%d", m.evSessionFilter, len(m.feed))
	}
}

// TestMemoryEnvConsolidate verifies E consolidates the env target and the
// footer teaches both targets.
func TestMemoryEnvConsolidate(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openMemory()))
	if !strings.Contains(plain(m.View()), "consolidate user") {
		t.Error("memory footer missing the consolidate hints")
	}
	// E returns a real work cmd (the env consolidation POST).
	if cmd := m.memConsolidate("env"); cmd == nil {
		t.Fatal("env consolidate returned no work")
	}
	m.Update(exec(m.memConsolidate("env")))
}
