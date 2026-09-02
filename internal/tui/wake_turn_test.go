package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// odek ≥ v1.40 wake-on-complete: the session frame arrives stamped
// system_initiated for a server-started turn. The TUI must open a streaming
// card from the wire — streaming events would otherwise find no open card
// and drop (cards only opened on the local send path before).
func TestWakeTurnOpensCardFromWire(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "session", SessionID: "s1", SystemInitiated: true})

	i := m.cur()
	if i < 0 {
		t.Fatal("wake session frame did not open a streaming card")
	}
	msg := m.msgs[i]
	if !msg.streaming {
		t.Error("wake card is not streaming")
	}
	if !msg.systemWake {
		t.Error("wake card not marked systemWake (marker would be lost)")
	}
	if msg.role != roleAsst {
		t.Errorf("wake card role = %v, want assistant (never renders as a user message)", msg.role)
	}
	if !m.busy {
		t.Error("wake turn did not set busy")
	}
	if !strings.Contains(m.status, "wak") {
		t.Errorf("status = %q, want a waking status", m.status)
	}
}

// Operator turns (session frame without system_initiated) keep the old
// behaviour: no card until the local send path opens one.
func TestOperatorSessionFrameOpensNothing(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "session", SessionID: "s1"})
	if m.cur() >= 0 {
		t.Error("operator session frame opened a card")
	}
	if m.busy {
		t.Error("operator session frame set busy")
	}
}

// A wake frame racing a live operator turn must not open a second card —
// odek wakes only idle connections, but the client-side guard keeps the
// transcript honest under any interleaving.
func TestWakeCardSuppressedWhileBusy(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "session", SessionID: "s1", SystemInitiated: true})
	if len(m.msgs) != 1 {
		t.Errorf("wake frame while busy opened a card: len(msgs) = %d", len(m.msgs))
	}
}

// Full wake lifecycle: the streamed output lands on the wake card (this is
// the defect being fixed — it used to drop), the turn finalizes, and the
// card renders the system-wake marker.
func TestWakeTurnLifecycleRendersMarker(t *testing.T) {
	m := newTestModel()
	feed := []client.Event{
		{Type: "session", SessionID: "s1", SystemInitiated: true},
		{Type: "thinking", Content: "checking job output"},
		{Type: "token", Content: "The background job finished: all tests green."},
		{Type: "done", OutputTokens: 42},
	}
	for _, ev := range feed {
		m.handleEvent(ev)
	}
	if m.cur() >= 0 {
		t.Error("turn still open after done")
	}
	if m.busy {
		t.Error("busy still set after done")
	}
	var found bool
	for _, msg := range m.msgs {
		if !msg.systemWake {
			continue
		}
		found = true
		if !strings.Contains(msg.content, "all tests green") {
			t.Errorf("wake card content = %q, want the streamed reply", msg.content)
		}
		if msg.stats == nil {
			t.Error("wake card missing finalized stats")
		}
		out, _ := m.renderMessage(msg, 0, 0)
		if !strings.Contains(strings.ToLower(out), "wake") {
			t.Errorf("wake card render missing the system-wake marker:\n%s", out)
		}
	}
	if !found {
		t.Fatal("no systemWake card in transcript")
	}
}

// The bg_wake frame is the operator's context for the unprompted activity:
// surface it as a transient note.
func TestBgWakeFrameNotifies(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "bg_wake", SessionID: "s1"})
	for _, n := range m.notices {
		if strings.Contains(n, "waking") {
			return
		}
	}
	t.Errorf("bg_wake frame produced no note; notices = %v", m.notices)
}

// A bg_job push frame refreshes the jobs snapshot immediately; a surface
// marked unavailable (< v1.38 sentinel) is never polled. The fetch is
// exercised against the test server so the kick is proven end to end.
func TestBgJobFrameKicksJobsFetch(t *testing.T) {
	m, seen := jobsMux(t, `{"jobs":[]}`, nil)
	m.applyJobs(jobsFixture(), nil) // watcher live

	cmd := m.kickJobsFetch()
	if cmd == nil {
		t.Fatal("live session: bg_job kick returned no fetch cmd")
	}
	m.Update(exec(cmd)) // jobsFetchedMsg → snapshot + rearm
	if len(*seen) == 0 {
		t.Error("kick fetch never reached the server")
	}

	m.jobsOff = true
	if cmd := m.kickJobsFetch(); cmd != nil {
		t.Error("unavailable surface: bg_job kick must not poll")
	}
}

// handleEvent routes bg_job frames through the kick.
func TestBgJobFrameRoutesThroughKick(t *testing.T) {
	m := newJobsTestModel(t, nil)
	m.applyJobs(jobsFixture(), nil)
	if _, cmd := m.handleEvent(client.Event{Type: "bg_job", SessionID: "s1"}); cmd == nil {
		t.Error("bg_job frame produced no cmd")
	}
}

// Plain mode announces the wake as a line; operator session frames print
// nothing.
func TestWakeTurnPlainModeAnnounces(t *testing.T) {
	m := newTestModel()
	m.plain = true
	lines := m.plainEventLines(client.Event{Type: "session", SessionID: "s1", SystemInitiated: true})
	if !strings.Contains(strings.ToLower(strings.Join(lines, "\n")), "wake") {
		t.Errorf("plain mode printed no wake line: %v", lines)
	}
	if lines := m.plainEventLines(client.Event{Type: "session", SessionID: "s1"}); lines != nil {
		t.Errorf("operator session frame printed in plain mode: %v", lines)
	}
}
