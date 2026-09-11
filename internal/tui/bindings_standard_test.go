package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── bindings standardization (plan: .plans/KEY_BINDINGS_FIX_PLAN.md) ────────
// Every chord action keeps a modifier-free fallback for terminals that
// cannot deliver Alt (macOS Option-as-UTF-8), gated the same way as the
// approval plain keys: card visible + composer draft empty + not a paste.

// TestSuggestPlainKeysDecideEmptyDraft: bare s/x answer a pending skill
// suggestion while the draft is empty (mirrors approval a/d/t).
func TestSuggestPlainKeysDecideEmptyDraft(t *testing.T) {
	m, _, skills := approvalRecorder(t)
	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested",
		SkillName: "deploy-helper", Detail: "observed"})
	if m.skillSuggest == nil {
		t.Fatal("precondition: suggestion must be armed")
	}
	_, cmd := m.Update(key("s"))
	execAll(cmd)
	select {
	case a := <-skills:
		if a != "save" {
			t.Errorf("skill ack = %q, want save", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("plain s did not save the suggestion")
	}
	if m.skillSuggest != nil {
		t.Error("card did not clear after plain s")
	}

	// Plain x skips a second suggestion.
	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested", SkillName: "x2"})
	_, cmd = m.Update(key("x"))
	execAll(cmd)
	select {
	case a := <-skills:
		if a != "skip" {
			t.Errorf("skill ack = %q, want skip", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("plain x did not skip the suggestion")
	}
}

// TestSuggestPlainKeysNeedEmptyDraft: a non-empty draft routes s/x to the
// composer — typing always wins.
func TestSuggestPlainKeysNeedEmptyDraft(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested", SkillName: "deploy-helper"})
	m.Update(key("q")) // non-empty draft
	m.Update(key("s"))
	m.Update(key("x"))
	if m.skillSuggest == nil {
		t.Fatal("plain letter must not answer while the draft is non-empty")
	}
	if got := m.ta.Value(); got != "qsx" {
		t.Errorf("composer draft = %q, want letters appended", got)
	}
	// Paste never answers even on an empty draft.
	m.ta.SetValue("")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Paste: true})
	if m.skillSuggest == nil {
		t.Fatal("pasted text must not answer a suggestion")
	}
}

// TestCtrlKGatedDuringApproval: ctrl+k must not open the palette over a live
// approval card — the approval captures the keyboard until answered.
func TestCtrlKGatedDuringApproval(t *testing.T) {
	m, _, _ := approvalRecorder(t)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
	m.Update(key("ctrl+k"))
	if m.pal.open {
		t.Error("ctrl+k opened the palette over a live approval")
	}
	if m.curApproval() == nil {
		t.Error("approval must remain head")
	}
	// Without an approval, ctrl+k still opens the palette.
	m.Update(key("alt+d")) // deny the approval
	m.Update(key("ctrl+k"))
	if !m.pal.open {
		t.Error("ctrl+k must still open the palette without an approval")
	}
}

// TestQFocusBeatsSuggest: while the ^Q queue strip holds focus, alt-chords
// belong to the strip — a pending suggestion underneath is not answered.
func TestQFocusBeatsSuggest(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested", SkillName: "deploy-helper"})
	m.queue = append(m.queue, "held prompt") // focused strip must stay live
	m.busy = false
	unfoldQueue(m)
	m.Update(key("alt+s"))
	if m.skillSuggest == nil {
		t.Error("alt+s answered a suggestion while the queue strip held focus")
	}
}

// TestUnmappedChordNote: a well-formed enhanced-key chord that fails to
// decode surfaces as a transient note instead of vanishing silently.
func TestUnmappedChordNote(t *testing.T) {
	msg := FilterShiftEnter(nil, []byte("\x1b[999;5u"))
	km, ok := msg.(tea.KeyMsg)
	if !ok || km.String() != "alt+unmapped-chord" {
		t.Fatalf("FilterShiftEnter unmapped chord = %#v, want alt+unmapped-chord sentinel", msg)
	}
	m := newTestModel()
	_, cmd := m.handleKey(km)
	applyQuick(m, cmd)
	found := false
	for _, note := range m.notices {
		if strings.Contains(note, "unmapped") {
			found = true
		}
	}
	if !found {
		t.Errorf("unmapped chord produced no transient note: %v", m.notices)
	}
	// The sentinel must never type into the composer.
	if got := m.ta.Value(); got != "" {
		t.Errorf("sentinel leaked into composer: %q", got)
	}
}
