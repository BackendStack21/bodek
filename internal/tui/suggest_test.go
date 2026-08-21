package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// execAll drives a cmd tree the way the tea runtime would, flattening
// batches so closures actually run.
func execAll(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			execAll(c)
		}
	}
}

// TestSkillSuggestionCard pins the suggestion flow end to end: a
// skill_event "suggested" shows the passive card, alt+s sends the
// skill_prompt_response ack (alt-x skips), typing never answers, and the
// next prompt clears the card.
func TestSkillSuggestionCard(t *testing.T) {
	m, _, skills := approvalRecorder(t)

	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested",
		SkillName: "deploy-helper", Detail: "multi-step deploy observed"})
	if m.skillSuggest == nil {
		t.Fatal("suggested event did not arm the card")
	}
	out := plain(m.View())
	for _, want := range []string{"skill suggested", "deploy-helper", "alt+s", "alt+x"} {
		if !strings.Contains(out, want) {
			t.Errorf("suggestion card missing %q:\n%s", want, out)
		}
	}

	// Typing is never captured by the card.
	m.Update(key("s"))
	if m.skillSuggest == nil {
		t.Fatal("a bare letter answered the suggestion")
	}

	// alt+s saves: the ack reaches the socket and the card clears.
	// execAll flattens the batch (note timer + sender) the runtime would run.
	_, cmd := m.Update(key("alt+s"))
	execAll(cmd)
	select {
	case a := <-skills:
		if a != "save" {
			t.Errorf("skill ack = %q, want save", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no skill_prompt_response sent")
	}
	if m.skillSuggest != nil {
		t.Error("card did not clear after answering")
	}

	// A second suggestion clears with the next prompt; alt+x skips.
	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested", SkillName: "x2"})
	if m.skillSuggest == nil {
		t.Fatal("second suggestion not armed")
	}
	m.submit() // prompt clears the card's window
	if m.skillSuggest != nil {
		t.Error("card survived into the next prompt")
	}
	// Non-suggested skill events never arm the card.
	m.handleEvent(client.Event{Type: "skill_event", SubType: "saved", SkillName: "x3"})
	if m.skillSuggest != nil {
		t.Error("non-suggested skill event armed the card")
	}
}
