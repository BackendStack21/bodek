package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

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

// TestJumpToLatest verifies ^G/End jump to the bottom and that a capital G
// always types — even as the first character of a prompt.
func TestJumpToLatest(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()
	tallTranscript(m)
	m.vp.GotoTop()
	if m.vp.AtBottom() {
		t.Fatal("precondition: scrolled away from the bottom")
	}

	// Footer advertises the jump while off-bottom.
	if foot := plain(m.footer()); !strings.Contains(foot, "^G") || !strings.Contains(foot, "latest") {
		t.Errorf("footer missing jump hint: %q", foot)
	}

	// A capital G types into the empty input instead of jumping.
	m.Update(key("G"))
	if m.vp.AtBottom() {
		t.Error("G with an empty input must not jump")
	}
	if m.ta.Value() != "G" {
		t.Errorf("G with an empty input should type, got %q", m.ta.Value())
	}

	// ^G jumps even with a draft (no textarea conflict).
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if !m.vp.AtBottom() {
		t.Error("^G should jump to the latest output")
	}
	if m.ta.Value() != "G" {
		t.Errorf("^G must not disturb the draft, got %q", m.ta.Value())
	}

	// End jumps only with an empty input (real terminals send KeyEnd, not runes).
	m.ta.SetValue("")
	m.vp.GotoTop()
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !m.vp.AtBottom() {
		t.Error("End should jump to the latest output")
	}

	// With a draft, End keeps its cursor-movement meaning.
	m.vp.GotoTop()
	m.ta.SetValue("draft")
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if m.vp.AtBottom() {
		t.Error("End with a draft must not jump")
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
// redial on ⏎ with an empty input, and that every character — including the
// letters that once carried actions — keeps typing while disconnected.
func TestDisconnectedRetry(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()
	m.opts.Reconnect = func() (*client.Client, error) { return nil, errors.New("down") }
	m.disconn = true
	m.status = "disconnected"

	if foot := plain(m.footer()); !strings.Contains(foot, "⏎") || !strings.Contains(foot, "retry") {
		t.Errorf("disconnected footer missing retry hint: %q", foot)
	}

	// ⏎ on an empty input schedules the redial.
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter while disconnected with empty input should schedule a redial")
	}
	if m.status != "reconnecting…" {
		t.Errorf("status = %q, want reconnecting…", m.status)
	}

	// r types like any other character while disconnected.
	m.status = "disconnected"
	m.ta.Reset()
	for _, r := range "reboot" {
		m.Update(key(string(r)))
	}
	if m.ta.Value() != "reboot" {
		t.Errorf("r must type while disconnected, got %q", m.ta.Value())
	}
	// ⏎ with a draft is submit (kept-draft warning), not a redial.
	m.Update(key("enter"))
	if m.status == "reconnecting" {
		t.Error("enter with a draft triggered a redial instead of submit")
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
	// Queueing returns the acknowledgment note's sweep cmd — but nothing is
	// dispatched: no prompt send, no transcript pair.
	if cmd := m.submit(); cmd == nil {
		t.Error("queueing should acknowledge with the note-sweep cmd")
	}
	if m.lastPrompt == "follow up" {
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

// TestSubmitJumpsToBottom verifies ⏎ always returns the viewport to the
// latest output — both for a fresh prompt and when queueing mid-turn — even
// when the reader was up in the scrollback.
func TestSubmitJumpsToBottom(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()
	tallTranscript(m)

	// Queueing mid-turn: busy model, scrolled up, ⏎.
	busyTurn(m)
	m.vp.GotoTop()
	m.ta.SetValue("follow up")
	m.submit()
	if !m.vp.AtBottom() {
		t.Error("queueing a prompt should jump to the latest output")
	}

	// Fresh submit once the turn ended (the returned send cmd is deliberately
	// not executed — newTestModel has no client).
	m.busy = false
	m.vp.GotoTop()
	m.ta.SetValue("next prompt")
	m.submit()
	if !m.vp.AtBottom() {
		t.Error("submitting a prompt should jump to the latest output")
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

// TestHistoryRecall verifies ^P/^N walks submitted prompts and restores the
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

	m.Update(key("ctrl+p"))
	if got := m.ta.Value(); got != "second" {
		t.Errorf("first ^P = %q, want %q", got, "second")
	}
	m.Update(key("ctrl+p"))
	if got := m.ta.Value(); got != "first" {
		t.Errorf("second ^P = %q, want %q", got, "first")
	}
	m.Update(key("ctrl+p")) // at the oldest entry: consumed, no movement
	if got := m.ta.Value(); got != "first" {
		t.Errorf("^P past oldest = %q, want %q", got, "first")
	}
	m.Update(key("ctrl+n"))
	if got := m.ta.Value(); got != "second" {
		t.Errorf("^N = %q, want %q", got, "second")
	}
	m.Update(key("ctrl+n")) // past newest: restore the (empty) draft
	if got := m.ta.Value(); got != "" {
		t.Errorf("^N past newest = %q, want empty draft", got)
	}
	if m.histNav {
		t.Error("history navigation should end past the newest entry")
	}
}

// TestHistoryScrollFallback verifies ↑ scrolls the transcript with an empty
// input (no history recorded yet, tall transcript).
func TestHistoryScrollFallback(t *testing.T) {
	m := newTestModel()
	tallTranscript(m)
	bottom := m.vp.YOffset

	m.Update(key("up"))
	if m.vp.YOffset >= bottom {
		t.Error("up with empty history should scroll the transcript")
	}
}

// TestUpScrollsEvenWithHistory is the regression test for the scroll-vs-recall
// conflict: bare ↑/↓ must scroll the transcript even when prompt history
// exists — recall moved to the dedicated ^P/^N binding.
func TestUpScrollsEvenWithHistory(t *testing.T) {
	m := newTestModel()
	m.recordHistory("earlier prompt")
	tallTranscript(m)
	bottom := m.vp.YOffset

	m.Update(key("up"))
	if m.vp.YOffset >= bottom {
		t.Error("up should scroll the transcript even with prompt history present")
	}
	if m.histNav || m.ta.Value() != "" {
		t.Errorf("up must not touch prompt history: histNav=%v input=%q", m.histNav, m.ta.Value())
	}

	// ^P still recalls.
	m.Update(key("ctrl+p"))
	if got := m.ta.Value(); got != "earlier prompt" {
		t.Errorf("^P = %q, want %q", got, "earlier prompt")
	}
}

// TestHistoryEdgeCases covers the history ring cap, the no-navigation guard,
// and cancelRun's draft-prepend branch.
func TestHistoryEdgeCases(t *testing.T) {
	m := newTestModel()
	if m.historyPrev() {
		t.Error("historyPrev with empty history should return false")
	}
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

// TestCancelRestoresQueue verifies that a confirmed esc hands queued prompts
// back to the input instead of firing them into a cancelled session. The
// gate must arm without touching the queue — only y fires the cancel.
func TestCancelRestoresQueue(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.sessionID = "s1"
	m.queue = []string{"one", "two"}

	// Arming the gate is not a cancel: the queue stays untouched.
	m.Update(key("esc"))
	if m.confirm != confirmCancel {
		t.Fatalf("esc did not arm confirmCancel: %v", m.confirm)
	}
	if len(m.queue) != 2 {
		t.Errorf("arming the gate disturbed the queue: %v", m.queue)
	}

	m.Update(key("y"))
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

// Enter while cancelling must not re-queue the restored draft — that
// would sendQueued it into the session the cancel just pulled it out of.
func TestCancelEnterDoesNotRequeue(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.sessionID = "s1"
	m.queue = []string{"one", "two"}

	m.Update(key("esc"))
	m.Update(key("y"))
	if m.status != "cancelling" {
		t.Fatalf("precondition: status = %q, want cancelling", m.status)
	}
	if got := m.ta.Value(); got != "one\ntwo" {
		t.Fatalf("precondition: input = %q", got)
	}

	m.Update(key("enter"))
	if len(m.queue) != 0 {
		t.Fatalf("enter while cancelling re-queued: %v", m.queue)
	}
	if got := m.ta.Value(); got != "one\ntwo" {
		t.Errorf("draft must stay in the input, got %q", got)
	}

	m.handleEvent(client.Event{Type: "done", Latency: 1})
	if len(m.msgs) != 2 {
		t.Error("no turn should start after enter-during-cancel")
	}
}

// TestSendFailureFinalizesTurn verifies a failed send closes out the phantom
// assistant turn sendPrompt opened, with the error inline in the transcript.
func TestSendFailureFinalizesTurn(t *testing.T) {
	m := newTestModel()
	busyTurn(m)

	m.Update(errMsg{err: errors.New("write broke")})
	if m.busy {
		t.Error("errMsg should clear busy")
	}
	if m.curIdx != -1 {
		t.Error("errMsg should finalize the in-flight turn")
	}
	msg := m.msgs[1]
	if msg.streaming {
		t.Error("assistant message should no longer be streaming")
	}
	if !strings.Contains(msg.content, "**Error:**") || !strings.Contains(msg.content, "write broke") {
		t.Errorf("inline error missing from the turn: %q", msg.content)
	}
	if out := plain(m.conversation()); !strings.Contains(out, "Error:") {
		t.Errorf("inline error not rendered in the transcript:\n%s", out)
	}
}

// TestDisconnectedFooterHidesRetryWithDraft verifies the r retry hint only
// shows when it actually works — with an empty input.
func TestDisconnectedFooterHidesRetryWithDraft(t *testing.T) {
	m := newTestModel()
	m.opts.Reconnect = func() (*client.Client, error) { return nil, errors.New("down") }
	m.disconn = true

	m.ta.SetValue("draft")
	if foot := plain(m.footer()); strings.Contains(foot, "retry") {
		t.Errorf("footer offers r retry with a draft present: %q", foot)
	}
	m.ta.SetValue("")
	if foot := plain(m.footer()); !strings.Contains(foot, "retry") {
		t.Errorf("footer missing r retry with an empty input: %q", foot)
	}
}

// TestDisconnectedSubmitWarningFades verifies the submit-while-disconnected
// warning is alert-tier (it dwells well past a glance away, but still
// autocloses like everything in the strip), keeps the draft, and does not
// stack a duplicate on every enter.
func TestDisconnectedSubmitWarningFades(t *testing.T) {
	m := newTestModel()
	m.disconn = true
	m.ta.SetValue("hello")

	m.submit()
	if len(m.notices) == 0 {
		t.Fatal("no warning posted")
	}
	last := len(m.notices) - 1
	if !strings.Contains(m.notices[last], "draft is kept") {
		t.Errorf("warning text = %q", m.notices[last])
	}
	if m.noticeExp[last].IsZero() {
		t.Error("disconnect warning should carry an expiry (no sticky notes)")
	}
	if m.ta.Value() != "hello" {
		t.Errorf("draft should be kept, got %q", m.ta.Value())
	}

	m.submit() // draft still there — must not stack a duplicate
	if len(m.notices) != last+1 {
		t.Errorf("duplicate warning posted: %v", m.notices)
	}

	m.pruneNotices(time.Now().Add(alertTTL + time.Second))
	if len(m.notices) != 0 {
		t.Errorf("warning survived alertTTL: %v", m.notices)
	}
}

// TestCancelFeedback verifies the cancel path acknowledges itself: a note
// when there is nothing to cancel, one when queued prompts return to the
// input, and one when the abort lands.
func TestCancelFeedback(t *testing.T) {
	m := newTestModel()
	hasNote := func(sub string) bool {
		for _, n := range m.notices {
			if strings.Contains(n, sub) {
				return true
			}
		}
		return false
	}

	// Idle: nothing to cancel.
	m.cancelRun()
	if !hasNote("nothing to cancel") {
		t.Errorf("idle cancel posted no note: %v", m.notices)
	}

	// Busy with a queue: the draft restore is announced.
	busyTurn(m)
	m.sessionID = "s1"
	m.queue = []string{"held"}
	m.cancelRun()
	if !hasNote("returned to the input") {
		t.Errorf("queue restore posted no note: %v", m.notices)
	}

	// A successful abort acknowledges itself (the failure path already notes).
	m.Update(cancelDoneMsg{})
	if !hasNote("cancelled") {
		t.Errorf("successful cancel posted no note: %v", m.notices)
	}
}
