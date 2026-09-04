package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// hints_test.go — the just-in-time teaching layer (F1): state-triggered tips
// that fire exactly once per process, plus the step-expansion behaviors (F2)
// they teach: error auto-expand and tab's last-step fallback.

func countNotices(m *Model, marker string) int {
	n := 0
	for _, s := range m.notices {
		if strings.Contains(s, marker) {
			n++
		}
	}
	return n
}

// isHintNote reports whether a strip note is a JIT hint (hints.go) rather
// than an operational note — tests that demand silence for a state use it
// to ignore the teaching layer.
func isHintNote(s string) bool {
	return strings.HasPrefix(s, "💡")
}

// liveTurnModel mirrors sendPrompt's local state without touching a client.
func liveTurnModel() *Model {
	m := newTestModel()
	m.busy = true
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "do the thing"},
		message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	return m
}

func runMiniTurn(t *testing.T, m *Model, tool, result string) {
	t.Helper()
	m.handleEvent(client.Event{Type: "tool_call", Name: tool, Data: "arg"})
	m.handleEvent(client.Event{Type: "tool_result", Name: tool, Data: result})
}

func TestQueueHintFiresOnce(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m.ta.SetValue("first")
	m.submit()
	if countNotices(m, "tip: ^Q manages the queue") != 1 {
		t.Fatalf("first queued prompt must teach ^Q once, notices: %q", m.notices)
	}
	m.ta.SetValue("second")
	m.submit()
	if countNotices(m, "tip: ^Q manages the queue") != 1 {
		t.Fatalf("hint repeated on the second queue: %q", m.notices)
	}
	if len(m.queue) != 2 {
		t.Fatalf("queue must still hold both prompts, got %d", len(m.queue))
	}
}

func TestSwarmHintFiresOnce(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "active", Status: "running", Tool: "read main.go"})
	if countNotices(m, "tip: tab cycles sub-agent") != 1 {
		t.Fatalf("first swarm frame must teach tab once, notices: %q", m.notices)
	}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "active", Status: "running", Tool: "go test"})
	if countNotices(m, "tip: tab cycles sub-agent") != 1 {
		t.Fatalf("hint repeated on the second frame: %q", m.notices)
	}
}

func TestStepsHintFiresOnce(t *testing.T) {
	m := liveTurnModel()
	runMiniTurn(t, m, "read_file", "contents")
	m.handleEvent(client.Event{Type: "done"})
	if countNotices(m, "tip: click any step") != 1 {
		t.Fatalf("first stepped turn must teach expansion once, notices: %q", m.notices)
	}
	// A second stepped turn must not repeat the tip.
	m.busy = true
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "again"},
		message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	runMiniTurn(t, m, "read_file", "more")
	m.handleEvent(client.Event{Type: "done"})
	if countNotices(m, "tip: click any step") != 1 {
		t.Fatalf("hint repeated on the second stepped turn: %q", m.notices)
	}
}

func TestErrorResultAutoExpands(t *testing.T) {
	m := liveTurnModel()
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: "go test ./..."})
	m.handleEvent(client.Event{Type: "tool_result", Name: "shell", Data: "exit status 1\nFAIL\t./..."})
	i := m.cur()
	s := m.msgs[i].steps
	if len(s) != 1 || !s[0].isErr {
		t.Fatalf("precondition: failing step classified, got %+v", s)
	}
	if !s[0].expanded {
		t.Fatal("a failing step must auto-expand its output")
	}
	// A successful step never unfolds on its own.
	m.handleEvent(client.Event{Type: "tool_call", Name: "read_file", Data: "main.go"})
	m.handleEvent(client.Event{Type: "tool_result", Name: "read_file", Data: "package main"})
	if s2 := m.msgs[i].steps; len(s2) != 2 || s2[1].expanded {
		t.Fatalf("successful step must stay collapsed, got %+v", s2)
	}
}

func TestTabFallsBackToLastStep(t *testing.T) {
	m := liveTurnModel()
	runMiniTurn(t, m, "read_file", "one")
	runMiniTurn(t, m, "shell", "two")
	m.handleEvent(client.Event{Type: "done"})
	last := len(m.msgs) - 1

	m.Update(key("tab"))
	if !m.msgs[last].steps[1].expanded {
		t.Fatal("tab with no swarm/thinking must toggle the latest step")
	}
	m.Update(key("tab"))
	if m.msgs[last].steps[1].expanded {
		t.Fatal("second tab must collapse the latest step again")
	}
	// Reasoning blocks keep priority: with a thinking item present, tab
	// opens it and leaves the step alone.
	ti := len(m.msgs[last].items)
	m.msgs[last].items = append(m.msgs[last].items, turnItem{thinking: true, text: "deliberation"})
	m.Update(key("tab"))
	if !m.msgs[last].items[ti].open {
		t.Fatal("tab must still toggle the reasoning block when one exists")
	}
	if m.msgs[last].steps[1].expanded {
		t.Fatal("tab must not touch the step while a reasoning block wins")
	}
}
