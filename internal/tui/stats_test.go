package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/BackendStack21/bodek/internal/tokens"
)

// driveTurn runs one full assistant turn (with a tool call and a thinking
// event) through handleEvent and returns the model, leaving the finalized
// message in place for stat-line assertions.
func driveTurn(t *testing.T, done client.Event) *Model {
	t.Helper()
	return driveTurnWith(t, newTestModel(), done)
}

// driveTurnWith is driveTurn on a caller-provided model, so tests can preset
// state (e.g. token prices) before the turn runs.
func driveTurnWith(t *testing.T, m *Model, done client.Event) *Model {
	t.Helper()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "do it"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true
	m.runStart = time.Now().Add(-3 * time.Second)
	m.sessionStart = m.runStart

	for _, ev := range []client.Event{
		{Type: "thinking", Content: "pondering"},
		{Type: "tool_call", Name: "shell", Data: `{"command":"ls"}`},
		{Type: "tool_result", Name: "shell", Data: "main.go"},
		{Type: "token", Content: "done"},
		done,
	} {
		m.handleEvent(ev)
	}
	return m
}

func TestTurnStatLine(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 2.5,
		ContextTokens: 1200, OutputTokens: 340,
		SessionContextTokens: 1200, SessionOutputTokens: 340,
	})

	if n := len(m.turnStats); n != 1 {
		t.Fatalf("turnStats len = %d, want 1", n)
	}
	ts := m.turnStats[0]
	if ts.toolCount != 1 || !ts.thought {
		t.Errorf("turn telemetry: toolCount=%d thought=%v", ts.toolCount, ts.thought)
	}
	if m.toolTotal != 1 {
		t.Errorf("toolTotal = %d, want 1", m.toolTotal)
	}
	if m.msgs[1].stats == nil {
		t.Fatal("finalized message has no stats")
	}

	out := plain(m.View())
	for _, want := range []string{"⚡ 2.5s", "⌂ 1.2k", "⎇ 340", "⚒ 1", "✳"} {
		if !strings.Contains(out, want) {
			t.Errorf("stat line missing %q in:\n%s", want, out)
		}
	}
}

// A streaming (not-yet-done) turn must render no stat line.
func TestNoStatLineWhileStreaming(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, content: "thinking", streaming: true})
	m.curIdx = 0
	if strings.Contains(plain(m.View()), "⚡") {
		t.Error("stat line should not render for a streaming turn")
	}
}

// TestGaugeFollowsTurnFill is a regression test: the header gauge must track
// the live window fill (the last request's prompt size, which drops after
// odek trims history), not sessionContextTokens, which is cumulative and only
// grows. odek reports contextTokens cumulative per run, so each done event
// below starts a fresh run with its own cumulative counter.
func TestGaugeFollowsTurnFill(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 900, OutputTokens: 100,
		SessionContextTokens: 50000, SessionOutputTokens: 300,
	})
	m.model = "big"
	m.models = []client.ModelInfo{{ID: "big", MaxContext: 1000}}
	m.resolveMaxContext()
	if out := plain(m.header()); !strings.Contains(out, "90%") {
		t.Errorf("gauge should reflect the turn fill (90%%), got:\n%s", out)
	}

	// Next turn comes back smaller after a history trim — the gauge drops,
	// even though the cumulative session total kept growing.
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.busy = true
	m.handleEvent(client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 300, OutputTokens: 100,
		SessionContextTokens: 51200, SessionOutputTokens: 400,
	})
	if m.winCtxTok != 300 {
		t.Fatalf("winCtxTok = %d, want 300", m.winCtxTok)
	}
	if out := plain(m.header()); !strings.Contains(out, "30%") {
		t.Errorf("gauge should drop after a trim (30%%), got:\n%s", out)
	}
}

