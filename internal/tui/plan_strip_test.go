package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// Tests for the live plan strip (docs/PLANNING_MODE_UI.md §4B) and the /plan
// slash command. The strip's zero-footprint rule: idle, no-plan, collapsed,
// or degraded engine ⇒ empty label, no layout cost.

func TestPlanStrip_VisibilityMatrix(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{}
	m.sessionID = "s1"

	// Idle ⇒ nothing.
	if got := m.planStripLabel(); got != "" {
		t.Fatalf("idle strip = %q", got)
	}

	m.busy = true
	// Busy but no knowledge yet ⇒ nothing.
	if got := m.planStripLabel(); got != "" {
		t.Fatalf("uninitialized strip = %q", got)
	}

	acceptPlan(m, planFixture())
	got := m.planStripLabel()
	for _, want := range []string{"plan 1/4", "wire flag parsing", "⛔1"} {
		if !strings.Contains(got, want) {
			t.Errorf("strip %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "scaffold") { // only the active step shows
		t.Error("strip leaked a non-active step title")
	}

	// Collapsed all-done ⇒ nothing (the drawer tab owns the record).
	allDone := planFixture()
	allDone.Version = 9
	allDone.Steps = []client.PlanStep{}
	acceptPlan(m, allDone)
	if got := m.planStripLabel(); got != "" {
		t.Fatalf("collapsed strip = %q", got)
	}

	// Degraded endpoint ⇒ nothing.
	acceptPlanErrStrip(m)
	if got := m.planStripLabel(); got != "" {
		t.Fatalf("degraded strip = %q", got)
	}
}

// acceptPlanErrStrip mirrors the tab test helper without importing across
// files beyond the package (single namespace, distinct name).
func acceptPlanErrStrip(m *Model) {
	m.planReqSeq++
	handlePlanErrMsg(m)
}

func handlePlanErrMsg(m *Model) {
	m.handlePlanMsg(planMsg{want: m.sessionID, seq: m.planReqSeq,
		err: errors.New("session plan: status 404 Not Found")})
}

func TestStatusLine_CarriesStripWhenBusy(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{}
	m.sessionID = "s1"
	m.busy = true
	m.status = "running shell"
	m.lastTool = "shell"
	acceptPlan(m, planFixture())

	out := plain(m.statusLine())
	if !strings.Contains(out, "▸ plan 1/4") {
		t.Errorf("status line missing strip:\n%s", out)
	}

	m.busy = false
	if strings.Contains(plain(m.statusLine()), "▸") {
		t.Error("idle status line must not carry the strip")
	}
}

func TestSlashCommand_PlanOpensTab(t *testing.T) {
	found := false
	var open func(m *Model, args string) tea.Cmd
	for _, c := range slashCommands() {
		if c.name == "plan" {
			found = true
			open = c.run
		}
	}
	if !found {
		t.Fatal("/plan missing from the registry")
	}
	m := newTestModel()
	m.cl = &client.Client{}
	m.sessionID = "s1" // fetch requires an attached session
	if cmd := open(m, ""); cmd == nil {
		t.Fatal("/plan must issue the fetch")
	}
	if m.panel != panelPlan {
		t.Fatalf("/plan panel = %d", m.panel)
	}
}
