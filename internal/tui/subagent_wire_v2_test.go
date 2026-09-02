package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// v2Fixture: a delegate step whose manifest declares one identity, followed
// by a started frame carrying the full wire-v2 block — wire truth must win.
func v2Fixture(t *testing.T) *Model {
	t.Helper()
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "delegate_tasks",
		Data: `{"tasks":[{"goal":"manifest goal","profile":"fast","max_risk":"system_write"}]}`})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "started", Status: "running",
		Goal: "wire goal", Profile: "default", MaxRisk: "local_write",
		BudgetSeconds: 1800, BudgetIterations: 15})
	return m
}

// TestWireIdentityBeatsManifest: started-frame identity (effective values)
// overrides the delegate arg's requested values; budgets ride along.
func TestWireIdentityBeatsManifest(t *testing.T) {
	m := v2Fixture(t)
	s := stateStep(t, m)
	card := s.agents[0]
	if card.goal != "wire goal" {
		t.Errorf("goal = %q, want wire truth", card.goal)
	}
	if card.profile != "default" || card.maxRisk != "local_write" {
		t.Errorf("identity = %q/%q, want default/local_write", card.profile, card.maxRisk)
	}
	if card.budgetS != 1800 || card.budgetIt != 15 {
		t.Errorf("budgets = %d/%d, want 1800/15", card.budgetS, card.budgetIt)
	}
	if badge := cardTrustBadge(card); badge != "profile=default · risk=local_write" {
		t.Errorf("badge = %q", badge)
	}
	if line := agentCardLine(card); !strings.Contains(line, "wire goal") {
		t.Errorf("card line missing wire goal: %q", line)
	}
}

// TestQueuedPhase: queued frames create ◌ cards that count separately in the
// rollup, suppress elapsed, satisfy pending slots, and upgrade on started.
func TestQueuedPhase(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "delegate_tasks",
		Data: `{"tasks":["a","b","c"]}`})
	for idx := 0; idx < 3; idx++ {
		m.handleEvent(client.Event{Type: "subagent_state", TaskID: string(rune('a' + idx)), TaskIdx: idx, Phase: "queued", Status: "queued"})
	}
	s := stateStep(t, m)
	if len(s.agents) != 3 || s.agents[0].glyph() != "◌" {
		t.Fatalf("queued cards wrong: %s %s %s", s.agents[0].glyph(), s.agents[1].glyph(), s.agents[2].glyph())
	}
	if line := agentCardLine(s.agents[0]); !strings.Contains(line, "queued") || strings.Contains(line, "· step") {
		t.Errorf("queued line wrong: %q", line)
	}
	if r := agentRollup(s); r != "0/3 agents · 3 queued" {
		t.Errorf("rollup = %q", r)
	}
	if p := s.pendingAgentLines(); len(p) != 0 {
		t.Errorf("queued cards should satisfy pending slots: %q", p)
	}

	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "a", TaskIdx: 0, Phase: "started", Status: "running", Step: 1})
	s = stateStep(t, m)
	if s.agents[0].glyph() != "⟳" {
		t.Errorf("started card still queued glyph: %q", s.agents[0].glyph())
	}
	if r := agentRollup(s); r != "0/3 agents · 2 queued" {
		t.Errorf("rollup after start = %q", r)
	}
}

// TestBudgetAndCostRender: budgets turn "9 it" into "it 9/15" and pair the
// elapsed with its cap; cost renders as an estimate, capped form included.
func TestBudgetAndCostRender(t *testing.T) {
	m := v2Fixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running",
		Step: 9, Iterations: 9, CostUSD: 0.0421, BudgetCostUSD: 0.5})
	s := stateStep(t, m)
	line := agentCardLine(s.agents[0])
	for _, want := range []string{"it 9/15", "/30m", "~$0.0421/$0.5"} {
		if !strings.Contains(line, want) {
			t.Errorf("line missing %q: %q", want, line)
		}
	}

	// Queued cards have no elapsed to pair.
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "queued", Status: "queued"})
	s = stateStep(t, m)
	if line := agentCardLine(s.agents[1]); strings.Contains(line, "/30m") {
		t.Errorf("queued line shows elapsed: %q", line)
	}
}

// TestResultEnvelopeV2: the framed result's artifacts, denials, and final
// cost parse and render; wire text is sanitized before display.
func TestResultEnvelopeV2(t *testing.T) {
	data := `{"status":"partial","summary":"did <script>things</script>","files_changed":["a.go"],
		"iterations":4,"tokens_used":900,"cost_usd":0.0125,
		"artifacts":[{"schema":"odek.artifact-ref/v1","id":"a1","uri":"file:///tmp/a1.md","media_type":"text/markdown","size_bytes":2048}],
		"denials":[{"tool":"shell","class":"system_write","reason":"protected path"}],"denials_total":2}`
	r := parseAgentResult(data)
	if r == nil {
		t.Fatal("envelope not parsed")
	}
	if r.costUSD != 0.0125 || r.denialsTotal != 2 || len(r.artifacts) != 1 || len(r.denials) != 1 {
		t.Fatalf("parsed envelope wrong: cost=%v denials=%d arts=%v denls=%v",
			r.costUSD, r.denialsTotal, r.artifacts, r.denials)
	}
	if r.artifacts[0].URI != "file:///tmp/a1.md" || r.artifacts[0].SizeBytes == nil || *r.artifacts[0].SizeBytes != 2048 {
		t.Fatalf("artifact ref wrong: %+v", r.artifacts[0])
	}

	m := newTestModel()
	lines := agentResultLines(m, r, 200)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"~$0.0125", "⊘ 2 denied", "⎘ a1", "file:///tmp/a1.md", "(2k)", "shell (system_write)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("result card missing %q:\n%s", want, joined)
		}
	}
	if lines := strings.Split(joined, "\n"); len(lines) > 0 && strings.Contains(lines[0], "\n") {
		t.Error("summary not collapsed to one line")
	}
}

// TestBudgetExhaustedStatus: the new terminal status renders as a partial —
// glyph ◐, counted in the swarm verdict's ◐ bucket.
func TestBudgetExhaustedStatus(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "budget_exhausted"})
	s := stateStep(t, m)
	if s.agents[0].glyph() != "◐" {
		t.Errorf("glyph = %q, want ◐", s.agents[0].glyph())
	}
	m.finalize()
	if !strings.Contains(m.msgs[0].content, "sub-agents: 1 ◐") {
		t.Errorf("verdict missing budget_exhausted: %q", m.msgs[0].content)
	}
}

// TestDenialsNote: a framed result carrying denials surfaces a transient
// note — the boundary hit must be visible even with the step collapsed.
func TestDenialsNote(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "tool_result", Name: "delegate_tasks", Data: `{"status":"partial","summary":"partly done","denials":[{"tool":"shell","class":"system_write","reason":"protected path"}],"denials_total":2}`})
	got := strings.Join(m.notices, "\n")
	if !strings.Contains(got, "2 denied") {
		t.Errorf("denials note missing: %q", got)
	}
}