// TestUsageEventRefreshesGaugeMidTurn is a regression test: odek serve emits
// a per-iteration "usage" event during a run; the header gauge must refresh
// on it instead of staying stale until "done" arrives at the end of the
// whole agent loop.
func TestUsageEventRefreshesGaugeMidTurn(t *testing.T) {
	m := newTestModel()
	m.model = "big"
	m.models = []client.ModelInfo{{ID: "big", MaxContext: 1000}}
	m.resolveMaxContext()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.busy = true
	m.status = "responding"

	m.handleEvent(client.Event{Type: "usage", ContextTokens: 400, OutputTokens: 50})
	if m.winCtxTok != 400 {
		t.Fatalf("winCtxTok = %d, want 400", m.winCtxTok)
	}
	if out := plain(m.header()); !strings.Contains(out, "40%") {
		t.Errorf("gauge should refresh mid-run (40%%), got:\n%s", out)
	}

	// A zero-usage event (provider without usage reporting) must not zero a
	// previously known fill.
	m.handleEvent(client.Event{Type: "usage"})
	if m.winCtxTok != 400 {
		t.Fatalf("winCtxTok = %d, want 400 (zero usage ignored)", m.winCtxTok)
	}

	// The mid-run state must not be disturbed: still busy, still responding.
	if !m.busy || m.status != "responding" {
		t.Errorf("usage event must not end the turn: busy=%v status=%q", m.busy, m.status)
	}
}

// TestGaugeDerivesFillFromCumulativeDeltas is a regression test: odek serve
// reports contextTokens cumulative per run (sum of prompt tokens across all
// LLM calls), so a long multi-iteration run easily exceeds the model's
// context window (e.g. 2.3M cumulative against a 1.0M window) while the
// actual window fill stays within budget. The gauge must show the delta
// between consecutive reports — the last request's prompt size — never the
// cumulative value.
func TestGaugeDerivesFillFromCumulativeDeltas(t *testing.T) {
	m := newTestModel()
	m.model = "big"
	m.models = []client.ModelInfo{{ID: "big", MaxContext: 1_000_000}}
	m.resolveMaxContext()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.busy = true
	m.status = "responding"

	// Three LLM calls with prompt sizes 600k → 800k → 900k, reported as
	// run-cumulative 600k → 1.4M → 2.3M.
	m.handleEvent(client.Event{Type: "usage", ContextTokens: 600_000, OutputTokens: 50})
	if m.winCtxTok != 600_000 {
		t.Fatalf("winCtxTok = %d, want 600000 (first report = first prompt)", m.winCtxTok)
	}
	m.handleEvent(client.Event{Type: "usage", ContextTokens: 1_400_000, OutputTokens: 100})
	if m.winCtxTok != 800_000 {
		t.Fatalf("winCtxTok = %d, want 800000 (delta of cumulative reports)", m.winCtxTok)
	}
	m.handleEvent(client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 2_300_000, OutputTokens: 150,
		SessionContextTokens: 2_300_000, SessionOutputTokens: 150,
	})
	if m.winCtxTok != 900_000 {
		t.Fatalf("winCtxTok = %d, want 900000 (delta of cumulative reports)", m.winCtxTok)
	}
	out := plain(m.header())
	if !strings.Contains(out, "90%") {
		t.Errorf("gauge should show the window fill (90%%), not cumulative overflow:\n%s", out)
	}
	if strings.Contains(out, "2.3M/") {
		t.Errorf("gauge must not render the cumulative total as window fill:\n%s", out)
	}
	// The session summary keeps tracking the cumulative total independently.
	if m.sessCtxTok != 2_300_000 {
		t.Errorf("sessCtxTok = %d, want 2300000", m.sessCtxTok)
	}

	// A trim between runs shrinks the next run's prompts; the cumulative
	// counter restarts, so the gauge drops instead of pinning at 100%.
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.busy = true
	m.handleEvent(client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 300_000, OutputTokens: 100,
		SessionContextTokens: 2_600_000, SessionOutputTokens: 250,
	})
	if m.winCtxTok != 300_000 {
		t.Fatalf("winCtxTok = %d, want 300000 (new run resets the cumulative baseline)", m.winCtxTok)
	}
	if out := plain(m.header()); !strings.Contains(out, "30%") {
		t.Errorf("gauge should drop after a trim (30%%), got:\n%s", out)
	}
}

