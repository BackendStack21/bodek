package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestThinkingAccordion verifies the intent-rail contract: the full block
// is stored, hidden in the calm default (live and finalized alike), and
// expandAll / a manual open unfold the stored text.
func TestThinkingAccordion(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	// Stream a thinking chunk well over the excerpt cap.
	chunk := "head-marker" + strings.Repeat(" filler", 100) + " tail-marker"
	m.handleEvent(client.Event{Type: "thinking", Content: chunk})

	if len(m.msgs[0].items) != 1 || !m.msgs[0].items[0].thinking {
		t.Fatalf("expected one thinking item, got %+v", m.msgs[0].items)
	}
	if block := m.msgs[0].items[0].text; block != chunk {
		t.Errorf("thinking block should be stored in full, got %d of %d bytes", len(block), len(chunk))
	}

	// Calm default: LIVE reasoning stays hidden — no rail, no glyph, no
	// text. The status line is the only signal while odek thinks.
	rendered, _ := m.renderMessage(m.msgs[0], 0, 0)
	out := plain(rendered)
	if strings.Contains(out, "head-marker") || strings.Contains(out, "tail-marker") {
		t.Errorf("live reasoning must stay hidden by default:\n%s", out[:200])
	}
	if strings.Contains(out, "┊") {
		t.Errorf("live rail glyph must stay hidden by default:\n%s", out[:200])
	}

	// ^E unfolds the whole block while the turn is still streaming.
	m.expandAll = true
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	out = plain(rendered)
	if !strings.Contains(squishSpaces(out), "head-marker") || !strings.Contains(squishSpaces(out), "tail-marker") {
		t.Errorf("details view should unfold the live block in full:\n%s", out)
	}
	if !strings.Contains(out, "┊") {
		t.Errorf("details view missing the intent glyph:\n%s", out)
	}
	m.expandAll = false

	// Finalize: the sealed block stays hidden by default…
	m.handleEvent(client.Event{Type: "done", Latency: 0.5, ContextTokens: 10, OutputTokens: 1})
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	out = plain(rendered)
	if strings.Contains(out, "tail-marker") {
		t.Errorf("finalized reasoning must stay hidden by default:\n%s", out)
	}
	if m.msgs[0].items[0].open {
		t.Error("finalize should close the renderer-opened block")
	}
	// …and ^E renders it whole with the thinking meta line.
	m.expandAll = true
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	out = plain(rendered)
	if !strings.Contains(squishSpaces(out), "tail-marker") {
		t.Errorf("details view lost the finalized block:\n%s", out)
	}
	if !strings.Contains(out, "thinking") {
		t.Errorf("details view missing the thinking meta:\n%s", out)
	}
	m.expandAll = false

	// A subsequent event extends the same block (checked live, above).
	m2 := newTestModel()
	m2.msgs = append(m2.msgs, message{role: roleAsst, streaming: true})
	m2.curIdx = 0
	m2.busy = true
	m2.handleEvent(client.Event{Type: "thinking", Content: chunk})
	m2.handleEvent(client.Event{Type: "thinking", Content: " final thought"})
	if len(m2.msgs[0].items) != 1 {
		t.Fatalf("thinking delta opened a new block: %+v", m2.msgs[0].items)
	}
	if !strings.HasSuffix(m2.msgs[0].items[0].text, "final thought") {
		t.Errorf("latest thinking not retained: %q", m2.msgs[0].items[0].text)
	}
}

// TestThinkingManualOpenPersists verifies tab opens the most recent finalized
// block and the manual open survives the renderer's auto-collapse (it stays
// open; reopening after a later turn does not re-close it).
func TestThinkingManualOpenPersists(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	thought := "head-marker" + strings.Repeat(" filler", 100) + " tail-marker"
	m.handleEvent(client.Event{Type: "thinking", Content: thought})
	m.handleEvent(client.Event{Type: "done", Latency: 0.5, ContextTokens: 10, OutputTokens: 1})

	// Tab opens the block — the full text renders.
	m.toggleThinkingLast()
	if !m.msgs[0].items[0].open {
		t.Fatal("tab did not open the reasoning block")
	}
	rendered, _ := m.renderMessage(m.msgs[0], 0, 0)
	if out := plain(rendered); !strings.Contains(squishSpaces(out), "tail-marker") {
		t.Errorf("manually opened block should render in full:\n%s", out[:200])
	}

	// ^E (expandAll) also unfolds finalized blocks.
	m.msgs[0].items[0].open = false
	m.expandAll = true
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	if out := plain(rendered); !strings.Contains(squishSpaces(out), "tail-marker") {
		t.Errorf("expandAll should render the full thinking text:\n%s", out[:200])
	}

	// Collapsed again: the block hides entirely (calm default).
	m.expandAll = false
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	if out := plain(rendered); strings.Contains(out, "tail-marker") || strings.Contains(out, "head-marker") {
		t.Errorf("collapsed render should hide the block:\n%s", out[:200])
	}
}

