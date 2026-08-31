package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// manifestFixture builds a model whose in-flight turn carries a delegate_tasks
// call with object-form tasks — the shape the LLM actually sends.
func manifestFixture(t *testing.T) *Model {
	t.Helper()
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "delegate_tasks", Data: `{"tasks":[
		{"goal":"audit the auth flow end to end and report the gaps","profile":"default"},
		{"goal":"write failing tests first, then the fix"},
		{"goal":"ship it and sync the docs"}]}`})
	return m
}

// TestDelegateManifestParsed: the delegate arg becomes a per-step manifest
// with excerpted goals; string-form task arrays and junk args degrade safely.
func TestDelegateManifestParsed(t *testing.T) {
	m := manifestFixture(t)
	s := stateStep(t, m)
	if len(s.manifest) != 3 {
		t.Fatalf("manifest = %d slots, want 3", len(s.manifest))
	}
	g := s.manifest[0].goal
	if !strings.HasPrefix(g, "audit the auth flow") || len([]rune(g)) > 32 {
		t.Errorf("slot 0 goal not excerpted to ≤32 runes: %q (%d runes)", g, len([]rune(g)))
	}
	if s.manifest[0].profile != "default" {
		t.Errorf("slot 0 profile = %q", s.manifest[0].profile)
	}
	if s.manifest[2].goal != "ship it and sync the docs" {
		t.Errorf("slot 2 goal = %q", s.manifest[2].goal)
	}

	// String-form arrays become goal-only slots (the stateFixture shape).
	m2 := stateFixture(t)
	if s2 := stateStep(t, m2); len(s2.manifest) != 2 || s2.manifest[0].goal != "a" {
		t.Errorf("string-form manifest = %#v", s2.manifest)
	}

	// Junk args leave no manifest — the step still renders exactly as before.
	m3 := newTestModel()
	m3.msgs = append(m3.msgs, message{role: roleAsst, streaming: true})
	m3.curIdx = 0
	m3.busy = true
	m3.handleEvent(client.Event{Type: "tool_call", Name: "delegate_tasks", Data: `not json at all`})
	if s3 := stateStep(t, m3); s3.manifest != nil {
		t.Errorf("junk arg produced a manifest: %#v", s3.manifest)
	}
}

// TestCardCarriesGoal: state frames inherit the slot's goal, rendered last on
// the card line so narrow-width truncation kills the garnish before vitals.
func TestCardCarriesGoal(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running", Step: 7})
	s := stateStep(t, m)
	if s.agents[0].goal != s.manifest[0].goal {
		t.Fatalf("card goal = %q, want manifest slot %q", s.agents[0].goal, s.manifest[0].goal)
	}
	line := agentCardLine(s.agents[0])
	if !strings.Contains(line, "audit the auth flow") {
		t.Errorf("card line missing goal: %q", line)
	}
	if strings.Index(line, "audit the auth flow") < strings.Index(line, "tok") {
		t.Errorf("goal must render after the telemetry tail: %q", line)
	}
}

// TestPendingSlots: manifest slots with no card yet render as pending —
// labelled inference, dropped once the frame arrives.
func TestPendingSlots(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running"})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t3", TaskIdx: 2, Phase: "active", Status: "running"})
	s := stateStep(t, m)
	pending := s.pendingAgentLines()
	if len(pending) != 1 {
		t.Fatalf("pending lines = %d, want 1: %q", len(pending), pending)
	}
	for _, want := range []string{"SA2", "pending (not yet reported)", "write failing tests"} {
		if !strings.Contains(pending[0], want) {
			t.Errorf("pending line missing %q: %q", want, pending[0])
		}
	}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "active", Status: "running"})
	if s = stateStep(t, m); len(s.pendingAgentLines()) != 0 {
		t.Errorf("pending lines after all frames = %q", s.pendingAgentLines())
	}
}

// TestFailureAwareRollup: the collapsed head counts failures — "1/2 · 1 ✗" —
// while the success-only rollup keeps its exact shape.
func TestFailureAwareRollup(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success", TokensUsed: 1200})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "finished", Status: "error", TokensUsed: 2000})
	s := stateStep(t, m)
	if got, want := agentRollup(s), "2/2 agents · 1 ✗ · 3.2k tok"; got != want {
		t.Errorf("rollup = %q, want %q", got, want)
	}
}

// TestLiveElapsed: running cards tick client-side between frame bursts;
// terminal cards show the frame-reported duration only.
func TestLiveElapsed(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running"})
	s := stateStep(t, m)
	s.agents[0].seen = time.Now().Add(-90 * time.Second)
	if line := agentCardLine(s.agents[0]); !strings.Contains(line, "1m30s") {
		t.Errorf("live line missing elapsed: %q", line)
	}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success", DurationSeconds: 4.2})
	s = stateStep(t, m)
	if line := agentCardLine(s.agents[0]); strings.Contains(line, "1m30s") {
		t.Errorf("terminal line shows live elapsed: %q", line)
	}
}

// TestLostOnDisconnect: a socket drop retires in-flight cards — no ghost
// spinners — and leaves one quiet note instead of silence.
func TestLostOnDisconnect(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running", Step: 7})
	m.handleEvent(client.Event{Type: client.EventDisconnected})
	s := stateStep(t, m)
	card := s.agents[0]
	if !card.lost || card.finished() {
		t.Fatalf("card not marked lost: %#v", card)
	}
	if line := agentCardLine(card); !strings.Contains(line, "lost on disconnect") {
		t.Errorf("lost line missing marker: %q", line)
	}
	if line := agentCardLine(card); strings.Contains(line, "step 7") {
		t.Errorf("lost card still shows live telemetry: %q", line)
	}
	if got := strings.Join(m.notices, "\n"); !strings.Contains(got, "sub-agent state lost on disconnect") {
		t.Errorf("disconnect note missing: %q", got)
	}
	// The verdict counts lost cards as lost, not live.
	if !strings.Contains(m.msgs[0].content, "swarm: 1 lost") {
		t.Errorf("verdict missing lost bucket: %q", m.msgs[0].content)
	}
	// Frames resuming after a reconnect revive the card.
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running", Step: 8})
	if s = stateStep(t, m); s.agents[0].lost {
		t.Error("card still marked lost after frames resumed")
	}
}
