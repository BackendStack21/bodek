package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// Sub-agent LLM calls cost real money, and odek reports each finished
// task's final spend (cost_usd on the finished subagent_state frame —
// wire v2 P6). The session-cost surfaces — header, /stats, and the
// cockpit cap row — must add that spend on top of the main-loop token
// estimate, summed once per task id so replayed frames never double-count.

// subCostFixture: prices configured; two sub-agent tasks finish with
// engine-reported costs; the main loop burns $0.016 of tokens
// (10k in @ $1/M + 2k out @ $3/M).
func subCostFixture(t *testing.T) *Model {
	t.Helper()
	m := newTestModel()
	m.limits = client.Limits{InputCostPerMillionUSD: 1.0, OutputCostPerMillionUSD: 3.0}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "started", Status: "running"})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "started", Status: "running"})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success", CostUSD: 0.0125})
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "finished", Status: "success", CostUSD: 0.005})
	return driveTurnWith(t, m, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
		SessionContextTokens: 10_000, SessionOutputTokens: 2_000,
	})
}

// TestSubagentCostAddsToHeader: header session spend = main loop + the
// finished tasks' engine-reported costs ($0.016 + $0.0175 = $0.0335).
func TestSubagentCostAddsToHeader(t *testing.T) {
	m := subCostFixture(t)
	if out := plain(m.header()); !strings.Contains(out, "$0.0335") {
		t.Errorf("header missing sub-agent-inclusive session cost:\n%s", out)
	}
}

// TestSubagentCostReplayIsIdempotent: duplicated/late finished frames
// upsert per task id — the total stays $0.0335, never doubles.
func TestSubagentCostReplayIsIdempotent(t *testing.T) {
	m := subCostFixture(t)
	for _, ev := range []client.Event{
		{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success", CostUSD: 0.0125},
		{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "finished", Status: "success", CostUSD: 0.005},
	} {
		m.handleEvent(ev)
	}
	if out := plain(m.header()); !strings.Contains(out, "$0.0335") {
		t.Errorf("replayed finish frames changed the session cost:\n%s", out)
	}
}

// TestStatsCardIncludesSubagentCost: the /stats cost row carries the
// finished task's spend too ($0.016 + $0.0125 = $0.0285).
func TestStatsCardIncludesSubagentCost(t *testing.T) {
	m := newTestModel()
	m.limits = client.Limits{
		InputCostPerMillionUSD:  1.0,
		OutputCostPerMillionUSD: 3.0,
		MaxCostUSD:              5,
	}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success", CostUSD: 0.0125})
	m = driveTurnWith(t, m, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
		SessionContextTokens: 10_000, SessionOutputTokens: 2_000,
	})
	m.showStats()
	if out := plain(m.View()); !strings.Contains(out, "$0.0285") {
		t.Errorf("stats card missing sub-agent-inclusive cost row:\n%s", out)
	}
}

// TestCockpitCapRowIncludesSubagentCost: the cap row compares total spend
// (main + sub-agents) against the configured cost cap.
func TestCockpitCapRowIncludesSubagentCost(t *testing.T) {
	m := subCostFixture(t)
	m.limits.MaxCostUSD = 0.5
	out := plain(m.cockpitBudgetSection())
	if !strings.Contains(out, "$0.0335") || !strings.Contains(out, "$0.50") {
		t.Errorf("cap row missing sub-agent-inclusive spend:\n%s", out)
	}
}

// TestSubagentNoCostReportsNothing: a task that finishes without a
// reported cost adds nothing — absent cost is unavailable, never $0 —
// so the header keeps showing the plain main-loop estimate.
func TestSubagentNoCostReportsNothing(t *testing.T) {
	m := newTestModel()
	m.limits = client.Limits{InputCostPerMillionUSD: 1.0, OutputCostPerMillionUSD: 3.0}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success"})
	m = driveTurnWith(t, m, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
		SessionContextTokens: 10_000, SessionOutputTokens: 2_000,
	})
	if out := plain(m.header()); !strings.Contains(out, "$0.016") {
		t.Errorf("absent sub-agent cost must not distort session cost:\n%s", out)
	}
}

// TestClearResetsSubagentCost: /clear wipes every session counter — the
// recorded sub-agent costs included — so a post-clear turn shows only the
// new session's spend.
func TestClearResetsSubagentCost(t *testing.T) {
	m := newTestModel()
	m.limits = client.Limits{InputCostPerMillionUSD: 1.0, OutputCostPerMillionUSD: 3.0}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success", CostUSD: 0.0125})
	m.Update(key("ctrl+l"))
	m.Update(key("y"))
	m = driveTurnWith(t, m, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
		SessionContextTokens: 10_000, SessionOutputTokens: 2_000,
	})
	if out := plain(m.header()); !strings.Contains(out, "$0.016") {
		t.Errorf("clear did not reset sub-agent cost — header shows:\n%s", out)
	}
}
