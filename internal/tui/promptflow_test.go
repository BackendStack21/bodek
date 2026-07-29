package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// busyTurn puts the model mid-turn with an open streaming assistant message.
func busyTurn(m *Model) {
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "first"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true
}

// TestJumpToLatest verifies G/End jump to the bottom with an empty input and
// never hijack typing.
func TestJumpToLatest(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()
	tallTranscript(m)
	m.vp.GotoTop()
	if m.vp.AtBottom() {
		t.Fatal("precondition: scrolled away from the bottom")
	}

	// Footer advertises the jump while off-bottom.
	if foot := plain(m.footer()); !strings.Contains(foot, "G") || !strings.Contains(foot, "latest") {
		t.Errorf("footer missing jump hint: %q", foot)
	}

	m.Update(key("G"))
	if !m.vp.AtBottom() {
		t.Error("G should jump to the latest output")
	}

	// End behaves the same (real terminals send KeyEnd, not runes).
	m.vp.GotoTop()
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !m.vp.AtBottom() {
		t.Error("End should jump to the latest output")
	}

	// With a draft, G types instead of jumping.
	m.vp.GotoTop()
	m.ta.SetValue("draft")
	m.Update(key("G"))
	if m.vp.AtBottom() {
		t.Error("G with a draft must not jump")
	}
	if m.ta.Value() != "draftG" {
		t.Errorf("G with a draft should type, got %q", m.ta.Value())
	}
}

// TestNewOutputIndicator verifies the footer calls out fresh output while
// scrolled up mid-run.
func TestNewOutputIndicator(t *testing.T) {
	m := newTestModel()
	tallTranscript(m)
	m.vp.GotoTop()
	m.busy = true

	if foot := plain(m.footer()); !strings.Contains(foot, "new output") {
		t.Errorf("busy off-bottom footer missing new-output indicator: %q", foot)
	}
}

// TestDisconnectedRetry verifies the dead-connection state offers a manual
// redial on r, and that typing r into a draft is never hijacked.
func TestDisconnectedRetry(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()
	m.opts.Reconnect = func() (*client.Client, error) { return nil, errors.New("down") }
	m.disconn = true
	m.status = "disconnected"

	if foot := plain(m.footer()); !strings.Contains(foot, "r") || !strings.Contains(foot, "retry") {
		t.Errorf("disconnected footer missing retry hint: %q", foot)
	}

	_, cmd := m.Update(key("r"))
	if cmd == nil {
		t.Fatal("r while disconnected should schedule a redial")
	}
	if m.status != "reconnecting" {
		t.Errorf("status = %q, want reconnecting", m.status)
	}

	// A non-empty draft keeps r as plain typing.
	m.status = "disconnected"
	m.ta.SetValue("draft")
	m.Update(key("r"))
	if m.ta.Value() != "draftr" {
		t.Errorf("r with a draft should type, got %q", m.ta.Value())
	}
}

// TestReconnectDrainsQueue verifies a successful redial flushes prompts
// queued while the socket was down.
func TestReconnectDrainsQueue(t *testing.T) {
	m := wired(t) // live stand-in: m.cl is a real connected client
	m.disconn = true
	m.queue = []string{"held"}

	_, cmd := m.handleReconnect(reconnectMsg{attempt: 0, cl: m.cl})
	if m.disconn {
		t.Fatal("reconnect with a client should clear the disconnected state")
	}
	if len(m.queue) != 0 {
		t.Errorf("queue should drain on reconnect, got %v", m.queue)
	}
	if !m.busy {
		t.Error("drained prompt should start a new turn")
	}
	if cmd == nil {
		t.Error("reconnect should return the listen/send batch")
	}
}

// TestSubmitWhileBusyQueues verifies that ⏎ mid-turn queues the prompt
// instead of silently dropping it, and that the footer says so.
func TestSubmitWhileBusyQueues(t *testing.T) {
	m := newTestModel()
	busyTurn(m)

	m.ta.SetValue("follow up")
	if cmd := m.submit(); cmd != nil {
		t.Error("queueing a prompt should not send anything yet")
	}
	if len(m.queue) != 1 || m.queue[0] != "follow up" {
		t.Fatalf("queue = %v, want [follow up]", m.queue)
	}
	if m.ta.Value() != "" {
		t.Error("input should reset after queueing")
	}
	if len(m.msgs) != 2 {
		t.Error("queued prompt must not enter the transcript before it is sent")
	}
	if foot := plain(m.footer()); !strings.Contains(foot, "1 queued") {
		t.Errorf("footer missing queued indicator: %q", foot)
	}
}