func TestContextGauge(t *testing.T) {
	m := newTestModel()
	m.model = "big"
	m.models = []client.ModelInfo{{ID: "big", MaxContext: 1000}}
	m.resolveMaxContext()
	if m.maxContext != 1000 {
		t.Fatalf("maxContext = %d, want 1000", m.maxContext)
	}
	m.sessCtxTok = 380
	m.winCtxTok = 380

	out := plain(m.header())
	for _, want := range []string{"█▉░░░", "38%", "380/1k"} {
		if !strings.Contains(out, want) {
			t.Errorf("header gauge missing %q in:\n%s", want, out)
		}
	}
	// The gauge is the header's sole token metric: the cumulative session
	// summary (∑ ⌂ … · ⎇ …) lives in /stats and the per-turn stat line, not
	// here — a fresh session must not flash placeholder zeros in the bar.
	m.sessCtxTok, m.sessOutTok = 0, 0
	out = plain(m.header())
	for _, banned := range []string{"∑", "⌂ 0", "⎇ 0"} {
		if strings.Contains(out, banned) {
			t.Errorf("header still carries session summary %q:\n%s", banned, out)
		}
	}
	if !strings.Contains(out, "ctx") {
		t.Errorf("gauge should carry the ctx label:\n%s", out)
	}

	// The five-cell fill bar tracks the ratio with eighth-block sub-cell
	// precision (the WebUI's ctx ▓▓▓░░ idiom, sharpened): full cells are █,
	// the leading edge rounds to the nearest eighth block, the rest stays ░.
	if g := gaugeGlyph(0.80); g != "████░" {
		t.Errorf("gaugeGlyph(0.80) = %q, want ████░", g)
	}
	if g := gaugeGlyph(0.95); g != "████▊" {
		t.Errorf("gaugeGlyph(0.95) = %q, want ████▊", g)
	}
	if g := gaugeGlyph(1.0); g != "█████" {
		t.Errorf("gaugeGlyph(1.0) = %q, want █████", g)
	}

	// Unknown budget hides the gauge entirely (no percent sign in the header).
	m.maxContext = 0
	if strings.Contains(plain(m.header()), "%") {
		t.Error("gauge should be hidden when maxContext is unknown")
	}
}

func TestStatsCard(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 2.5,
		ContextTokens: 1200, OutputTokens: 340,
		SessionContextTokens: 1200, SessionOutputTokens: 340,
	})
	m.model = "big"
	m.models = []client.ModelInfo{{ID: "big", MaxContext: 4000}}
	m.resolveMaxContext()

	m.showStats()
	if m.panel != panelStats {
		t.Fatal("stats should open as a bottom sheet")
	}
	out := plain(m.View())
	for _, want := range []string{"session", "context", "output", "turns", "tools", "latency", "thinking", "active", "model"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats card missing %q in:\n%s", want, out)
		}
	}
}

func TestStatsCardEmptySession(t *testing.T) {
	m := newTestModel()
	m.showStats()
	if !strings.Contains(plain(m.View()), "no turns yet") {
		t.Error("empty stats card should show the no-turns hint")
	}
}

