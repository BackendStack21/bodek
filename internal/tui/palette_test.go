package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestFuzzyScore(t *testing.T) {
	cases := []struct {
		query, s string
		wantOK   bool
	}{
		{"", "anything", true},
		{"ses", "sessions", true},
		{"ssns", "sessions", true},    // subsequence
		{"srv", "server stats", true}, // subsequence across words
		{"ses", "cancel the running turn", false},
		{"SES", "sessions", true}, // case-insensitive
		{"xyz", "sessions", false},
	}
	for _, tc := range cases {
		if _, ok := fuzzyScore(tc.query, tc.s); ok != tc.wantOK {
			t.Errorf("fuzzyScore(%q, %q) ok = %v, want %v", tc.query, tc.s, ok, tc.wantOK)
		}
	}
	// Consecutive + word-start bonuses rank tighter matches higher.
	tight, _ := fuzzyScore("ses", "sessions")
	loose, _ := fuzzyScore("ses", "server stats")
	if tight <= loose {
		t.Errorf("expected tight match to outrank loose: %d vs %d", tight, loose)
	}
}

// TestPaletteOpenFilterRun drives the full palette interaction: ^K opens,
// typing filters, enter runs the highlighted entry, and the chord hint shows.
func TestPaletteOpenFilterRun(t *testing.T) {
	m := wired(t)
	_, cmd := m.Update(key("ctrl+k"))
	if !m.pal.open {
		t.Fatal("^K did not open the palette")
	}
	m.Update(exec(cmd)) // sessions fetch lands

	if len(m.pal.items) == 0 {
		t.Fatal("palette has no entries")
	}
	// The default page leads with the navigation spine and teaches chords;
	// slash commands sit behind it (still present in the full list).
	out := plain(m.palPopup())
	for _, want := range []string{"everything", "sessions", "^R", "runs"} {
		if !strings.Contains(out, want) {
			t.Errorf("palette missing %q:\n%s", want, out)
		}
	}
	hasHelp := false
	for _, e := range m.pal.all {
		if e.title == "/help" {
			hasHelp = true
		}
	}
	if !hasHelp {
		t.Error("slash commands missing from the palette source")
	}

	// Typing filters to the session resume entry; its hint teaches ^R.
	for _, r := range "resume" {
		m.Update(key(string(r)))
	}
	if len(m.pal.items) == 0 || !strings.HasPrefix(m.pal.items[0].title, "resume") {
		t.Fatalf("filter result = %+v", m.pal.items)
	}

	// Enter runs it: the session loads directly (no panel round-trip).
	_, rcmd := m.Update(key("enter"))
	m.Update(exec(rcmd)) // sessionDetailMsg
	if m.sessionID != "s1" {
		t.Fatalf("palette resume did not load the session: %q", m.sessionID)
	}

	// ^K toggles closed; a stale palSessionsMsg is ignored while closed.
	m.Update(key("ctrl+k"))
	m.Update(key("ctrl+k"))
	if m.pal.open {
		t.Error("^K did not close the palette")
	}
}

// TestPaletteSelectionTracksScroll is a regression test for the ^K popup:
// the highlight must follow sel even after the list scrolls past the
// visible window (the highlight used to compare a window-relative index
// against the absolute sel, so it vanished or landed on the wrong row).
func TestPaletteSelectionTracksScroll(t *testing.T) {
	m := wired(t)
	_, cmd := m.Update(key("ctrl+k"))
	m.Update(exec(cmd)) // sessions fetch

	if len(m.pal.items) <= maxPalRows {
		t.Fatalf("fixture too small to scroll: %d entries", len(m.pal.items))
	}

	highlighted := func(m *Model) string {
		var row string
		n := 0
		for _, ln := range strings.Split(plain(m.palPopup()), "\n") {
			if strings.Contains(ln, "›") {
				row, n = ln, n+1
			}
		}
		if n != 1 {
			t.Fatalf("expected exactly one highlighted row, got %d", n)
		}
		return row
	}

	// Walk the selection past the visible window; the highlight must stay
	// on the row the selection actually points at.
	for i := 0; i < maxPalRows+2; i++ {
		m.Update(key("down"))
	}
	sel := m.pal.sel
	if sel != maxPalRows+2 {
		t.Fatalf("down did not advance sel: got %d", sel)
	}
	if row := highlighted(m); !strings.Contains(row, m.pal.items[sel].title) {
		t.Fatalf("scrolled highlight is on the wrong row:\n%q\nwant title %q", row, m.pal.items[sel].title)
	}

	// Walk back to the top; the highlight must still track.
	for i := 0; i < sel; i++ {
		m.Update(key("up"))
	}
	if m.pal.sel != 0 {
		t.Fatalf("up did not return to top: got %d", m.pal.sel)
	}
	if row := highlighted(m); !strings.Contains(row, m.pal.items[0].title) {
		t.Fatalf("top highlight is on the wrong row:\n%q\nwant title %q", row, m.pal.items[0].title)
	}
}

// TestPaletteFromApproval verifies the palette works from the approval rung.
func TestPaletteFromApproval(t *testing.T) {
	m := wired(t)
	m.busy = true
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Command: "x"})
	m.Update(key("ctrl+k"))
	if !m.pal.open {
		t.Fatal("^K did not open over the approval panel")
	}
	// Esc closes the palette, not the approval.
	m.Update(key("esc"))
	if m.pal.open {
		t.Error("esc did not close the palette")
	}
	if m.curApproval() == nil {
		t.Error("esc closed the palette straight through the approval")
	}
}
