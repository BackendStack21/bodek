package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestThinkingCap verifies that each reasoning block is capped so a long
// thinking stream cannot grow without bound.
func TestThinkingCap(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	// Stream a thinking chunk well over the cap.
	chunk := strings.Repeat("word ", 200)
	m.handleEvent(client.Event{Type: "thinking", Content: chunk})

	if len(m.msgs[0].items) != 1 || !m.msgs[0].items[0].thinking {
		t.Fatalf("expected one thinking item, got %+v", m.msgs[0].items)
	}
	block := m.msgs[0].items[0].text
	if len(block) > maxThinkingLen*2 {
		t.Errorf("thinking block grew too large: %d", len(block))
	}

	// The visible excerpt should end with the tail of the latest input.
	if !strings.HasSuffix(block, "word ") {
		t.Errorf("thinking block lost the tail: %q", block)
	}

	// A subsequent event extends the same block, capping from the end again.
	m.handleEvent(client.Event{Type: "thinking", Content: "final thought"})
	if len(m.msgs[0].items) != 1 {
		t.Fatalf("thinking delta opened a new block: %+v", m.msgs[0].items)
	}
	if !strings.Contains(m.msgs[0].items[0].text, "final thought") {
		t.Errorf("latest thinking not retained: %q", m.msgs[0].items[0].text)
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
