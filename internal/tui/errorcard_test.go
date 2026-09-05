package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── provider-failure classification + retry affordance ──────────────────────
//
// A provider failure (stream stall under parallel-agent load, HTTP 429 after
// the client retry budget, dropped connection) lands as a raw wire string on
// the error event. The transcript must show what happened in plain language,
// attached to the failed turn, with a visible way to resend the preserved
// prompt — not an LLM-internal error line buried in a side note.

// TestClassifyErr covers the classifier's routing table.
func TestClassifyErr(t *testing.T) {
	cases := []struct {
		msg  string
		want errClass
	}{
		{"iteration 17: llm: stream idle for over 2m0s without an event", errStall},
		{"llm: stream idle for over 2m0s without an event", errStall},
		{"llm: stream idle timeout", errStall},
		{"iteration 4: llm: stream idle for over 5m0s without an event", errStall},
		{"llm: provider rate limit (status 429) after 8 attempts", errRateLimit},
		{"iteration 3: llm: read stream: read tcp 1.2.3.4:443->5.6.7.8:55: read: connection reset by peer", errConnDrop},
		{"context deadline exceeded", errTimeout},
		{"request timed out", errTimeout},
		{"write broke", errOther},
		{"invalid model ID", errOther},
	}
	for _, c := range cases {
		if got := classifyErr(c.msg); got != c.want {
			t.Errorf("classifyErr(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// errorTurn wires a model through a prompt, a streamed reply fragment, and a
// provider failure — the typical mid-run stall shape.
func errorTurn(t *testing.T, rawErr string) *Model {
	t.Helper()
	m := newTestModel()
	m.sendPrompt("diagnose the loop")
	m.handleEvent(client.Event{Type: "token_delta", Content: "partial answer before the failure"})
	m.handleEvent(client.Event{Type: "error", Message: rawErr})
	return m
}

// TestErrorCardAttachesToTurnWithContent pins F2: a mid-run failure attaches
// the classified card to the turn that was streaming — below the reply —
// instead of degrading to a side note.
func TestErrorCardAttachesToTurnWithContent(t *testing.T) {
	m := errorTurn(t, "iteration 17: llm: stream idle for over 2m0s without an event")

	msg := m.msgs[len(m.msgs)-1]
	if msg.content == "" {
		t.Fatal("error event lost the turn entirely")
	}
	if !strings.HasPrefix(msg.content, "partial answer") {
		t.Errorf("reply must survive ahead of the card, got %q", msg.content)
	}
	if !strings.Contains(msg.content, "Provider stream stalled") {
		t.Errorf("classified card missing from the turn: %q", msg.content)
	}
	if strings.Contains(msg.content, "llm: stream idle") {
		t.Errorf("raw wire text leaked into the card: %q", msg.content)
	}
	for _, n := range m.notices {
		if strings.Contains(n, "llm: stream idle") {
			t.Errorf("error degraded to a side note: %v", m.notices)
		}
	}
	if m.status != "error" {
		t.Errorf("status = %q, want error", m.status)
	}
	if m.busy {
		t.Error("failed turn must not stay busy")
	}
}

// TestErrorCardRetriablePrompt pins F3's card copy: when the failed prompt is
// resendable, the card says so.
func TestErrorCardRetriablePrompt(t *testing.T) {
	m := errorTurn(t, "llm: provider rate limit (status 429) after 8 attempts")
	card := m.msgs[len(m.msgs)-1].content
	if !strings.Contains(card, "429") {
		t.Errorf("rate-limit card lost the classification: %q", card)
	}
	if !strings.Contains(card, "⏎ resends") {
		t.Errorf("retriable card missing the resend hint: %q", card)
	}
}

// TestErrorCardUnknownStaysRaw keeps the fallback: unrecognized failures
// still show their (sanitized) message on the turn.
func TestErrorCardUnknownStaysRaw(t *testing.T) {
	m := errorTurn(t, "boom \x1b[31mevil")
	card := m.msgs[len(m.msgs)-1].content
	if !strings.Contains(card, "**Error:**") || !strings.Contains(card, "boom") {
		t.Errorf("unknown failure must fall back to the raw card: %q", card)
	}
	if strings.ContainsRune(card, 0x1b) {
		t.Errorf("unsanitized wire text in the card: %q", card)
	}
}

// TestEnterRetriesAfterError pins F3's key path: ⏎ on an empty input right
// after a failed turn resends the preserved prompt.
func TestEnterRetriesAfterError(t *testing.T) {
	m := errorTurn(t, "iteration 17: llm: stream idle for over 2m0s without an event")
	n := len(m.msgs)

	var cmd tea.Cmd
	_, cmd = m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("⏎ after a failed turn with an empty input must retry")
	}
	if !m.busy || m.status != "thinking" {
		t.Errorf("retry did not start a turn: busy=%v status=%q", m.busy, m.status)
	}
	if len(m.msgs) != n+2 {
		t.Fatalf("retry did not open a new user/assistant pair: %d msgs", len(m.msgs))
	}
	if got := m.msgs[len(m.msgs)-2].content; got != "diagnose the loop" {
		t.Errorf("retry resent %q, want the failed prompt", got)
	}
}

// TestEnterIdleAfterErrorWithoutPrompt keeps ⏎ inert when there is nothing
// to retry (fresh model, no failed prompt).
func TestEnterIdleAfterErrorWithoutPrompt(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "error", Message: "boom"})
	if _, cmd := m.Update(key("enter")); cmd != nil {
		t.Error("⏎ with no failed prompt must stay inert")
	}
}

// TestFooterRetryHintOnError pins the footer's error-state hint, mirroring
// the disconnected ⏎-redial hint: shown only when it can actually fire.
func TestFooterRetryHintOnError(t *testing.T) {
	m := errorTurn(t, "iteration 17: llm: stream idle for over 2m0s without an event")
	if foot := plain(m.footer()); !strings.Contains(foot, "retry") {
		t.Errorf("error footer missing retry hint: %q", foot)
	}

	// A draft keeps typing sacred — the hint only promises what ⏎ does.
	m.ta.SetValue("a fresh thought")
	if foot := plain(m.footer()); strings.Contains(foot, "retry") {
		t.Errorf("retry hint must hide while a draft exists: %q", foot)
	}
}

// TestErrorCardStallDoesNotHardcodeTwoMinutes pins the odek v2.1.0 budgets:
// the stall card must not claim a 2-minute silence, and the timeout card
// must point at request_timeout_seconds (default 300s).
func TestErrorCardStallDoesNotHardcodeTwoMinutes(t *testing.T) {
	m := errorTurn(t, "llm: stream idle timeout")
	card := m.msgs[len(m.msgs)-1].content
	if strings.Contains(card, "2m") {
		t.Errorf("stall card still hardcodes the old 120s budget: %q", card)
	}
	if !strings.Contains(card, "idle timeout") {
		t.Errorf("stall card missing idle-timeout wording: %q", card)
	}
	if !strings.Contains(card, "stream_idle_timeout_seconds") {
		t.Errorf("stall card missing the config knob: %q", card)
	}

	to := errorTurn(t, "context deadline exceeded")
	got := to.msgs[len(to.msgs)-1].content
	if !strings.Contains(got, "request_timeout_seconds") {
		t.Errorf("timeout card missing the config knob: %q", got)
	}
}
