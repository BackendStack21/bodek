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
	for _, want := range []string{"⚡ 2.5s", "⌂ 1.2k", "↳ 340", "⚒ 1", "✳"} {
		if !strings.Contains(out, want) {
			t.Errorf("stat line missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "↗") || strings.Contains(out, "tok/s") {
		t.Errorf("stat line invented tok/s from cumulative output/latency:\n%s", out)
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

// TestGaugeUsesWindowTokens is the odek ≥ v2.3 contract: usage/done carry
// the parent conversation window directly. The gauge must take that number
// as-is — no cumulative-delta — and must not bounce on child-sized
// billing totals that used to ride contextTokens.
func TestGaugeUsesWindowTokens(t *testing.T) {
	m := newTestModel()
	m.model = "big"
	m.models = []client.ModelInfo{{ID: "big", MaxContext: 1_000_000}}
	m.resolveMaxContext()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	m.handleEvent(client.Event{Type: "usage", WindowTokens: 38_412, MaxContextTokens: 200_000, OutputTokens: 50})
	if m.winCtxTok != 38_412 {
		t.Fatalf("winCtxTok = %d, want 38412", m.winCtxTok)
	}
	if m.maxContext != 200_000 {
		t.Fatalf("maxContext = %d, want 200000 (wire beats catalog)", m.maxContext)
	}
	if out := plain(m.header()); !strings.Contains(out, "19%") {
		t.Errorf("gauge should use the parent window against the wire limit (19%%), got:\n%s", out)
	}

	// A later parent call shrinks after trim — windowTokens drops; the
	// gauge must follow instead of holding the high-water mark.
	m.handleEvent(client.Event{Type: "usage", WindowTokens: 20_000, MaxContextTokens: 200_000})
	if m.winCtxTok != 20_000 {
		t.Fatalf("winCtxTok = %d, want 20000", m.winCtxTok)
	}

	// Absent windowTokens holds the fill (not-reported, never "empty").
	m.handleEvent(client.Event{Type: "usage", OutputTokens: 10})
	if m.winCtxTok != 20_000 {
		t.Fatalf("absent windowTokens must hold the gauge: %d", m.winCtxTok)
	}

	m.handleEvent(client.Event{
		Type: "done", Latency: 1,
		WindowTokens: 41_000, MaxContextTokens: 200_000,
		InputTokens: 152_300, OutputTokens: 800,
		SessionContextTokens: 300_000, SessionOutputTokens: 40_000,
	})
	if m.winCtxTok != 41_000 {
		t.Fatalf("done windowTokens = %d, want 41000", m.winCtxTok)
	}
	if m.turnStats[0].ctxTok != 152_300 {
		t.Fatalf("receipt ctxTok = %d, want 152300 (billing inputTokens, not the window)", m.turnStats[0].ctxTok)
	}
	if m.sessCtxTok != 300_000 {
		t.Fatalf("sessCtxTok = %d, want 300000", m.sessCtxTok)
	}
}

// TestGaugeHonestOverrun pins the header when the advertised max is
// below the provider-reported parent window (stale catalog or last-resort
// miss). The bar fills; the label must not say 100% next to 270k/131k.
func TestGaugeHonestOverrun(t *testing.T) {
	m := newTestModel()
	m.model = "deepseek-v4.1"
	m.models = []client.ModelInfo{{ID: "deepseek-v4.1", MaxContext: 131_072}}
	m.resolveMaxContext()
	m.handleEvent(client.Event{Type: "usage", WindowTokens: 270_000, MaxContextTokens: 131_072})
	if m.winCtxTok != 270_000 {
		t.Fatalf("winCtxTok = %d, want 270000 (provider parent window)", m.winCtxTok)
	}
	if m.maxContext != 131_072 {
		t.Fatalf("maxContext = %d, want 131072 (wire/catalog, not invented)", m.maxContext)
	}
	out := plain(m.ctxGauge(false))
	if !strings.Contains(out, "206%") {
		t.Errorf("overrun gauge = %q, want 206%% (270k/131k)", out)
	}
	if strings.Contains(out, "100%") {
		t.Errorf("overrun must not relabel as 100%%: %q", out)
	}
	if !strings.Contains(out, "270k/131k") {
		t.Errorf("overrun gauge = %q, want the raw 270k/131k fraction", out)
	}
}

func TestGaugeHoldsOnDoneWithoutWindow(t *testing.T) {
	m := newTestModel()
	m.winCtxTok = 41_000
	m.busy = true
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.handleEvent(client.Event{Type: "done", Latency: 0.5, InputTokens: 100, OutputTokens: 10})
	if m.winCtxTok != 41_000 {
		t.Fatalf("done without windowTokens zeroed the gauge: %d", m.winCtxTok)
	}
}

func TestMaxContextWireClearsOnModelChange(t *testing.T) {
	m := newTestModel()
	m.model = "big"
	m.models = []client.ModelInfo{
		{ID: "big", MaxContext: 1000},
		{ID: "small", MaxContext: 500},
	}
	m.maxContextWire = 200_000
	m.resolveMaxContext()
	if m.maxContext != 200_000 {
		t.Fatalf("wire limit should win: %d", m.maxContext)
	}
	m.applyModelChoice("small")
	if m.maxContextWire != 0 {
		t.Fatal("model change must drop the previous wire limit")
	}
	if m.maxContext != 500 {
		t.Fatalf("catalog limit after switch = %d, want 500", m.maxContext)
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
	// summary (∑ ⌂ … · ↳ …) lives in /stats and the per-turn stat line, not
	// here — a fresh session must not flash placeholder zeros in the bar.
	m.sessCtxTok, m.sessOutTok = 0, 0
	out = plain(m.header())
	for _, banned := range []string{"∑", "⌂ 0", "↳ 0"} {
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
	if line := plain(m.statLine(ts)); !strings.Contains(line, "⌂") || !strings.Contains(line, "↳") {
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
	m.tokPerSec = 25.2
	m.tokPerSecKind = client.TokPerSecGeneration

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
	if m.sessCtxTok != 0 || m.sessOutTok != 0 || m.lastLatency != 0 || m.tokPerSec != 0 {
		t.Errorf("session token/latency/speed not reset: ctx=%d out=%d lat=%v tok/s=%v",
			m.sessCtxTok, m.sessOutTok, m.lastLatency, m.tokPerSec)
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
			m.tokPerSec = 25.2
			m.tokPerSecKind = client.TokPerSecGeneration

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
			if m.sessCtxTok != 0 || m.sessOutTok != 0 || m.lastLatency != 0 || m.tokPerSec != 0 {
				t.Errorf("session token/latency/speed not reset: ctx=%d out=%d lat=%v tok/s=%v",
					m.sessCtxTok, m.sessOutTok, m.lastLatency, m.tokPerSec)
			}
		})
	}
}

func TestFormatTokPerSec(t *testing.T) {
	cases := map[float64]string{
		0:    "",
		-1:   "",
		0.04: "",
		9.6:  "9.6 tok/s",
		25.2: "25.2 tok/s",
		200:  "200.0 tok/s",
	}
	for in, want := range cases {
		if got := formatTokPerSec(in); got != want {
			t.Errorf("formatTokPerSec(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestTurnStatLineShowsTokPerSec(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 2.5,
		ContextTokens: 1200, OutputTokens: 340,
		SessionContextTokens: 1200, SessionOutputTokens: 340,
		TokensPerSecond: 9.6, GenerationTokensPerSecond: 25.2,
		TTFTMs: 420, CallDurationMs: 8100, LLMDurationMs: 8100,
	})
	ts := m.turnStats[0]
	if ts.tokPerSec != 25.2 || ts.tokPerSecKind != client.TokPerSecGeneration {
		t.Fatalf("tok/s = %v %q, want 25.2 generation", ts.tokPerSec, ts.tokPerSecKind)
	}
	if ts.ttftMs != 420 || ts.callDurMs != 8100 || ts.llmDurMs != 8100 {
		t.Fatalf("timing = ttft %d call %d llm %d", ts.ttftMs, ts.callDurMs, ts.llmDurMs)
	}
	foot := plain(m.turnStatFoot(m.msgs[1]))
	if !strings.Contains(foot, "↗") || !strings.Contains(foot, "25.2 tok/s") {
		t.Errorf("turn foot missing generation tok/s: %q", foot)
	}
	// The header no longer carries tok/s — the cockpit owns the live rate.
	if strings.Contains(plain(m.header()), "tok/s") {
		t.Errorf("header must not show tok/s:\n%s", plain(m.header()))
	}
}

func TestChromeFooterOmitsTokPerSec(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 2.5, OutputTokens: 340,
		GenerationTokensPerSecond: 25.2,
		SessionContextTokens:      100, SessionOutputTokens: 340,
	})
	m.sendPrompt("next")
	m.handleEvent(client.Event{Type: "usage", TokensPerSecond: 9.6})
	// The header no longer carries the in-flight rate — the cockpit owns it;
	// the sealed turn foot keeps the last sealed rate.
	if strings.Contains(plain(m.header()), "tok/s") {
		t.Errorf("header must not show tok/s:\n%s", plain(m.header()))
	}
	foot := plain(m.footer())
	if strings.Contains(foot, "tok/s") {
		t.Errorf("chrome footer must not carry tok/s (header + turn foot own it): %q", foot)
	}
	if got := plain(m.turnStatFoot(m.msgs[1])); !strings.Contains(got, "25.2 tok/s") {
		t.Errorf("sealed turn foot missing previous rate: %q", got)
	}
}

func TestUsageAppliesLiveSpeed(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.status = "responding"

	m.handleEvent(client.Event{Type: "usage", GenerationTokensPerSecond: 25.2, TokensPerSecond: 9.6, TTFTMs: 420, CallDurationMs: 8100})
	if m.tokPerSec != 25.2 || m.tokPerSecKind != client.TokPerSecGeneration {
		t.Fatalf("live tok/s = %v %q, want 25.2 generation", m.tokPerSec, m.tokPerSecKind)
	}
	if m.ttftMs != 420 || m.callDurMs != 8100 {
		t.Fatalf("live timing = ttft %d call %d", m.ttftMs, m.callDurMs)
	}
	if out := plain(m.header()); strings.Contains(out, "25.2 tok/s") {
		t.Errorf("header must not show tok/s: %q", out)
	}
	if m.msgs[0].stats != nil {
		t.Fatal("usage must not seal turn stats")
	}
	if foot := plain(m.turnStatFoot(m.msgs[0])); foot != "" {
		t.Errorf("streaming turn foot must stay empty, got %q", foot)
	}

	m.handleEvent(client.Event{Type: "usage", OutputTokens: 10})
	if m.tokPerSec != 25.2 || m.ttftMs != 420 {
		t.Fatalf("zero usage zeroed held metrics: tok/s=%v ttft=%d", m.tokPerSec, m.ttftMs)
	}

	m.winCtxTok = 400
	m.handleEvent(client.Event{Type: "usage", TokensPerSecond: 9.6, CallDurationMs: 80, CallOutputTokens: 16})
	if m.tokPerSec != 9.6 || m.tokPerSecKind != client.TokPerSecE2E {
		t.Fatalf("last usage should update tok/s: %v %q", m.tokPerSec, m.tokPerSecKind)
	}
	if m.winCtxTok != 400 {
		t.Fatalf("usage without windowTokens zeroed the gauge: %d", m.winCtxTok)
	}
}

func TestUsageFirstRemoteTurnResetsThenApplies(t *testing.T) {
	m := newTestModel()
	m.tokPerSec = 40
	m.tokPerSecKind = client.TokPerSecGeneration
	m.handleEvent(client.Event{Type: "usage", TokensPerSecond: 9.6})
	if m.cur() < 0 {
		t.Fatal("usage-first remote turn must open a card")
	}
	if m.tokPerSec != 9.6 || m.tokPerSecKind != client.TokPerSecE2E {
		t.Fatalf("usage-first applied after reset: %v %q", m.tokPerSec, m.tokPerSecKind)
	}
}

func TestDoneFallsBackToLiveUsageRate(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "do it"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true
	m.handleEvent(client.Event{Type: "usage", GenerationTokensPerSecond: 25.2, TTFTMs: 340, CallDurationMs: 2000})
	m.handleEvent(client.Event{Type: "done", Latency: 1, OutputTokens: 80,
		SessionContextTokens: 100, SessionOutputTokens: 80})
	ts := m.turnStats[0]
	if ts.tokPerSec != 25.2 || ts.ttftMs != 340 || ts.callDurMs != 2000 {
		t.Fatalf("done without metrics dropped live usage: %+v", ts)
	}
}

func TestDonePrefersOwnRateOverLive(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "usage", TokensPerSecond: 9.6})
	m.handleEvent(client.Event{Type: "done", Latency: 1, GenerationTokensPerSecond: 25.2,
		SessionContextTokens: 10, SessionOutputTokens: 4})
	if m.turnStats[0].tokPerSec != 25.2 {
		t.Fatalf("done rate should win over live usage: %v", m.turnStats[0].tokPerSec)
	}
}

func TestSecondTurnWithoutRateDoesNotInherit(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 1, GenerationTokensPerSecond: 40,
		SessionContextTokens: 100, SessionOutputTokens: 10,
	})
	m.sendPrompt("next")
	m.handleEvent(client.Event{Type: "done", Latency: 1, OutputTokens: 4,
		SessionContextTokens: 104, SessionOutputTokens: 14})
	if n := len(m.turnStats); n != 2 {
		t.Fatalf("turnStats = %d, want 2", n)
	}
	if m.turnStats[1].tokPerSec != 0 || m.turnStats[1].tokPerSecKind != "" {
		t.Fatalf("second turn inherited previous tok/s: %+v", m.turnStats[1])
	}
	if m.turnStats[0].tokPerSec != 40 {
		t.Fatalf("first turn rate clobbered: %v", m.turnStats[0].tokPerSec)
	}
}

