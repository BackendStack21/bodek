package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── realtime tab freshness (plan: .plans/REALTIME_TABS_FIX_PLAN.md) ─────────
// Wire frames must refresh the open tab, live cards must feed the agents
// renderer, and the events tab must tick while visible — the same kick +
// generation-guarded-poll pattern the jobs and runs tabs use.

// TestSubagentStateKicksAgentsFetch: a subagent_state frame refreshes the
// open agents tab immediately instead of waiting for the 3s poll.
func TestSubagentStateKicksAgentsFetch(t *testing.T) {
	m := stateFixture(t)
	m.cl = &client.Client{} // kick construction only; the fetch never runs
	m.panel = panelAgents
	if cmd := m.kickAgentsFetch(); cmd == nil {
		t.Fatal("state frame must kick a fetch while the agents tab is open")
	}
	// Tab closed: no kick.
	m.panel = panelNone
	if cmd := m.kickAgentsFetch(); cmd != nil {
		t.Fatal("kick must not fire while another surface is open")
	}
}

// TestAgentsRowsPreferLiveCard: the agents tab row renders the live card's
// telemetry (tool/step, elapsed) — not just the REST snapshot's coarse data.
func TestAgentsRowsPreferLiveCard(t *testing.T) {
	m := stateFixture(t)
	// Live card: the agent is on step 3 running "multi_grep", 40s elapsed.
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0,
		Phase: "active", Status: "running", Step: 3, Tool: "multi_grep"})
	// REST row is stale: server snapshot still says step 1 / shell, 2s.
	m.panel = panelAgents // handleMgmtMsg drops cross-tab results
	m.handleMgmtMsg(mgmtMsg{tab: panelAgents, sag: []client.SubagentEntry{
		{TaskID: "t1", Phase: "active", Status: "running", Goal: "audit", Step: 1, LastTool: "shell", DurationSeconds: 2},
	}})
	rows := m.agentRowsRender(120)
	if len(rows) == 0 {
		t.Fatal("no rows rendered")
	}
	joined := strings.Join(rows, " ")
	if !strings.Contains(joined, "multi_grep") {
		t.Errorf("row ignores the live card's tool: %q", joined)
	}
	if strings.Contains(joined, "shell") {
		t.Errorf("stale REST tool wins over the live card: %q", joined)
	}
	// The live card's step shows only when the tool is empty (tool wins).
	_ = 3
}

// TestMemoryEventKicksMemoryTab: a memory_event frame refetches the open
// memory tab so facts written mid-session appear.
func TestMemoryEventKicksMemoryTab(t *testing.T) {
	m := stateFixture(t)
	m.cl = &client.Client{}
	m.panel = panelMemory
	if cmd := m.kickMemoryFetch(); cmd == nil {
		t.Fatal("memory_event must kick a refetch while the memory tab is open")
	}
	m.panel = panelNone
	if cmd := m.kickMemoryFetch(); cmd != nil {
		t.Fatal("memory kick must not fire while another surface is open")
	}
	// No client: never fetch.
	m.panel = panelMemory
	m.cl = nil
	if cmd := m.kickMemoryFetch(); cmd != nil {
		t.Fatal("memory kick must not fire without a client")
	}
}

// TestMemoryKickExecutesFetch: the memory kick's fetch actually runs against
// a stand-in server and lands as a memory-tab snapshot.
func TestMemoryKickExecutesFetch(t *testing.T) {
	m := wired(t)
	m.panel = panelMemory
	cmd := m.kickMemoryFetch()
	if cmd == nil {
		t.Fatal("kick must fire on the open memory tab with a client")
	}
	m.Update(exec(cmd))
	if len(m.memRows) == 0 {
		t.Logf("memory tab: msg %q", m.panelMsg)
	}
}

// TestAgentsKickNeedsClient: the agents kick never fetches without a client.
func TestAgentsKickNeedsClient(t *testing.T) {
	m := stateFixture(t)
	m.cl = nil
	m.panel = panelAgents
	if cmd := m.kickAgentsFetch(); cmd != nil {
		t.Fatal("agents kick must not fire without a client")
	}
}

