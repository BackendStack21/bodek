package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestLiveTaskRegistryAcrossTurns: stop resolution must not be scoped to the
// current turn — a card left running by turn 1 stays stoppable after turn 2
// opens (the drawer registry stops whatever the user highlights).
func TestLiveTaskRegistryAcrossTurns(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running"})

	// Turn 1 ends, turn 2 opens with its own delegation.
	m.finalize()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 1
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "delegate_tasks", Data: `{"tasks":["c"]}`})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 0, Phase: "active", Status: "running"})

	if m.liveCard("t1") == nil {
		t.Fatal("t1 lost to the current-turn scan — drawer stop would false-negative")
	}
	if m.liveCard("t2") == nil {
		t.Fatal("t2 not tracked")
	}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 0, Phase: "finished", Status: "success"})
	if m.liveCard("t2") != nil {
		t.Fatal("terminal t2 still tracked as live")
	}
}

// TestAgentsTabLivePoll: the agents tab polls every 3s while visible — the
// registry stops being a stale snapshot.
func TestAgentsTabLivePoll(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{} // cmd construction only; the fetch never runs here
	if cmd := m.openAgents(); cmd == nil {
		t.Fatal("openAgents with a client should fetch")
	}
	seq := m.agentsSeq
	if cmd := m.handleAgentsTick(agentsTickMsg{seq: seq}); cmd == nil {
		t.Fatal("fresh tick on the visible tab should refetch")
	}
	m.agentsSeq = seq + 5
	if cmd := m.handleAgentsTick(agentsTickMsg{seq: seq}); cmd != nil {
		t.Fatal("stale tick should be dropped")
	}
	m.panel = panelNone
	if cmd := m.handleAgentsTick(agentsTickMsg{seq: seq + 5}); cmd != nil {
		t.Fatal("tick with the tab closed should be dropped")
	}
}

// TestAgentsTabStop: `c` arms the same two-step stop gate on the highlighted
// registry row; finished rows refuse, and the gate fires through the
// cross-turn live registry.
func TestAgentsTabStop(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running"})
	m.openAgents()
	m.handleMgmtMsg(mgmtMsg{tab: panelAgents, sag: []client.SubagentEntry{
		{TaskID: "t1", Phase: "active", Status: "running", Goal: "audit the auth flow"},
		{TaskID: "t9", Phase: "finished", Status: "success"},
	}})

	m.panelSel = 0
	if cmd := m.stopSelectedAgent(); cmd != nil {
		t.Fatal("arming should return nil (the gate waits for y)")
	}
	if m.confirm != confirmStopAgent || m.stopTarget != "t1" {
		t.Fatalf("gate not armed on the running row: confirm=%v target=%q", m.confirm, m.stopTarget)
	}
	if !strings.Contains(m.panelMsg, "audit the auth flow") {
		t.Errorf("gate text missing the goal label: %q", m.panelMsg)
	}

	m.confirm = confirmNone
	m.panelSel = 1
	if cmd := m.stopSelectedAgent(); cmd == nil {
		t.Fatal("finished row should decline with a note (non-nil sweep)")
	}
	if m.confirm != confirmNone {
		t.Fatalf("finished row armed a gate: %v", m.confirm)
	}
}

// TestAgentsTabJump: `o` closes the drawer onto the transcript step that owns
// the selected task, expanding it; tasks with no card degrade to a note.
func TestAgentsTabJump(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running"})
	m.openAgents()
	m.handleMgmtMsg(mgmtMsg{tab: panelAgents, sag: []client.SubagentEntry{
		{TaskID: "t1", Phase: "active", Status: "running"},
		{TaskID: "t404", Phase: "finished", Status: "success"},
	}})

	m.panelSel = 0
	if cmd := m.jumpToAgentStep(); cmd != nil {
		t.Fatal("jump returns no cmd")
	}
	if m.panel != panelNone {
		t.Fatalf("drawer still open: %v", m.panel)
	}
	if s := stateStep(t, m); !s.expanded {
		t.Fatal("target step not expanded")
	}

	m.openAgents()
	m.panelSel = 1
	if cmd := m.jumpToAgentStep(); cmd == nil {
		t.Fatal("cardless task should degrade to a note (non-nil sweep)")
	}
	if m.panel != panelAgents {
		t.Fatalf("failed jump closed the drawer: %v", m.panel)
	}
}

// TestStickyFailureAndVerdict: terminal errors/timeout stick until the turn
// finalizes (user cancels stay transient), and finalize appends the swarm
// verdict marker to the turn.
func TestStickyFailureAndVerdict(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "error"})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "finished", Status: "cancelled"})

	sticky := 0
	for i, exp := range m.noticeExp {
		if exp.IsZero() && strings.Contains(m.notices[i], "SA1") {
			sticky++
		}
	}
	if sticky != 1 {
		t.Fatalf("sticky error notes = %d, want 1: %q", sticky, m.notices)
	}
	for i, n := range m.notices {
		if strings.Contains(n, "SA2") && m.noticeExp[i].IsZero() {
			t.Errorf("cancelled surfaced sticky, want transient: %q (exp %v)", n, m.noticeExp[i])
		}
	}

	m.finalize()
	msg := m.msgs[0]
	if !strings.Contains(msg.content, "**sub-agents: 1 ✗ · 1 ⊘ — #1 error, #2 cancelled**") {
		t.Errorf("swarm verdict missing: %q", msg.content)
	}
	for i, exp := range m.noticeExp {
		if exp.IsZero() {
			t.Errorf("sticky note survived finalize: %q", m.notices[i])
		}
	}
}

// TestSwarmVerdictSkipsPlainTurns: turns without sub-agent cards get no
// marker — finalize output is unchanged.
func TestSwarmVerdictSkipsPlainTurns(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true, content: "done"})
	m.curIdx = 0
	m.finalize()
	if strings.Contains(m.msgs[0].content, "swarm") {
		t.Errorf("plain turn got a swarm marker: %q", m.msgs[0].content)
	}
}
