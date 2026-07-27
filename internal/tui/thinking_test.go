package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestThinkingCap verifies that the live reasoning excerpt is capped so a long
// thinking stream cannot grow without bound.
func TestThinkingCap(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	// Stream a thinking chunk well over the cap.
	chunk := strings.Repeat("word ", 200)
	m.handleEvent(client.Event{Type: "thinking", Content: chunk})

	if m.thinking.Len() > maxThinkingLen*2 {
		t.Errorf("thinking excerpt grew too large: %d", m.thinking.Len())
	}

	// The visible excerpt should end with the tail of the latest input.
	out := m.thinking.String()
	if !strings.HasSuffix(out, "word ") {
		t.Errorf("thinking excerpt lost the tail: %q", out)
	}

	// A subsequent event should keep replacing/capping from the end.
	m.handleEvent(client.Event{Type: "thinking", Content: "final thought"})
	if !strings.Contains(m.thinking.String(), "final thought") {
		t.Errorf("latest thinking not retained: %q", m.thinking.String())
	}
}

// TestThinkingRendersAboveTools verifies that live reasoning appears at the top
// of the assistant body, before any tool summaries.
func TestThinkingRendersAboveTools(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	m.handleEvent(client.Event{Type: "thinking", Content: "I need to check the file."})
	m.handleEvent(client.Event{Type: "tool_call", Name: "read_file", Data: `{"path":"main.go"}`})
	m.handleEvent(client.Event{Type: "tool_result", Name: "read_file", Data: "package main\n"})

	rendered, _ := m.renderMessage(m.msgs[0], 0, 0)
	out := plain(rendered)
	thinkIdx := strings.Index(out, "I need to check the file")
	toolIdx := strings.Index(out, "read_file")
	if thinkIdx < 0 || toolIdx < 0 || thinkIdx > toolIdx {
		t.Errorf("thinking should appear before tools in:\n%s", out)
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
