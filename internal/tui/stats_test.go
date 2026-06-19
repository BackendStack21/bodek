package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/BackendStack21/bodek/internal/tokens"
)

// driveTurn runs one full assistant turn (with a tool call and a thinking
// event) through handleEvent and returns the model, leaving the finalized
// message in place for stat-line assertions.
func driveTurn(t *testing.T, done client.Event) *Model {
	t.Helper()
	m := newTestModel()
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

func TestContextGauge(t *testing.T) {
	m := newTestModel()
	m.model = "big"
	m.models = []client.ModelInfo{{ID: "big", MaxContext: 1000}}
	m.resolveMaxContext()
	if m.maxContext != 1000 {
		t.Fatalf("maxContext = %d, want 1000", m.maxContext)
	}
	m.sessCtxTok = 380

	out := plain(m.header())
	for _, want := range []string{"◑", "38%", "380/1k"} {
		if !strings.Contains(out, want) {
			t.Errorf("header gauge missing %q in:\n%s", want, out)
		}
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
	last := m.msgs[len(m.msgs)-1]
	if !last.raw {
		t.Fatal("stats card should be a raw message")
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
	m.approval = &client.Event{
		Type: "approval_request", Risk: "shell_exec", Name: "shell",
		Command: "rm -rf x", IsOperation: true, Untrusted: true,
	}
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