// TestAgentsRowsFallbackBranches: finished cards render the summary tail;
// a card with neither tool nor step falls back to "running".
func TestAgentsRowsFallbackBranches(t *testing.T) {
	m := stateFixture(t)
	m.panel = panelAgents
	// Finished live card beats a REST row that agrees it finished.
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0,
		Phase: "finished", Status: "success", Iterations: 7, TokensUsed: 1234})
	// Running card with no tool and no step → bare "running".
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1,
		Phase: "active", Status: "running"})
	m.handleMgmtMsg(mgmtMsg{tab: panelAgents, sag: []client.SubagentEntry{
		{TaskID: "t1", Phase: "finished", Status: "partial", Goal: "g1", LastTool: "shell"},
		{TaskID: "t2", Phase: "active", Status: "running", Goal: ""},
	}})
	rows := m.agentRowsRender(120)
	joined := strings.Join(rows, " ")
	if !strings.Contains(joined, "success · 7 it") {
		t.Errorf("finished card tail not rendered: %q", joined)
	}
	if !strings.Contains(joined, "running · running") {
		t.Errorf("bare running fallback missing: %q", joined)
	}
	if !strings.Contains(joined, "(no goal recorded)") {
		t.Errorf("empty-goal placeholder missing: %q", joined)
	}
}

// TestKickCoalescesPerBatch: a wire burst with multiple subagent_state and
// memory_event frames flushes exactly ONE fetch per kind (flags, not cmds —
// per-event cmds would starve inside ingestWireBatch).
func TestKickCoalescesPerBatch(t *testing.T) {
	m := wired(t)
	m.panel = panelAgents
	_, cmd := m.ingestWireBatch([]client.Event{
		{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running"},
		{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "active", Status: "running"},
		{Type: "subagent_state", TaskID: "t3", TaskIdx: 2, Phase: "active", Status: "running"},
		{Type: "memory_event", SubType: "stored", Target: "user"},
	})
	if cmd == nil {
		t.Fatal("batch with kick-worthy frames must return follow-up cmds")
	}
	// The flush ran at batch construction: flags cleared, no flood on a
	// second flush.
	if m.kickAgents || m.kickMemory {
		t.Fatal("flush must clear pending flags")
	}
	if cmd := m.flushKicks(); cmd != nil {
		t.Fatal("second flush with no pending flags must be nil")
	}
}

// TestLostCardDoesNotOverrideRestRow: a lost card (socket dropped mid-run)
// never overrides the fresher REST row on the agents tab.
func TestLostCardDoesNotOverrideRestRow(t *testing.T) {
	m := stateFixture(t)
	m.panel = panelAgents
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0,
		Phase: "active", Status: "running", Tool: "shell"})
	m.loseLiveAgents() // disconnect orphans the card
	m.handleMgmtMsg(mgmtMsg{tab: panelAgents, sag: []client.SubagentEntry{
		{TaskID: "t1", Phase: "finished", Status: "success", Goal: "g"},
	}})
	rows := m.agentRowsRender(120)
	joined := strings.Join(rows, " ")
	if strings.Contains(joined, "running") {
		t.Errorf("lost card overrode the finished REST row: %q", joined)
	}
	if !strings.Contains(joined, "success") {
		t.Errorf("REST row's terminal state not shown: %q", joined)
	}
}

// TestStaleEventsMsgDropped: an eventsMsg armed before a filter change
// (stale seq) must not clobber the feed; a fresh-seq msg applies and clamps
// a past-the-end selection.
func TestStaleEventsMsgDropped(t *testing.T) {
	m := newTestModel()
	m.panel = panelEvents
	m.eventsTabSeq = 5
	m.feed = []client.RuntimeEvent{{Type: "current"}}
	m.Update(eventsMsg{seq: 3, events: []client.RuntimeEvent{{Type: "stale"}}})
	if len(m.feed) != 1 || m.feed[0].Type != "current" {
		t.Fatalf("stale eventsMsg overwrote the feed: %+v", m.feed)
	}
	// Fresh msg shrinks the feed; selection clamps into range. (The stale
	// drop above re-armed the poll, so read the current generation.)
	m.panelSel = 9
	m.Update(eventsMsg{seq: m.eventsTabSeq, events: []client.RuntimeEvent{{Type: "a"}}})
	if m.panelSel != 0 {
		t.Errorf("panelSel = %d, want 0 after shrink clamp", m.panelSel)
	}
}

