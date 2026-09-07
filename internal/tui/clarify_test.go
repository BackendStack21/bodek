package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestClarify_RequestRendersAndSends(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "clarify_request", ID: "clr-1", Question: "Which approach?"})
	if m.clarify == nil || m.clarify.ID != "clr-1" {
		t.Fatal("clarify card not armed")
	}
	out := plain(m.View())
	if !strings.Contains(out, "Which approach?") {
		t.Fatalf("question missing from view:\n%s", out)
	}
	got, _ := m.handleClarifyKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("the first")})
	m = got.(*Model)
	if m.clarifyBuf != "the first" {
		t.Fatalf("clarifyBuf = %q", m.clarifyBuf)
	}
}

func TestClarify_EmptyAnswerDoesNotSend(t *testing.T) {
	m := newTestModel()
	m.clarify = &client.Event{Type: "clarify_request", ID: "clr-1", Question: "q"}
	if cmd := m.sendClarifyAnswer(); cmd != nil {
		t.Fatal("empty answer must not send")
	}
	if m.clarify == nil {
		t.Fatal("empty send must keep the card")
	}
}

func TestClarify_AckClearsCard(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "clarify_request", ID: "clr-1", Question: "q"})
	m.handleEvent(client.Event{Type: "clarify_ack", ID: "clr-1"})
	if m.clarify != nil {
		t.Fatal("ack must dismiss the card")
	}
}

func TestClarify_ExpiredClearsCard(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "clarify_request", ID: "clr-1", Question: "q"})
	m.handleEvent(client.Event{Type: "clarify_expired", ID: "clr-1"})
	if m.clarify != nil {
		t.Fatal("expired must dismiss the card")
	}
}