func TestSecondTurnFallsBackToThisTurnUsage(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 1, GenerationTokensPerSecond: 40,
		SessionContextTokens: 100, SessionOutputTokens: 10,
	})
	m.sendPrompt("next")
	m.handleEvent(client.Event{Type: "usage", GenerationTokensPerSecond: 25.2})
	m.handleEvent(client.Event{Type: "done", Latency: 1, OutputTokens: 4,
		SessionContextTokens: 104, SessionOutputTokens: 14})
	if m.turnStats[1].tokPerSec != 25.2 {
		t.Fatalf("second turn should seal this-turn usage: %v", m.turnStats[1].tokPerSec)
	}
}

func TestNewTurnResetsSpeedChip(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 1,
		GenerationTokensPerSecond: 25.2,
		SessionContextTokens:      100, SessionOutputTokens: 10,
	})
	if m.tokPerSec != 25.2 {
		t.Fatal("precondition: done should keep the last-call chip")
	}
	m.sendPrompt("next")
	if m.tokPerSec != 0 || m.tokPerSecKind != "" || m.ttftMs != 0 || m.callDurMs != 0 {
		t.Fatalf("new turn kept previous metrics: tok/s=%v ttft=%d call=%d", m.tokPerSec, m.ttftMs, m.callDurMs)
	}
	if strings.Contains(plain(m.header()), "tok/s") {
		t.Errorf("header still shows previous turn's rate:\n%s", plain(m.header()))
	}
}

