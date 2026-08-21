package tui

import (
	"strings"
	"testing"
)

// TestCockpitLiveFetch verifies the popover's one-shot /api/health +
// /api/usage fetch: the lifetime section renders with the usage aggregates,
// the server card gains the authoritative started-at, and a closed popover
// swallows the (stale) result without refreshing.
func TestCockpitLiveFetch(t *testing.T) {
	m := wired(t)
	cmd := m.openCockpit()
	if !m.popover {
		t.Fatal("openCockpit did not show the popover")
	}
	m.Update(exec(cmd)) // cockpitMsg

	out := plain(m.popoverView(100, 30))
	for _, want := range []string{"lifetime", "4 started · 3 completed", "⇥1.0k ↦200", "unavailable (no prices)", "since"} {
		if !strings.Contains(out, want) {
			t.Errorf("cockpit missing %q:\n%s", want, out)
		}
	}

	// A stale result for a closed popover is a no-op.
	m.popover = false
	m.Update(exec(m.openCockpit()))
	m.popover = false
	m.Update(cockpitMsg{})
	_ = m.View()
}
