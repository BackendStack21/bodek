package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// A queue drain sends the queued prompt without wiping what the user is
// typing meanwhile (sendPrompt used to reset the composer on every path).
func TestRegressQueueDrainKeepsDraft(t *testing.T) {
	m := newTestModel()
	m.queue = []string{"queued B"}
	m.ta.SetValue("half typed follow-up")
	if cmd := m.sendQueued(); cmd == nil {
		t.Fatal("queued prompt was not sent")
	}
	if got := m.ta.Value(); got != "half typed follow-up" {
		t.Fatalf("draft = %q after drain", got)
	}
	if m.lastPrompt != "queued B" {
		t.Fatalf("lastPrompt = %q", m.lastPrompt)
	}
}

// A bare a/d/t typed while a text field is on screen must not decide an
// approval that arrived meanwhile; Alt still does.
func TestRegressBareApprovalKeysSkipOpenTextFields(t *testing.T) {
	for name, open := range map[string]func(m *Model){
		"find":   func(m *Model) { m.openFind() },
		"search": func(m *Model) { m.panel = panelSessions; m.panelEdit = panelEditSearch },
	} {
		m := newTestModel()
		open(m)
		m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Risk: "shell_exec", Command: "rm x"})
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		if m.curApproval() == nil {
			t.Fatalf("%s: bare 'a' approved while the field was open", name)
		}
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}, Alt: true})
		if m.curApproval() != nil {
			t.Fatalf("%s: alt+a did not approve", name)
		}
	}
}
