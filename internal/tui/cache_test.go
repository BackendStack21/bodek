package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/charmbracelet/bubbles/spinner"
)

func TestSpinnerTickDoesNotRebuildPrefixDuringStream(t *testing.T) {
	m := newTestModel()
	streamingTurn(m)
	m.refresh()
	if m.convCount != 1 {
		t.Fatalf("precondition convCount=%d, want 1", m.convCount)
	}
	prefix := m.convPrefix
	m.handleEvent(client.Event{Type: "token", Content: "more"})
	m.Update(spinner.TickMsg{})
	if m.convPrefix != prefix {
		t.Error("spinner tick rebuilt the finalized prefix during streaming")
	}
}

func TestTailClockRefreshUpdatesLiveStep(t *testing.T) {
	m := newTestModel()
	streamingTurn(m)
	m.handleEvent(client.Event{Type: "tool_call", Name: "read_file", Data: `{"path":"x.go"}`})
	if i := m.cur(); i >= 0 && len(m.msgs[i].steps) > 0 {
		m.msgs[i].steps[0].started = time.Now().Add(-500 * time.Millisecond)
	}
	m.refresh()
	before := plain(m.vp.View())

	if i := m.cur(); i >= 0 && len(m.msgs[i].steps) > 0 {
		m.msgs[i].steps[0].started = time.Now().Add(-1500 * time.Millisecond)
	}
	m.tailClockPending = true
	m.tailClockSeq = 1
	m.Update(tailClockFlushMsg{seq: 1})
	after := plain(m.vp.View())
	if before == after {
		t.Errorf("tail clock flush should refresh live step duration:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestStepBlockCacheReusesFinishedStep(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: false, steps: []step{
		{name: "shell", arg: "go test ./...", done: true, result: strings.Repeat("line\n", 40)},
	}})
	out1, _, _ := m.renderStep(m.msgs[0].steps[0], false, 0, 0, 0)
	out2, _, _ := m.renderStep(m.msgs[0].steps[0], false, 0, 0, 0)
	if out1 != out2 {
		t.Error("finished step should reuse block cache")
	}
	if m.msgs[0].steps[0].blockCache == "" {
		t.Error("renderStep should populate block cache for finished steps")
	}
}

func TestHomeResumeKey(t *testing.T) {
	m := newTestModel()
	m.homePrompt = "cleared"
	m.homeSess = []client.Session{{ID: "sess-abc", Task: "fix login", Turns: 2, UpdatedAt: time.Now()}}
	if cmd := m.handleHomeResumeKey("1"); cmd == nil {
		t.Fatal("1 should resume the first recent session")
	}
	m.msgs = append(m.msgs, message{role: roleUser, content: "busy transcript"})
	if cmd := m.handleHomeResumeKey("1"); cmd != nil {
		t.Error("home resume must not fire while transcript has messages")
	}
}

func TestMsgBlockPartialInvalidate(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "q1"},
		message{role: roleAsst, content: "a1", rendered: m.render("a1"), steps: []step{
			{name: "read", done: true, result: "ok"},
		}},
		message{role: roleUser, content: "q2"},
		message{role: roleAsst, content: "a2", rendered: m.render("a2")},
	)
	m.refresh()
	block3 := m.msgBlocks[3].block
	m.toggleStep(1, 0)
	if len(m.msgBlocks) > 1 && m.msgBlocks[1].valid {
		t.Error("toggle should invalidate message 1 block")
	}
	if len(m.msgBlocks) < 4 || !m.msgBlocks[3].valid || m.msgBlocks[3].block != block3 {
		t.Error("sibling message block should stay cached")
	}
}
