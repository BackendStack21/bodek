package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestAgentsTabFlow: the drawer's agents tab opens, decodes the registry
// snapshot, renders rows, and opens the ⏎ detail.
func TestAgentsTabFlow(t *testing.T) {
	m := newTestModel()
	if cmd := m.openAgents(); cmd != nil {
		t.Fatal("openAgents without a connection should skip the fetch")
	}
	if m.panel != panelAgents {
		t.Fatalf("openAgents did not open the tab: %v", m.panel)
	}

	m.handleMgmtMsg(mgmtMsg{tab: panelAgents, sag: []client.SubagentEntry{
		{TaskID: "t1", RunKey: "rk1", Goal: "explore the repo", Phase: "finished", Status: "success", Iterations: 3, TokensUsed: 1500},
		{TaskID: "t2", Phase: "active", Status: "running", Step: 4, LastTool: "read"},
	}})
	if m.panelLen() != 2 {
		t.Fatalf("panelLen = %d, want 2", m.panelLen())
	}
	rows := m.agentRowsRender(120)
	joined := strings.Join(rows, "\n")
	for _, want := range []string{"✓", "SA1", "explore the repo", "1.5k tok", "⟳", "SA2", "read"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rows missing %q: %q", want, joined)
		}
	}

	// ⏎ opens the detail with the full metadata through sanitize().
	m.panelSelect()
	if !m.panelDetail {
		t.Fatal("enter did not open the detail view")
	}
	detail := strings.Join(m.mgmtDetailLines(120), "\n")
	for _, want := range []string{"explore the repo", "task t1", "rk1"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %q: %q", want, detail)
		}
	}

	// Tab cycling reaches the new tab and resets the detail submode.
	m2, _ := m.Update(key("]"))
	m = m2.(*Model)
	if m.panelDetail {
		t.Error("tab switch did not reset the detail submode")
	}
}

// TestAgentsTabCommand: /agents is a registered slash command opening the tab.
func TestAgentsTabCommand(t *testing.T) {
	names := make([]string, 0, 12)
	for _, c := range slashCommands() {
		names = append(names, c.name)
	}
	if !slices.Contains(names, "agents") {
		t.Fatalf("/agents not registered: %v", names)
	}
	m := newTestModel()
	for _, c := range slashCommands() {
		if c.name == "agents" {
			c.run(m, "")
			break
		}
	}
	if m.panel != panelAgents {
		t.Fatalf("/agents did not open the tab: %v", m.panel)
	}
}
