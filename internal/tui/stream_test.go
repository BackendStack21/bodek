package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// streamingTurn puts m mid-turn with one streaming assistant message.
func streamingTurn(m *Model) {
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "q"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = len(m.msgs) - 1
	m.busy = true
	m.runStart = time.Now()
}

// flush delivers the pending coalesced-render tick, as the event loop would.
func flush(t *testing.T, m *Model) {
	t.Helper()
	if !m.renderPending {
		t.Fatal("flush: no coalesced render pending")
	}
	m.Update(renderFlushMsg{seq: m.renderSeq})
	if m.renderPending {
		t.Fatal("flush: render still pending after flush")
	}
}

// TestStreamingRenderCoalesced: token/thinking events must not rebuild the
// viewport per event (the streaming tail is glamour re-rendered on every
// rebuild); they queue one flush per streamRenderInterval instead.
func TestStreamingRenderCoalesced(t *testing.T) {
	m := newTestModel()
	streamingTurn(m)
	m.refresh() // baseline viewport: turn visible, no tokens yet
	base := m.vp.View()

	m.handleEvent(client.Event{Type: "token", Content: "hello"})
	if !m.renderPending {
		t.Fatal("token event should queue a coalesced render")
	}
	if vp := m.vp.View(); vp != base {
		t.Error("token event re-rendered eagerly instead of coalescing")
	}

	// More tokens while pending coalesce into the same flush.
	m.handleEvent(client.Event{Type: "token", Content: " world"})
	m.handleEvent(client.Event{Type: "thinking", Content: "hmm"})
	seq := m.renderSeq

	// A stale flush (superseded sequence) is ignored.
	stale := m.renderPending
	m.Update(renderFlushMsg{seq: seq + 1})
	if m.renderPending != stale {
		t.Error("stale flush changed render state")
	}

	flush(t, m)
	if vp := plain(m.vp.View()); !strings.Contains(vp, "hello world") {
		t.Error("flushed render missing streamed tokens")
	}
}

// TestQueueRenderFlushCarriesSeq: the scheduled flush fires with the sequence
// it was scheduled under, and a second schedule while pending is a no-op.
func TestQueueRenderFlushCarriesSeq(t *testing.T) {
	m := newTestModel()
	cmd := m.queueRender()
	if cmd == nil {
		t.Fatal("first queueRender should schedule a flush")
	}
	if again := m.queueRender(); again != nil {
		t.Error("queueRender while pending must not reschedule")
	}
	fm, ok := cmd().(renderFlushMsg) // blocks for streamRenderInterval
	if !ok || fm.seq != m.renderSeq {
		t.Errorf("flush = %#v, want renderFlushMsg{seq: %d}", fm, m.renderSeq)
	}
}

// TestNonStreamingEventsRenderImmediately: low-frequency events (tool calls,
// done, errors) still refresh eagerly — only the token/thinking firehose is
// coalesced.
func TestNonStreamingEventsRenderImmediately(t *testing.T) {
	m := newTestModel()
	streamingTurn(m)
	m.refresh()

	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"ls"}`})
	if m.renderPending {
		t.Error("tool_call should render immediately, not queue a flush")
	}
	if vp := plain(m.vp.View()); !strings.Contains(vp, "shell") {
		t.Error("tool_call not visible without a flush")
	}

	// The terminal event of a turn must never wait on a flush either.
	m.handleEvent(client.Event{Type: "token", Content: "x"})
	m.handleEvent(client.Event{Type: "done"})
	if m.renderPending {
		t.Error("done left a coalesced render pending")
	}
}