func TestBeginWireTurnResetsSpeedChip(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 1,
		GenerationTokensPerSecond: 25.2, TTFTMs: 300,
		SessionContextTokens: 100, SessionOutputTokens: 10,
	})
	m.beginWireTurn(true)
	if m.tokPerSec != 0 || m.tokPerSecKind != "" || m.ttftMs != 0 {
		t.Fatalf("wake turn kept previous metrics: tok/s=%v ttft=%d", m.tokPerSec, m.ttftMs)
	}
	if strings.Contains(plain(m.header()), "tok/s") {
		t.Errorf("header still shows previous turn's rate:\n%s", plain(m.header()))
	}
}

func TestStatsCardSpeedAndTTFT(t *testing.T) {
	m := newTestModel()
	m.sessionStart = time.Now()
	m.turnStats = []turnStats{
		{latency: 1.0, tokPerSec: 20.0, tokPerSecKind: client.TokPerSecGeneration, ttftMs: 300, callDurMs: 2000, llmDurMs: 2000},
		{latency: 2.0, tokPerSec: 30.0, tokPerSecKind: client.TokPerSecGeneration, ttftMs: 500, callDurMs: 4000, llmDurMs: 4000},
	}
	out := plain(m.statsBody())
	for _, want := range []string{"speed", "30.0 tok/s", "generation", "mean 25.0 tok/s", "4.0s call", "ttft", "400ms", "slowest 500ms", "llm", "6.0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats body missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "peak 30") {
		t.Errorf("peak should omit when it equals the last rate:\n%s", out)
	}
}