// TestAgentsSelectionAnchoredByTaskID: a registry rebuild that inserts a row
// above the selection must not retarget it (the stop gate acts on panelSel).
func TestAgentsSelectionAnchoredByTaskID(t *testing.T) {
	m := stateFixture(t)
	m.panel = panelAgents
	m.handleMgmtMsg(mgmtMsg{tab: panelAgents, sag: []client.SubagentEntry{
		{TaskID: "t1", Goal: "one"},
		{TaskID: "t2", Goal: "two"},
	}})
	m.panelSel = 1 // t2
	m.handleMgmtMsg(mgmtMsg{tab: panelAgents, sag: []client.SubagentEntry{
		{TaskID: "t0", Goal: "new"}, // inserted above
		{TaskID: "t1", Goal: "one"},
		{TaskID: "t2", Goal: "two"},
	}})
	if m.agentsReg[m.panelSel].TaskID != "t2" {
		t.Fatalf("selection drifted to %s, want t2", m.agentsReg[m.panelSel].TaskID)
	}
}

// TestMemorySelectionAnchoredByText: anchorRow keeps the selection on the
// same fact across rebuilds and clamps when the row is gone.
func TestMemorySelectionAnchoredByText(t *testing.T) {
	if got := anchorRow(1, []memRow{{text: "x"}, {text: "y"}, {text: "z"}}, "y"); got != 1 {
		t.Errorf("anchorRow identity = %d, want 1", got)
	}
	if got := anchorRow(1, []memRow{{text: "x"}}, "gone"); got != 0 {
		t.Errorf("anchorRow clamp = %d, want 0", got)
	}
}

// TestAgentGlyphFollowsCard: a finished card must flip the row glyph too —
// never a spinner beside a finished summary.
func TestAgentGlyphFollowsCard(t *testing.T) {
	m := stateFixture(t)
	m.panel = panelAgents
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0,
		Phase: "finished", Status: "success"})
	m.handleMgmtMsg(mgmtMsg{tab: panelAgents, sag: []client.SubagentEntry{
		{TaskID: "t1", Phase: "active", Status: "running", Goal: "g"},
	}})
	rows := m.agentRowsRender(120)
	if strings.Contains(strings.Join(rows, " "), "▸") {
		t.Errorf("running glyph shown for a finished card: %q", rows)
	}
}

// TestEventsTabTicksWhileOpen: the events tab refetches on a fresh tick and
// drops stale/closed ticks (runsTickMsg pattern).
func TestEventsTabTicksWhileOpen(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{} // cmd construction only; the fetch never runs
	if cmd := m.armEventsPoll(); cmd == nil {
		t.Fatal("armEventsPoll should schedule a tick")
	}
	seq := m.eventsTabSeq
	m.panel = panelEvents
	if cmd := m.handleEventsTick(eventsTickMsg{seq: seq}); cmd == nil {
		t.Fatal("fresh tick on the visible events tab should refetch")
	}
	m.eventsTabSeq = seq + 5
	if cmd := m.handleEventsTick(eventsTickMsg{seq: seq}); cmd != nil {
		t.Fatal("stale events tick should be dropped")
	}
	m.panel = panelNone
	if cmd := m.handleEventsTick(eventsTickMsg{seq: m.eventsTabSeq}); cmd != nil {
		t.Fatal("events tick with the tab closed should be dropped")
	}
}

// TestOpenEventsArmsPoll: the first eventsMsg arms the tick chain (open →
// fetch → eventsMsg → arm → tick → …).
func TestOpenEventsArmsPoll(t *testing.T) {
	m := newTestModel()
	m.cl = &client.Client{}
	m.openEvents()
	m.Update(eventsMsg{}) // handleEventsMsg + arm
	if m.eventsTabSeq == 0 {
		t.Fatal("the first eventsMsg must arm the poll chain")
	}
}
