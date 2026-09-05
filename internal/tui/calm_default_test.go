package tui

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/charmbracelet/lipgloss"
)

// squishSpaces strips every whitespace rune — wrapped rail lines can
// split a marker mid-word at the panel width, so long-text assertions
// match against the squished render.
func squishSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// Calm-default contract (v0.25): reasoning previews and tool responses are
// hidden by default — only the step head lines and the reply cards paint.
// ^E (details) reveals both; per-block/per-step manual expands still work.

// TestReasoningHiddenByDefault verifies the live and finalized intent rail
// stays off unless details are on (or the block was opened deliberately).
func TestReasoningHiddenByDefault(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	chunk := "rail-head-marker" + strings.Repeat(" filler", 100) + " rail-tail-marker"
	m.handleEvent(client.Event{Type: "thinking", Content: chunk})

	// LIVE: hidden by default — no rail, no glyph, no text at all.
	rendered, _ := m.renderMessage(m.msgs[0], 0, 0)
	out := plain(rendered)
	if strings.Contains(out, "rail-head-marker") || strings.Contains(out, "rail-tail-marker") {
		t.Errorf("live reasoning must stay hidden by default:\n%s", out[:200])
	}
	if strings.Contains(out, "┊") {
		t.Errorf("live rail glyph must stay hidden by default:\n%s", out[:200])
	}

	// ^E reveals the stored block in full. (The rail wraps at the panel
	// width, so match against whitespace-squished output.)
	squish := squishSpaces
	m.Update(key("ctrl+e"))
	if !m.expandAll {
		t.Fatal("Ctrl+E should enable expandAll")
	}
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	out = plain(rendered)
	if !strings.Contains(squish(out), "rail-head-marker") || !strings.Contains(squish(out), "rail-tail-marker") {
		t.Errorf("details view should show the full reasoning block:\n%s", out)
	}

	// ^E again: hidden once more.
	m.Update(key("ctrl+e"))
	if m.expandAll {
		t.Fatal("second Ctrl+E should disable expandAll")
	}
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	out = plain(rendered)
	if strings.Contains(out, "rail-tail-marker") {
		t.Errorf("reasoning should hide again after Ctrl+E toggles off:\n%s", out[:200])
	}

	// FINALIZED: the sealed block stays hidden by default too, and a manual
	// open (tab / click) is the per-block reveal.
	m.handleEvent(client.Event{Type: "done", Latency: 0.5, ContextTokens: 10, OutputTokens: 1})
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	out = plain(rendered)
	if strings.Contains(out, "rail-tail-marker") {
		t.Errorf("finalized reasoning must stay hidden by default:\n%s", out)
	}
	m.msgs[0].items[0].open = true
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	out = plain(rendered)
	if !strings.Contains(squish(out), "rail-head-marker") || !strings.Contains(squish(out), "rail-tail-marker") {
		t.Errorf("a manually opened block should reveal its text:\n%s", out)
	}
}

// TestToolResponseHiddenByDefault verifies finished steps render head-only:
// no result peek lines unless details are on or the step was expanded.
func TestToolResponseHiddenByDefault(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()
	longRead := "peek-read-one\npeek-read-two\nEXPAND-READ-ONLY\n" + strings.Repeat("read-tail\n", 8)
	m.msgs = append(m.msgs,
		message{role: roleAsst, steps: []step{{name: "read", done: true, result: longRead}}},
		message{role: roleAsst, streaming: true, steps: []step{{name: "shell", done: true, result: "quiet-shell-beat\nEXPAND-SHELL-ONLY"}}},
	)
	m.curIdx = 1
	m.busy = true

	// Default: step heads paint, response bodies do not.
	out := plain(m.conversation())
	if !strings.Contains(out, "read") || !strings.Contains(out, "shell") {
		t.Fatalf("step head lines should still render:\n%s", out)
	}
	for _, beat := range []string{"peek-read-one", "peek-read-two", "quiet-shell-beat", "EXPAND-SHELL-ONLY"} {
		if strings.Contains(out, beat) {
			t.Errorf("tool response %q must stay hidden by default:\n%s", beat, out)
		}
	}

	// ^E: full detail for every step, cached prefix and streaming tail alike.
	m.Update(key("ctrl+e"))
	out = plain(m.conversation())
	if !strings.Contains(out, "EXPAND-READ-ONLY") || !strings.Contains(out, "EXPAND-SHELL-ONLY") {
		t.Errorf("details view should reveal full tool responses:\n%s", out)
	}

	// Per-step expand is still a deliberate reveal with details off.
	m.Update(key("ctrl+e"))
	m.toggleStep(0, 0)
	out = plain(m.conversation())
	if !strings.Contains(out, "EXPAND-READ-ONLY") {
		t.Errorf("a manually expanded step should reveal its response:\n%s", out)
	}
	if strings.Contains(out, "EXPAND-SHELL-ONLY") {
		t.Errorf("other steps stay hidden when only one is expanded:\n%s", out)
	}
}

// TestTurnHeadElapsedCounter verifies the streaming turn's head line carries
// the run's elapsed counter right-aligned at the viewport edge — the calm
// default hides the rail and step bodies, so the head is the one live clock
// in the transcript. Finalized turns drop it for the sealed telemetry stat,
// and the tail-clock lane ticks even with no running steps.
func TestTurnHeadElapsedCounter(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "hi"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true
	m.runStart = time.Now().Add(-3 * time.Second)

	m.handleEvent(client.Event{Type: "thinking", Content: "hmm"})
	rendered, _ := m.renderMessage(m.msgs[1], 1, 0)
	head := strings.Split(plain(rendered), "\n")[0]
	if !strings.HasSuffix(strings.TrimRight(head, " "), "3s") {
		t.Errorf("streaming head missing the right-aligned elapsed counter: %q", head)
	}
	if w := lipgloss.Width(head); w != m.vp.Width {
		t.Errorf("head should span the viewport width: got %d want %d\n%q", w, m.vp.Width, head)
	}

	// The transcript clock lane must tick for a streaming turn even while
	// odek thinks in silence (no running steps).
	if !m.hasLiveStepClock() {
		t.Error("a streaming turn must drive the transcript clock lane")
	}

	// Finalized: the sealed latency stat owns the head — no live counter.
	m.handleEvent(client.Event{Type: "done", Latency: 0.5, ContextTokens: 10, OutputTokens: 1})
	rendered, _ = m.renderMessage(m.msgs[1], 1, 0)
	head = strings.Split(plain(rendered), "\n")[0]
	if strings.HasSuffix(strings.TrimRight(head, " "), "3s") {
		t.Errorf("finalized head must drop the live counter:\n%s", head)
	}
}