func TestStatsCardIndependentPerfRows(t *testing.T) {
	m := newTestModel()
	m.sessionStart = time.Now()
	m.turnStats = []turnStats{{latency: 1, tokPerSec: 9.6, tokPerSecKind: client.TokPerSecE2E}}
	out := plain(m.statsBody())
	if !strings.Contains(out, "speed") || !strings.Contains(out, "9.6 tok/s") || !strings.Contains(out, "e2e") {
		t.Errorf("speed-only card missing e2e rate:\n%s", out)
	}
	if strings.Contains(out, "ttft") || strings.Contains(out, "llm") {
		t.Errorf("speed-only card leaked ttft/llm:\n%s", out)
	}

	m.turnStats = []turnStats{{latency: 1, ttftMs: 340}}
	out = plain(m.statsBody())
	if !strings.Contains(out, "ttft") {
		t.Errorf("ttft-only card missing ttft:\n%s", out)
	}
	if strings.Contains(out, "speed") || strings.Contains(out, "tok/s") || strings.Contains(out, "llm") {
		t.Errorf("ttft-only card leaked speed/llm:\n%s", out)
	}

	m.turnStats = []turnStats{{latency: 1, llmDurMs: 2500}}
	out = plain(m.statsBody())
	if !strings.Contains(out, "llm") || !strings.Contains(out, "2.5s") {
		t.Errorf("llm-only card missing duration:\n%s", out)
	}
	if strings.Contains(out, "speed") || strings.Contains(out, "ttft") {
		t.Errorf("llm-only card leaked speed/ttft:\n%s", out)
	}

	m.turnStats = []turnStats{
		{latency: 1, tokPerSec: 20},
		{latency: 1},
		{latency: 1, tokPerSec: 30},
	}
	out = plain(m.statsBody())
	if !strings.Contains(out, "30.0 tok/s") || !strings.Contains(out, "mean 25.0 tok/s") {
		t.Errorf("zero-rate turns must not dilute the mean:\n%s", out)
	}
}

