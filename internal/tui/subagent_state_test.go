package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// stateFixture spins a model with an in-flight delegate_tasks step — the
// anchor every subagent_state frame attaches to.
func stateFixture(t *testing.T) *Model {
	t.Helper()
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "delegate_tasks", Data: `{"tasks":["a","b"]}`})
	return m
}

// stateStep returns the message's sub-agent step.
func stateStep(t *testing.T, m *Model) *step {
	t.Helper()
	msg := &m.msgs[0]
	for j := len(msg.steps) - 1; j >= 0; j-- {
		if msg.steps[j].subagent {
			return &msg.steps[j]
		}
	}
	t.Fatal("no sub-agent step")
	return nil
}

// TestSubagentStateLifecycle drives started → active → finished and checks
// the upserted card, the terminal line, and the collapsed rollup.
func TestSubagentStateLifecycle(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "started", Status: "running"})
	s := stateStep(t, m)
	if len(s.agents) != 1 || s.agents[0].phase != "started" {
		t.Fatalf("card not created: %#v", s.agents)
	}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running", Step: 7, Tool: "read main.go", Iterations: 5, TokensUsed: 3200})
	s = stateStep(t, m)
	if len(s.agents) != 1 {
		t.Fatalf("active frame duplicated the card: %#v", s.agents)
	}
	if s.agents[0].step != 7 || s.agents[0].tool != "read main.go" || s.agents[0].tokens != 3200 {
		t.Fatalf("telemetry not updated: %#v", s.agents[0])
	}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success", Iterations: 6, TokensUsed: 3200, DurationSeconds: 4.2})
	s = stateStep(t, m)
	card := s.agents[0]
	if !card.finished() || card.status != "success" {
		t.Fatalf("card not terminal: %#v", card)
	}
	line := agentCardLine(card)
	for _, want := range []string{"✓ SA1", "6 it", "3.2k tok", "4.2s"} {
		if !strings.Contains(line, want) {
			t.Errorf("terminal line missing %q: %q", want, line)
		}
	}
	if strings.Contains(line, "read main.go") {
		t.Errorf("terminal line still shows live tool: %q", line)
	}
	if r := agentRollup(s); r != "1/1 agents · 3.2k tok" {
		t.Errorf("rollup = %q", r)
	}
}

// TestSubagentStateGlyphs pins the glyph per terminal status and the error
// styling of failed states.
func TestSubagentStateGlyphs(t *testing.T) {
	cases := []struct {
		status string
		glyph  string
		failed bool
	}{
		{"success", "✓", false},
		{"partial", "◐", false},
		{"error", "✗", true},
		{"cancelled", "⊘", true},
		{"timeout", "⏱", true},
	}
	for _, tc := range cases {
		a := &agentCard{taskID: "t", phase: "finished", status: tc.status}
		if got := a.glyph(); got != tc.glyph {
			t.Errorf("status %q glyph = %q, want %q", tc.status, got, tc.glyph)
		}
		if a.failed() != tc.failed {
			t.Errorf("status %q failed = %v", tc.status, a.failed())
		}
	}
	if got := (&agentCard{phase: "active", status: "running"}).glyph(); got != "⟳" {
		t.Errorf("live glyph = %q", got)
	}
}

// TestSubagentStateStray: with no in-flight sub-agent step the frame falls
// back to a notice instead of vanishing — including a frame that arrives
// after the delegate step already closed.
func TestSubagentStateStray(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t9", TaskIdx: 2, Phase: "started", Status: "running"})
	got := strings.Join(m.notices, "\n")
	if !strings.Contains(got, "state SA3") || !strings.Contains(got, "started") {
		t.Errorf("stray state frame not noticed: %q", got)
	}

	m = stateFixture(t)
	m.handleEvent(client.Event{Type: "tool_result", Name: "delegate_tasks", Data: `{"status":"success"}`})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success"})
	got = strings.Join(m.notices, "\n")
	if !strings.Contains(got, "state SA1") || !strings.Contains(got, "finished") {
		t.Errorf("late frame not noticed: %q", got)
	}
}

// TestSubagentStateRollup: the collapsed head aggregates done count and
// tokens; the expanded view renders one live card line per task.
func TestSubagentStateRollup(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success", TokensUsed: 3200})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "active", Status: "running", Step: 3, Tool: "grep x", TokensUsed: 3100})
	s := stateStep(t, m)
	if r := agentRollup(s); r != "1/2 agents · 6.3k tok" {
		t.Fatalf("rollup = %q", r)
	}
	s.expanded = true
	out, _ := renderStepsForTest(m, m.msgs[0], 0, 0)
	if !strings.Contains(out, "1/2 agents") || !strings.Contains(out, "⟳ SA2") {
		t.Errorf("render missing rollup or live card: %q", out)
	}
}

// TestSubagentStateSanitize: wire-derived tool text is sanitized and
// single-lined before it reaches a card.
func TestSubagentStateSanitize(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running", Tool: "read \x1b[31mmain.go\nsecond line", Step: 2})
	line := agentCardLine(stateStep(t, m).agents[0])
	if strings.ContainsAny(line, "\x1b\n") {
		t.Errorf("card line carries control bytes: %q", line)
	}
	if !strings.Contains(line, "main.go second line") {
		t.Errorf("tool text mangled: %q", line)
	}
}