func TestHumanCtx(t *testing.T) {
	cases := map[int]string{
		0:         "0",
		512:       "512",
		48_200:    "48k",
		128_000:   "128k",
		999_499:   "999k", // just under the rounding seam
		999_500:   "1.0M", // whole-k rounding would reach 1000k → promote to M
		999_999:   "1.0M",
		1_000_000: "1.0M",
		1_500_000: "1.5M",
	}
	for in, want := range cases {
		if got := humanCtx(in); got != want {
			t.Errorf("humanCtx(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestEventTailNotices(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "memory_event", SubType: "merge", Target: "user", Count: 12})
	m.handleEvent(client.Event{Type: "subagent_log", SubType: "started", Name: "t0", TaskIdx: 2})

	joined := strings.Join(m.notices, "\n")
	if !strings.Contains(joined, "×12") {
		t.Errorf("missing count tail in notices: %q", joined)
	}
	if !strings.Contains(joined, "#2") {
		t.Errorf("missing task-index tail in notices: %q", joined)
	}
}

func TestApprovalOperationTags(t *testing.T) {
	m := newTestModel()
	m.approvals = []client.Event{{
		Type: "approval_request", Risk: "shell_exec", Name: "shell",
		Command: "rm -rf x", IsOperation: true, Untrusted: true,
	}}
	out := plain(m.approvalPanel())
	for _, want := range []string{"⚙ operation", "⚠ untrusted"} {
		if !strings.Contains(out, want) {
			t.Errorf("approval head missing %q in:\n%s", want, out)
		}
	}
}

func TestStatLineWidthDegradation(t *testing.T) {
	ts := turnStats{
		latency: 2.5, wall: 9 * time.Second,
		ctxTok: 1200, outTok: 340, toolCount: 3,
		toolGlyphs: []string{"❯", "◰"}, thought: true,
	}
	// Down to absurdly narrow widths the row must never exceed the viewport
	// (no wrap) and must always retain the latency essential.
	for _, w := range []int{40, 30, 24, 16, 12} {
		m := newTestModel()
		m.resize(w, 20)
		line := m.statLine(ts)
		if got, limit := lipgloss.Width(line), m.vp.Width-2; got > limit {
			t.Errorf("width %d: line width %d exceeds limit %d: %q", w, got, limit, plain(line))
		}
		if !strings.Contains(plain(line), "⚡") {
			t.Errorf("width %d: dropped latency essential: %q", w, plain(line))
		}
	}
	// At a comfortable width all three essentials survive.
	m := newTestModel()
	m.resize(80, 20)
	if line := plain(m.statLine(ts)); !strings.Contains(line, "⌂") || !strings.Contains(line, "⎇") {
		t.Errorf("essentials missing at width 80: %q", line)
	}
}

func TestSessionResumeResetsTelemetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 2.5,
		ContextTokens: 1200, OutputTokens: 340,
		SessionContextTokens: 1200, SessionOutputTokens: 340,
	})
	m.tokens = tokens.Open()
	if len(m.turnStats) == 0 || m.toolTotal == 0 || m.sessCtxTok == 0 {
		t.Fatal("precondition: session telemetry not populated by the turn")
	}

	// Resuming a different session must clear the accumulated telemetry so the
	// dashboard/header/footer don't show the previous session's data.
	m.handleSessionDetail(sessionDetailMsg{
		sess: client.Session{ID: "other", Model: "m", Messages: []client.SessionMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		}},
		token: "tok",
	})

	if len(m.turnStats) != 0 {
		t.Errorf("turnStats not reset: %d", len(m.turnStats))
	}
	if m.toolTotal != 0 {
		t.Errorf("toolTotal not reset: %d", m.toolTotal)
	}
	if !m.sessionStart.IsZero() {
		t.Error("sessionStart not reset")
	}
	if m.sessCtxTok != 0 || m.sessOutTok != 0 || m.lastLatency != 0 {
		t.Errorf("session token/latency not reset: ctx=%d out=%d lat=%v",
			m.sessCtxTok, m.sessOutTok, m.lastLatency)
	}
}

// Clearing the conversation (/clear or ctrl+l) must also reset the session
// telemetry, so the stats UI doesn't keep showing pre-clear turns, tools,
// tokens, and age.
func TestClearResetsTelemetry(t *testing.T) {
	clears := map[string]func(m *Model){
		"/clear": func(m *Model) {
			for _, c := range slashCommands() {
				if c.name == "clear" {
					c.run(m, "")
				}
			}
			m.Update(key("y")) // the two-step confirm fires the clear
		},
		"ctrl+l": func(m *Model) {
			m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
			m.Update(key("y")) // the two-step confirm fires the clear
		},
	}
	for name, clear := range clears {
		t.Run(name, func(t *testing.T) {
			m := driveTurn(t, client.Event{
				Type: "done", Latency: 2.5,
				ContextTokens: 1200, OutputTokens: 340,
				SessionContextTokens: 1200, SessionOutputTokens: 340,
			})
			if len(m.turnStats) == 0 || m.toolTotal == 0 || m.sessCtxTok == 0 {
				t.Fatal("precondition: session telemetry not populated by the turn")
			}

			clear(m)

			if len(m.msgs) != 0 {
				t.Errorf("msgs not cleared: %d", len(m.msgs))
			}
			if len(m.turnStats) != 0 {
				t.Errorf("turnStats not reset: %d", len(m.turnStats))
			}
			if m.toolTotal != 0 {
				t.Errorf("toolTotal not reset: %d", m.toolTotal)
			}
			if !m.sessionStart.IsZero() {
				t.Error("sessionStart not reset")
			}
			if m.sessCtxTok != 0 || m.sessOutTok != 0 || m.lastLatency != 0 {
				t.Errorf("session token/latency not reset: ctx=%d out=%d lat=%v",
					m.sessCtxTok, m.sessOutTok, m.lastLatency)
			}
		})
	}
}
