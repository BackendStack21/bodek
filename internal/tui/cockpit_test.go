package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestCockpitPopover verifies /server (and a header click) open the cockpit,
// its sections render from live snapshot state, and esc closes it — without
// disturbing a running turn underneath.
func TestCockpitPopover(t *testing.T) {
	m := newTestModel()
	m.odekVersion = "1.24.0"
	m.model = "glm-5.3"
	m.serverStream = true
	m.srvUptime = 90 * time.Second
	m.srvConns = 2
	m.rtt = 34 * time.Millisecond
	m.limits.MaxCostUSD = 0.5
	m.sessCtxTok, m.sessOutTok = 1200, 340
	m.busy = true // the run keeps streaming under the overlay

	m.runCommand("server", "")
	if !m.popover {
		t.Fatal("/server did not open the cockpit")
	}
	out := plain(m.View())
	for _, want := range []string{"cockpit", "server", "1.24.0", "glm-5.3",
		"» live deltas", "1m30s", "2", "34ms", "cost cap", "session"} {
		if !strings.Contains(out, want) {
			t.Errorf("cockpit missing %q:\n%s", want, out)
		}
	}
	if !m.busy {
		t.Error("cockpit disturbed the running turn")
	}

	// Esc closes back to the transcript.
	m.Update(key("esc"))
	if m.popover {
		t.Error("esc did not close the cockpit")
	}

	// A header click toggles it (WebUI health-popover parity).
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, Y: 0})
	if !m.popover {
		t.Error("header click did not open the cockpit")
	}
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, Y: 1})
	if m.popover {
		t.Error("second header click did not close the cockpit")
	}
}

// TestCockpitBudgetEmpty verifies the budget card degrades to "none
// configured" instead of empty rows.
func TestCockpitBudgetEmpty(t *testing.T) {
	m := newTestModel()
	if out := plain(m.cockpitRows("budget", m.cockpitBudgetRows(), 0)); !strings.Contains(out, "none configured") {
		t.Errorf("empty budget card = %q", out)
	}
}

// TestCockpitHugsContent verifies a sparse cockpit renders only the rows it
// has: the card no longer pads to the full transcript height, and the
// session block no longer repeats the sandbox state as a lone row.
func TestCockpitHugsContent(t *testing.T) {
	m := newTestModel()
	m.sandbox = true
	m.model = "deepseek-v4-flash"
	m.odekVersion = "v2.14.0"
	m.popover = true

	const avail = 34
	card := plain(m.popoverView(100, avail))
	lines := strings.Split(card, "\n")
	if len(lines) >= avail {
		t.Errorf("cockpit rendered %d lines, want fewer than the %d available:\n%s", len(lines), avail, card)
	}
	if n := strings.Count(card, "sandboxed"); n != 0 {
		t.Errorf("cockpit repeats the sandbox state %d times, want none:\n%s", n, card)
	}
	if n := strings.Count(card, "v2.14.0"); n != 1 {
		t.Errorf("engine version rendered %d times, want 1:\n%s", n, card)
	}
	// The full view keeps the header's single sandbox badge and nothing more.
	if n := strings.Count(plain(m.View()), "sandboxed"); n > 1 {
		t.Errorf("full view shows sandbox state %d times, want at most the header's:\n%s", n, plain(m.View()))
	}
}

// TestCockpitValuesAlign verifies every label/value row in the cockpit puts
// its value in one shared column, including the session block's engine row.
func TestCockpitValuesAlign(t *testing.T) {
	m := newTestModel()
	m.sandbox = true
	m.model = "deepseek-v4-flash"
	m.odekVersion = "v2.14.0"
	m.popover = true
	m.serverStream = true
	m.limits.MaxCostUSD = 0.5
	m.usageSnap = nil

	col := func(needle string) int {
		t.Helper()
		for _, line := range strings.Split(plain(m.popoverView(100, 34)), "\n") {
			if i := strings.Index(line, needle); i >= 0 {
				return lipgloss.Width(line[:i])
			}
		}
		t.Fatalf("cockpit has no row containing %q", needle)
		return -1
	}
	want := col("deepseek-v4-flash")
	for _, needle := range []string{"» live deltas", "isolated", "v2.14.0"} {
		if got := col(needle); got != want {
			t.Errorf("value %q at column %d, want the shared column %d", needle, got, want)
		}
	}
}

// TestCockpitRefresh verifies r inside the popover re-fires the live
// health+usage fetch — the cockpit is not a one-shot snapshot.
func TestCockpitRefresh(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openCockpit()))
	if m.healthSnap == nil || m.usageSnap == nil {
		t.Fatal("open did not fetch health+usage")
	}
	m.healthSnap = nil // simulate staleness
	_, cmd := m.Update(key("r"))
	m.Update(exec(cmd))
	if m.healthSnap == nil {
		t.Error("r did not re-fetch the cockpit snapshots")
	}
	if out := plain(m.View()); !strings.Contains(out, "r refresh") {
		t.Errorf("refresh hint missing from the card:\n%s", out)
	}
}