// TestQueuedPromptSendsOnDone verifies the queue drains automatically when
// the running turn ends.
func TestQueuedPromptSendsOnDone(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.ta.SetValue("follow up")
	m.submit()

	_, cmd := m.handleEvent(client.Event{Type: "done", Latency: 1})
	if cmd == nil {
		t.Fatal("done should drain the queued prompt")
	}
	// The returned cmd wraps SendPrompt; newTestModel has no client, so it is
	// deliberately not executed — state assertions suffice.
	if len(m.queue) != 0 {
		t.Errorf("queue not drained: %v", m.queue)
	}
	if !m.busy {
		t.Error("model should be busy again with the queued turn")
	}
	if len(m.msgs) != 4 || m.msgs[2].content != "follow up" {
		t.Fatalf("queued turn not appended: %+v", m.msgs)
	}
}

// tallTranscript loads a scrollable assistant message into the transcript.
// (A markdown list survives glamour as one rendered line per item; a plain
// "x\n" repeat would collapse into a single wrapped paragraph.)
func tallTranscript(m *Model) {
	md := strings.Repeat("- item\n", 60)
	m.msgs = append(m.msgs, message{role: roleAsst, content: md, rendered: m.render(md)})
	m.refresh()
	if m.vp.TotalLineCount() <= m.vp.Height {
		panic("tallTranscript: content should exceed the viewport")
	}
}

// TestHistoryRecall verifies ↑/↓ walks submitted prompts and restores the
// stashed draft past the newest entry.
func TestHistoryRecall(t *testing.T) {
	m := newTestModel()
	m.sendPrompt("first")
	m.handleEvent(client.Event{Type: "done", Latency: 1})
	m.sendPrompt("second")
	m.handleEvent(client.Event{Type: "done", Latency: 1})
	m.sendPrompt("second") // consecutive dup — must not double-record
	if len(m.history) != 2 {
		t.Fatalf("history = %v, want [first second]", m.history)
	}

	m.Update(key("up"))
	if got := m.ta.Value(); got != "second" {
		t.Errorf("first up = %q, want %q", got, "second")
	}
	m.Update(key("up"))
	if got := m.ta.Value(); got != "first" {
		t.Errorf("second up = %q, want %q", got, "first")
	}
	m.Update(key("up")) // at the oldest entry: consumed, no movement
	if got := m.ta.Value(); got != "first" {
		t.Errorf("up past oldest = %q, want %q", got, "first")
	}
	m.Update(key("down"))
	if got := m.ta.Value(); got != "second" {
		t.Errorf("down = %q, want %q", got, "second")
	}
	m.Update(key("down")) // past newest: restore the (empty) draft
	if got := m.ta.Value(); got != "" {
		t.Errorf("down past newest = %q, want empty draft", got)
	}
	if m.histNav {
		t.Error("history navigation should end past the newest entry")
	}
}

// TestHistoryScrollFallback verifies ↑ still scrolls the transcript when
// there is no history to recall (empty input, tall transcript).
func TestHistoryScrollFallback(t *testing.T) {
	m := newTestModel()
	tallTranscript(m)
	bottom := m.vp.YOffset

	m.Update(key("up"))
	if m.vp.YOffset >= bottom {
		t.Error("up with empty history should scroll the transcript")
	}
}

// TestHistoryEdgeCases covers the history ring cap, the no-navigation guard,
// and cancelRun's draft-prepend branch.
func TestHistoryEdgeCases(t *testing.T) {
	m := newTestModel()
	for i := 0; i < maxHistory+10; i++ {
		m.recordHistory("prompt")
		m.recordHistory("unique")
	}
	if len(m.history) != maxHistory {
		t.Errorf("history should cap at %d, got %d", maxHistory, len(m.history))
	}

	// historyNext outside navigation is a safe no-op.
	m.ta.SetValue("keep")
	m.historyNext()
	if m.ta.Value() != "keep" {
		t.Error("historyNext without navigation must not touch the input")
	}

	// Cancel with both a draft and a queue prepends the draft.
	busyTurn(m)
	m.sessionID = "s1"
	m.ta.SetValue("draft")
	m.queue = []string{"held"}
	m.cancelRun()
	if got := m.ta.Value(); got != "draft\nheld" {
		t.Errorf("cancel restore = %q, want draft prepended to queue", got)
	}
}

// TestCancelRestoresQueue verifies that esc hands queued prompts back to the
// input instead of firing them into a cancelled session.
func TestCancelRestoresQueue(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.sessionID = "s1"
	m.queue = []string{"one", "two"}

	m.Update(key("esc"))
	if len(m.queue) != 0 {
		t.Errorf("queue should be handed back, got %v", m.queue)
	}
	if got := m.ta.Value(); got != "one\ntwo" {
		t.Errorf("input = %q, want queued drafts restored", got)
	}

	// The done that follows the cancel must not fire anything.
	m.handleEvent(client.Event{Type: "done", Latency: 1})
	if len(m.msgs) != 2 {
		t.Error("no turn should start after cancel restored the queue")
	}
}
