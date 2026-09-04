package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestSwarmChipsAlwaysOn(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running", Tool: "read main.go"})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "finished", Status: "error"})
	s := stateStep(t, m)
	if s.expanded {
		t.Fatal("precondition: collapsed")
	}
	out, _ := renderStepsForTest(m, m.msgs[0], 0, 0)
	plainOut := plain(out)
	for _, want := range []string{"SA1", "read main.go", "SA2", "SA3"} {
		if !strings.Contains(plainOut, want) {
			t.Errorf("collapsed strip missing %q:\n%s", want, plainOut)
		}
	}
	if strings.Contains(plainOut, "· sub-agent") {
		t.Errorf("chips should replace the · sub-agent tag:\n%s", plainOut)
	}
	if strings.Contains(plainOut, "started explorer") || strings.Contains(plainOut, "pending (not yet reported)") {
		t.Errorf("collapsed strip leaked the log dump:\n%s", plainOut)
	}
}

func TestAgentFocusShowsBeatNotDump(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running", Step: 7, Tool: "read main.go", Iterations: 5})
	m.handleEvent(client.Event{Type: "subagent_log", SubType: "tool_call", Name: "read", Data: "secret-log-line", TaskIdx: 0})
	s := stateStep(t, m)
	s.setAgentFocus(0)
	out, _ := renderStepsForTest(m, m.msgs[0], 0, 0)
	plainOut := plain(out)
	if !strings.Contains(plainOut, "SA1") || !strings.Contains(plainOut, "step 7") {
		t.Errorf("focused card missing identity/beat:\n%s", plainOut)
	}
	if strings.Contains(plainOut, "secret-log-line") {
		t.Errorf("focus without expand should not dump logs:\n%s", plainOut)
	}
	s.expanded = true
	out, _ = renderStepsForTest(m, m.msgs[0], 0, 0)
	if !strings.Contains(plain(out), "secret-log-line") {
		t.Errorf("expand+focus should show scoped logs:\n%s", plain(out))
	}
}

func TestCycleAgentFocusAndEsc(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running"})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t3", TaskIdx: 2, Phase: "queued", Status: "queued"})
	if !m.cycleAgentFocus() {
		t.Fatal("cycleAgentFocus returned false")
	}
	s := stateStep(t, m)
	if s.focusedIdx() != 0 {
		t.Fatalf("first tab = %d, want SA1 (0)", s.focusedIdx())
	}
	m.cycleAgentFocus()
	if s.focusedIdx() != 2 {
		t.Fatalf("second tab = %d, want SA3 (2)", s.focusedIdx())
	}
	m.cycleAgentFocus()
	if s.focusedIdx() != 1 {
		t.Fatalf("third tab = %d, want pending SA2 (1)", s.focusedIdx())
	}
	m.cycleAgentFocus()
	if s.focusedIdx() != -1 {
		t.Fatalf("fourth tab should clear focus, got %d", s.focusedIdx())
	}

	s.setAgentFocus(0)
	s.expanded = true
	m.Update(key("esc"))
	if stateStep(t, m).focusedIdx() != -1 {
		t.Error("esc did not clear focus")
	}
	if !stateStep(t, m).expanded {
		t.Error("esc cleared expand together with focus")
	}
}

func TestSwarmReceiptRailNotInAnswer(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success"})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "finished", Status: "error"})
	m.finalize()
	if strings.Contains(m.msgs[0].content, "sub-agents:") {
		t.Fatalf("receipt stuffed into content: %q", m.msgs[0].content)
	}
	out, _ := m.renderMessage(m.msgs[0], 0, 0)
	if !strings.Contains(plain(out), "sub-agents: 1 ✓ · 1 ✗") {
		t.Errorf("receipt rail missing from finalized turn:\n%s", plain(out))
	}
}

func TestSelectAgentChipToggles(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running"})
	m.selectAgentChip(0, 0, 0)
	if stateStep(t, m).focusedIdx() != 0 {
		t.Fatal("click did not focus")
	}
	m.selectAgentChip(0, 0, 0)
	if stateStep(t, m).focusedIdx() != -1 {
		t.Fatal("second click did not clear focus")
	}
}

func TestPackChipRowsNeverSplitsAChip(t *testing.T) {
	chips := []agentChip{
		{idx: 0, glyph: "⟳", label: "SA1 explore"},
		{idx: 1, glyph: "✓", label: "SA2 a-very-long-goal-label"},
	}
	rows := packChipRows(chips, 20)
	if len(rows) < 2 {
		t.Fatalf("expected wrap, got %d rows", len(rows))
	}
	for _, row := range rows {
		if len(row) == 0 {
			t.Fatal("empty chip row")
		}
	}
}
