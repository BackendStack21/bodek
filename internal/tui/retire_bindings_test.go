package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── Tab / [ ] retirement: transcript inspector bindings are removed ────────
// Traversal keeps its arrow forms (up/down inside the inspector); Tab,
// Shift+Tab, and the [ ] paging keys are gone from code and docs. Bare
// [ and ] must always type into the composer.

// armedInspector arms an inspect focus on a step so the retired bindings can
// be probed.
func armedInspector(t *testing.T) *Model {
	t.Helper()
	m := newTestModel()
	busyTurn(m)
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"ls"}`})
	m.handleEvent(client.Event{Type: "tool_result", Name: "shell", Data: strings.Repeat("line\n", 60)})
	m.handleEvent(client.Event{Type: "tool_call", Name: "read_file", Data: `{"path":"x.go"}`})
	m.handleEvent(client.Event{Type: "tool_result", Name: "read_file", Data: "body"})
	m.handleEvent(client.Event{Type: "done"})
	m.moveInspect(false)
	if !m.validInspect() {
		t.Fatal("precondition: inspector must be armed")
	}
	return m
}

// TestTabNoLongerMovesInspect: tab/shift+tab are retired — they must not
// move the inspector focus anymore (arrows still do).
func TestTabNoLongerMovesInspect(t *testing.T) {
	m := armedInspector(t)
	before := *m.inspect
	m.handleKey(key("tab"))
	if m.inspect == nil || *m.inspect != before {
		t.Error("tab still moved the inspector focus")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.inspect == nil || *m.inspect != before {
		t.Error("shift+tab still moved the inspector focus")
	}
	// Arrows remain the traversal form inside the inspector.
	m.handleKey(key("down"))
	if m.inspect == nil {
		t.Error("down must still traverse inspector items")
	}
}

// TestBracketsNoLongerPageToolDetail: the [ ] paging case is gone — the
// detail offset must not move on those keys (seeded mid-list so both
// directions are observable).
func TestBracketsNoLongerPageToolDetail(t *testing.T) {
	m := armedInspector(t)
	p := *m.inspect
	if p.stepIdx < 0 {
		t.Skip("focus landed on reasoning; tool paging not applicable")
	}
	s := &m.msgs[p.msgIdx].steps[p.stepIdx]
	s.expanded = true
	s.detailOffset = 16 // mid-list: [ would decrease, ] would increase
	m.handleKey(key("["))
	if s.detailOffset != 16 {
		t.Errorf("[ still paged the tool detail (offset %d)", s.detailOffset)
	}
	m.handleKey(key("]"))
	if s.detailOffset != 16 {
		t.Errorf("] still paged the tool detail (offset %d)", s.detailOffset)
	}
	// Bare runes return the keyboard to the composer (inspector cleared).
	// PgDn is the paging form once the inspector is re-armed.
	if m.inspect != nil {
		t.Error("bare [ must return focus to the composer while inspecting")
	}
	m.moveInspect(false)
	m.handleKey(key("pgdown"))
	if s.detailOffset <= 16 {
		t.Errorf("pgdown did not advance the page (offset %d)", s.detailOffset)
	}
}

// TestBracketsTypeIntoComposer: bare [ and ] always type.
func TestBracketsTypeIntoComposer(t *testing.T) {
	m := newTestModel()
	m.handleKey(key("["))
	m.handleKey(key("]"))
	if got := m.ta.Value(); got != "[]" {
		t.Fatalf("composer draft = %q, want \"[]\"", got)
	}
}

// TestTabIdleNoInspect: tab at the composer with no inspector open must do
// nothing (no traversal side effects).
func TestTabIdleNoInspect(t *testing.T) {
	m := newTestModel()
	m.handleKey(key("tab"))
	if m.inspect != nil {
		t.Error("tab armed the inspector from idle")
	}
}

// TestInspectFooterRetiredLabels: the inspector footer no longer advertises
// Tab or [ ] keys.
func TestInspectFooterRetiredLabels(t *testing.T) {
	m := armedInspector(t)
	foot := plain(m.footer())
	for _, banned := range []string{"Tab next", "[ ] page", "Tab select"} {
		if strings.Contains(foot, banned) {
			t.Errorf("footer still advertises retired binding %q: %q", banned, foot)
		}
	}
}