func TestStatsCardOmitsSpeedWithoutRates(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 2.5,
		ContextTokens: 1200, OutputTokens: 340,
		SessionContextTokens: 1200, SessionOutputTokens: 340,
	})
	out := plain(m.statsBody())
	for _, banned := range []string{"speed", "tok/s", "ttft", "llm"} {
		if strings.Contains(out, banned) {
			t.Errorf("stats body invented %q without this-call metrics:\n%s", banned, out)
		}
	}
}

func TestStatLineWidthKeepsLatencyWithTokPerSec(t *testing.T) {
	ts := turnStats{
		latency: 2.5, wall: 9 * time.Second,
		ctxTok: 1200, outTok: 340, tokPerSec: 25.2,
		toolCount: 3, toolGlyphs: []string{"❯", "◰"}, thought: true,
	}
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
	m := newTestModel()
	m.resize(80, 20)
	if line := plain(m.statLine(ts)); !strings.Contains(line, "↗") || !strings.Contains(line, "25.2 tok/s") {
		t.Errorf("tok/s missing at width 80: %q", line)
	}
	// Tools (drop 1) shed before tok/s (drop 2).
	m.resize(50, 20)
	line := plain(m.statLine(ts))
	if !strings.Contains(line, "↗") {
		t.Errorf("tok/s should survive after tools drop: %q", line)
	}
}
