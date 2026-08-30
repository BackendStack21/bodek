package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestStopAgentGate: ctrl+s arms the stop gate on the first live agent,
// any other key disarms, y fires (self-guarded without a connection).
func TestStopAgentGate(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running", Step: 2})

	m2, _ := m.Update(key("ctrl+s"))
	m = m2.(*Model)
	if m.confirm != confirmStopAgent {
		t.Fatalf("ctrl+s did not arm confirmStopAgent: %v", m.confirm)
	}
	if !strings.Contains(m.panelMsg, "stop") || !strings.Contains(m.panelMsg, "SA1") {
		t.Errorf("gate text missing target: %q", m.panelMsg)
	}

	m2, _ = m.Update(key("x"))
	m = m2.(*Model)
	if m.confirm != confirmNone {
		t.Fatalf("non-confirm key did not disarm: %v", m.confirm)
	}

	m2, _ = m.Update(key("ctrl+s"))
	m = m2.(*Model)
	m2, _ = m.Update(key("y"))
	m = m2.(*Model)
	if m.confirm != confirmNone {
		t.Fatalf("gate still armed after firing: %v", m.confirm)
	}
	if got := strings.Join(m.notices, "\n"); !strings.Contains(got, "nothing to stop") {
		t.Errorf("sessionless fire should self-guard, notices=%q", got)
	}
}

// TestStopAgentIdle: with no live cards ctrl+s is a no-op, not a gate.
func TestStopAgentIdle(t *testing.T) {
	m := stateFixture(t) // delegate step exists but no state frames → no cards
	m2, _ := m.Update(key("ctrl+s"))
	m = m2.(*Model)
	if m.confirm != confirmNone {
		t.Fatalf("ctrl+s without live agents armed a gate: %v", m.confirm)
	}
}

// TestStopCommand: /stop lists live labels bare, resolves <SA#>, and arms
// the same gate; unknown labels are refused.
func TestStopCommand(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running"})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "active", Status: "running"})

	if cmd := m.stopByLabel(""); cmd == nil {
		t.Fatal("bare /stop should return the notice sweep")
	}
	if got := strings.Join(m.notices, "\n"); !strings.Contains(got, "SA1") || !strings.Contains(got, "SA2") {
		t.Errorf("bare /stop should list live labels, notices=%q", got)
	}

	m.confirm = confirmNone
	m.panelMsg = ""
	if cmd := m.stopByLabel("sa2"); cmd != nil {
		t.Fatal("arming should return nil (the gate waits for y)")
	}
	if m.confirm != confirmStopAgent || m.stopTarget != "t2" {
		t.Fatalf("/stop sa2 did not arm the gate for t2: %v %q", m.confirm, m.stopTarget)
	}
	if !strings.Contains(m.panelMsg, "SA2") {
		t.Errorf("gate text missing SA2: %q", m.panelMsg)
	}

	m.confirm = confirmNone
	if cmd := m.stopByLabel("9"); cmd == nil {
		t.Fatal("unknown label should return the notice sweep")
	}
	if got := strings.Join(m.notices, "\n"); !strings.Contains(got, "SA9") {
		t.Errorf("unknown label not surfaced: %q", got)
	}

	// The slash registry exposes /stop.
	names := make([]string, 0, 8)
	for _, c := range slashCommands() {
		names = append(names, c.name)
	}
	if !slices.Contains(names, "stop") {
		t.Errorf("/stop not registered: %v", names)
	}
}

// TestStopAck: accepted:true stays silent; accepted:false (benign race) gets
// an explicit notice. Terminal state always comes from subagent_state.
func TestStopAck(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running"})
	m.handleEvent(client.Event{Type: "subagent_cancelled", TaskID: "t1", Accepted: true})
	if got := strings.Join(m.notices, "\n"); got != "" {
		t.Errorf("accepted ack should stay silent, notices=%q", got)
	}
	m.handleEvent(client.Event{Type: "subagent_cancelled", TaskID: "t9", Accepted: false})
	if got := strings.Join(m.notices, "\n"); !strings.Contains(got, "already finished") {
		t.Errorf("benign-race ack not surfaced: %q", got)
	}
}

// TestStopSentMarker: the card advertises the in-flight stop until the
// terminal frame replaces the line.
func TestStopSentMarker(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running", Step: 4})
	s := stateStep(t, m)
	s.agents[0].stopSent = true
	if line := agentCardLine(s.agents[0]); !strings.Contains(line, "stop sent") {
		t.Errorf("running line missing stop marker: %q", line)
	}
	s.agents[0].phase, s.agents[0].status = "finished", "cancelled"
	if line := agentCardLine(s.agents[0]); strings.Contains(line, "stop sent") {
		t.Errorf("terminal line still shows stop marker: %q", line)
	}
}