// TestCtrlTThinkingFeedback verifies ^T acknowledges the toggle with a
// transient note and a persistent header indicator.
func TestCtrlTThinkingFeedback(t *testing.T) {
	m := newTestModel()
	m.Update(key("ctrl+t"))
	if !m.thinkOn {
		t.Fatal("^T did not enable thinking")
	}
	found := false
	for _, n := range m.notices {
		if strings.Contains(n, "thinking on") {
			found = true
		}
	}
	if !found {
		t.Errorf("^T posted no acknowledgement note: %v", m.notices)
	}
	if !strings.Contains(plain(m.header()), "✳ think") {
		t.Error("header shows no thinking indicator while enabled")
	}

	m.Update(key("ctrl+t"))
	if m.thinkOn {
		t.Fatal("second ^T did not disable thinking")
	}
	if strings.Contains(plain(m.header()), "✳ think") {
		t.Error("header thinking indicator should clear when disabled")
	}
}

// TestThinkingInterleavesWithTools verifies that reasoning blocks and tool
// steps render in chronological order — thinking before and after a tool call
// appears around it, not pinned above it — both while streaming and after the
// turn is finalized.
func TestThinkingInterleavesWithTools(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	m.handleEvent(client.Event{Type: "thinking", Content: "first"})
	m.handleEvent(client.Event{Type: "tool_call", Name: "read_file", Data: `{"path":"main.go"}`})
	m.handleEvent(client.Event{Type: "tool_result", Name: "read_file", Data: "package main\n"})
	m.handleEvent(client.Event{Type: "thinking", Content: "second thought"})

	assertOrder := func(out string) {
		t.Helper()
		firstIdx := strings.Index(out, "first")
		toolIdx := strings.Index(out, "read_file")
		secondIdx := strings.Index(out, "second thought")
		if firstIdx < 0 || toolIdx < 0 || secondIdx < 0 {
			t.Fatalf("missing timeline entries in:\n%s", out)
		}
		if firstIdx >= toolIdx || toolIdx >= secondIdx {
			t.Errorf("thinking should interleave around the tool step in:\n%s", out)
		}
	}

	// Details view: thinking interleaves around the tool step.
	m.expandAll = true
	rendered, _ := m.renderMessage(m.msgs[0], 0, 0)
	assertOrder(plain(rendered))

	// Finalized render keeps the same chronological order.
	m.handleEvent(client.Event{Type: "done", Latency: 0.5, ContextTokens: 10, OutputTokens: 1})
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	assertOrder(plain(rendered))
	m.expandAll = false
	if m.msgs[0].thinking != "first\nsecond thought" {
		t.Errorf("finalized thinking not concatenated: %q", m.msgs[0].thinking)
	}
}

// TestRenderMessageTimelineFallbacks covers the timeline edge branches: a
// hand-built message (no items) falls back to fixed thinking→steps order,
// blank thinking items are skipped, and out-of-range step references are
// ignored without dropping the valid ones.
func TestRenderMessageTimelineFallbacks(t *testing.T) {
	m := newTestModel()
	msg := message{role: roleAsst, thinking: "old thought", steps: []step{{name: "read", done: true}}}
	// Calm default: the fallback thinking item hides like every rail.
	out, _ := m.renderMessage(msg, 0, 0)
	if strings.Contains(plain(out), "old thought") {
		t.Errorf("fallback thinking must stay hidden by default:\n%s", plain(out))
	}
	m.expandAll = true
	out, _ = m.renderMessage(msg, 0, 0)
	m.expandAll = false
	if !strings.Contains(plain(out), "old thought") {
		t.Errorf("fallback thinking not rendered:\n%s", plain(out))
	}

	msg.items = []turnItem{{thinking: true, text: "   "}, {stepIdx: 7}, {stepIdx: -1}, {stepIdx: 0}}
	out, _ = m.renderMessage(msg, 0, 0)
	if !strings.Contains(plain(out), "read") {
		t.Errorf("valid step should still render:\n%s", plain(out))
	}
}

// TestThinkingCapturedOnFinalize verifies that reasoning is stored in the
// assistant message and still renders above the final response after the turn
// ends — hidden in the calm default, ordered under details.
func TestThinkingCapturedOnFinalize(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	m.handleEvent(client.Event{Type: "thinking", Content: "planning the response"})
	m.handleEvent(client.Event{Type: "token", Content: "hello"})
	m.handleEvent(client.Event{Type: "done", Latency: 0.5, ContextTokens: 10, OutputTokens: 1})

	if m.msgs[0].thinking != "planning the response" {
		t.Errorf("thinking not captured: %q", m.msgs[0].thinking)
	}
	rendered, _ := m.renderMessage(m.msgs[0], 0, 0)
	out := plain(rendered)
	if strings.Contains(out, "planning the response") {
		t.Errorf("finalized thinking must stay hidden by default:\n%s", out)
	}
	m.expandAll = true
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	m.expandAll = false
	out = plain(rendered)
	thinkIdx := strings.Index(out, "planning the response")
	respIdx := strings.Index(out, "hello")
	if thinkIdx < 0 || respIdx < 0 || thinkIdx > respIdx {
		t.Errorf("thinking should appear above response in the details view:\n%s", out)
	}
}
