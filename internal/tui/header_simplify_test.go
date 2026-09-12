package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── header simplification: plan x/t, odek version, and tok/s leave the top
// bar. Plan progress lives in the /plan tab, tok/s and the context gauge in
// the cockpit; the engine version moves INTO the cockpit so it stays reachable.

func headerModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel()
	m.odekVersion = "v1.40.2"
	m.bodekVersion = "v1.11.10"
	// A plan with progress would previously paint "plan 1/2" in the header.
	m.planInit = true
	m.planAvail = planAvailable
	m.plan.Found = true
	m.plan.Steps = []client.PlanStep{
		{ID: "a", Status: client.PlanDone},
		{ID: "b", Status: client.PlanPending},
	}
	// A live rate would previously paint "↗ x tok/s".
	m.tokPerSec = 42.5
	return m
}

func TestHeaderDropsPlanVersionRate(t *testing.T) {
	m := headerModel(t)
	head := plain(m.header())
	for _, banned := range []string{"plan 1/2", "odek v1.40.2", "tok/s", "↗"} {
		if strings.Contains(head, banned) {
			t.Errorf("header still shows %q:\n%s", banned, head)
		}
	}
	// Kept: bodek's own version rides the logo; instruments strip keeps jobs.
	if !strings.Contains(head, "v1.11.10") {
		t.Errorf("bodek version missing from header:\n%s", head)
	}
}

func TestCockpitShowsEngineVersion(t *testing.T) {
	m := headerModel(t)
	m.popover = true // cockpit popover embeds the stats sheet
	v := plain(m.View())
	if !strings.Contains(v, "v1.40.2") {
		t.Errorf("cockpit missing the engine version row:\n%s", firstLines(v, 22))
	}
}

func firstLines(s string, n int) string {
	parts := strings.Split(s, "\n")
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}
