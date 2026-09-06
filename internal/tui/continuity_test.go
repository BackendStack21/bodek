package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestCancelRestoresPromptToComposer(t *testing.T) {
	m := newTestModel()
	m.lastPrompt = "fix the flaky test"
	m.ta.SetValue("")
	m.handleEvent(client.Event{Type: "error", Message: "context canceled"})
	if m.ta.Value() != "fix the flaky test" {
		t.Fatalf("composer = %q, want the cancelled prompt", m.ta.Value())
	}
}

func TestFailedTurnRestoresPromptToComposer(t *testing.T) {
	m := newTestModel()
	m.lastPrompt = "explain client.go"
	m.ta.SetValue("")
	m.handleEvent(client.Event{Type: "error", Message: "llm: stream idle timeout"})
	if m.ta.Value() != "explain client.go" {
		t.Fatalf("composer = %q, want the failed prompt", m.ta.Value())
	}
}

func TestRestorePromptDoesNotClobberDraft(t *testing.T) {
	m := newTestModel()
	m.lastPrompt = "old"
	m.ta.SetValue("already typing")
	m.restoreComposerPrompt()
	if m.ta.Value() != "already typing" {
		t.Fatalf("draft was clobbered: %q", m.ta.Value())
	}
}

func TestFindExpandsHiddenStepHit(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{
		role:    roleAsst,
		content: "done",
		steps: []step{{
			name: "shell", arg: "go test", result: "unique-needle-xyz failed",
		}},
	})
	m.refresh()
	m.Update(key("alt+f"))
	for _, r := range "unique-needle-xyz" {
		m.Update(key(string(r)))
	}
	m.Update(key("enter"))
	if !m.msgs[0].steps[0].expanded {
		t.Fatal("find must expand the step that holds the hit")
	}
}

func TestFindOpensHiddenReasoningHit(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{
		role:    roleAsst,
		content: "ok",
		items: []turnItem{{
			thinking: true, text: "I should inspect unique-think-abc",
		}},
	})
	m.refresh()
	m.Update(key("alt+f"))
	for _, r := range "unique-think-abc" {
		m.Update(key(string(r)))
	}
	m.Update(key("enter"))
	if !m.msgs[0].items[0].open {
		t.Fatal("find must open the reasoning block that holds the hit")
	}
}

func TestFocusedCopyPrefersExpandedStep(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{
		role:    roleAsst,
		content: "the reply",
		steps: []step{{
			name: "shell", result: "step body", expanded: true,
		}},
	})
	m.focusIdx = 0
	if got := m.focusedCopyText(); got != "step body" {
		t.Fatalf("focusedCopyText = %q, want step body", got)
	}
}

func TestFocusedCopyPrefersOpenReasoning(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{
		role:    roleAsst,
		content: "the reply",
		items: []turnItem{{
			thinking: true, text: "the plan", open: true,
		}},
	})
	m.focusIdx = 0
	if got := m.focusedCopyText(); got != "the plan" {
		t.Fatalf("focusedCopyText = %q, want the plan", got)
	}
}

func TestSpanYankCopiesBetweenMarks(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleAsst, content: "alpha"},
		message{role: roleAsst, content: "bravo"},
		message{role: roleAsst, content: "charlie"},
	)
	m.focusIdx = 0
	m.markCopySpan()
	m.focusIdx = 2
	if got := m.spanCopyText(); got != "alpha\n\ncharlie" && got != "alpha\n\nbravo\n\ncharlie" {
		// Inclusive range of reply surfaces between the marks.
		if !strings.Contains(got, "alpha") || !strings.Contains(got, "charlie") {
			t.Fatalf("span = %q", got)
		}
	}
	if !strings.Contains(m.spanCopyText(), "bravo") {
		t.Fatalf("span must include the middle reply: %q", m.spanCopyText())
	}
}

func TestCopyRefusesEmptyAndRaw(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, content: "\x1b[36mhelp", raw: true})
	m.focusIdx = 0
	if got := m.focusedCopyText(); got != "" {
		t.Fatalf("raw card leaked: %q", got)
	}
}

func TestProtocolNoteOldEngine(t *testing.T) {
	m := newTestModel()
	m.odekVersion = "v1.20.0"
	if cmd := m.protocolNote(); cmd == nil {
		t.Fatal("old engine must warn once")
	}
	if !strings.Contains(strings.Join(m.notices, "\n"), "jobs") {
		t.Fatalf("note = %q, want missing contracts", m.notices)
	}
}

func TestProtocolNoteCurrentEngineSilent(t *testing.T) {
	m := newTestModel()
	m.odekVersion = "v2.3.0"
	if cmd := m.protocolNote(); cmd != nil {
		t.Fatal("current engine must stay quiet")
	}
}

func TestProtocolNoteUnknownSilent(t *testing.T) {
	m := newTestModel()
	if cmd := m.protocolNote(); cmd != nil {
		t.Fatal("unknown version must stay quiet")
	}
}

func TestParseOdekVersion(t *testing.T) {
	v, ok := parseOdekVersion("odek v1.40.2")
	if !ok || v != [3]int{1, 40, 2} {
		t.Fatalf("parse = %v ok=%v", v, ok)
	}
	if missing := missingContracts("1.39.0"); len(missing) == 0 {
		t.Fatal("1.39 must miss wake/keepalive/windowTokens")
	}
}
