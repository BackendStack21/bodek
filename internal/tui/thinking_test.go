package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestThinkingCap verifies that a long reasoning stream is stored in full but
// rendered as an excerpt capped at maxThinkingLen from the HEAD of the block,
// so it cannot push the transcript off-screen and orients the reader at the
// thought's beginning.
func TestThinkingCap(t *testing.T) {
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

	// The rendered excerpt is capped and shows the head, not the tail.
	rendered, _ := m.renderMessage(m.msgs[0], 0, 0)
	out := plain(rendered)
	if !strings.Contains(out, "head-marker") {
		t.Errorf("excerpt lost the head:\n%s", out)
	}
	if strings.Contains(out, "tail-marker") {
		t.Errorf("excerpt should be capped before the tail:\n%s", out)
	}

	// A subsequent event extends the same block.
	m.handleEvent(client.Event{Type: "thinking", Content: " final thought"})
	if len(m.msgs[0].items) != 1 {
		t.Fatalf("thinking delta opened a new block: %+v", m.msgs[0].items)
	}
	if !strings.HasSuffix(m.msgs[0].items[0].text, "final thought") {
		t.Errorf("latest thinking not retained: %q", m.msgs[0].items[0].text)
	}
}

// TestExpandAllFullThinking verifies ^E unfolds the complete thinking text
// past the excerpt cap once the turn is finalized, while a live stream keeps
// the bounded excerpt.
func TestExpandAllFullThinking(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	thought := "head-marker" + strings.Repeat(" filler", 100) + " tail-marker"
	m.handleEvent(client.Event{Type: "thinking", Content: thought})

	// Streaming + expandAll: still the capped excerpt, never the unbounded stream.
	m.expandAll = true
	rendered, _ := m.renderMessage(m.msgs[0], 0, 0)
	if out := plain(rendered); strings.Contains(out, "tail-marker") {
		t.Errorf("streaming render should keep the capped excerpt under expandAll:\n%s", out)
	}

	// Finalized + expandAll: the full text renders, wrapped in thinkStyle.
	m.handleEvent(client.Event{Type: "done", Latency: 0.5, ContextTokens: 10, OutputTokens: 1})
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	if out := plain(rendered); !strings.Contains(out, "head-marker") || !strings.Contains(out, "tail-marker") {
		t.Errorf("expandAll should render the full thinking text:\n%s", out)
	}

	// Collapsed again: back to the capped head excerpt.
	m.expandAll = false
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	if out := plain(rendered); !strings.Contains(out, "head-marker") || strings.Contains(out, "tail-marker") {
		t.Errorf("collapsed render should show the capped head excerpt:\n%s", out)
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

	// Streaming render.
	rendered, _ := m.renderMessage(m.msgs[0], 0, 0)
	assertOrder(plain(rendered))

	// Finalized render keeps the same chronological order.
	m.handleEvent(client.Event{Type: "done", Latency: 0.5, ContextTokens: 10, OutputTokens: 1})
	rendered, _ = m.renderMessage(m.msgs[0], 0, 0)
	assertOrder(plain(rendered))
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
	out, _ := m.renderMessage(msg, 0, 0)
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
// ends.
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
	thinkIdx := strings.Index(out, "planning the response")
	respIdx := strings.Index(out, "hello")
	if thinkIdx < 0 || respIdx < 0 || thinkIdx > respIdx {
		t.Errorf("thinking should appear above response in finalized message:\n%s", out)
	}
}
