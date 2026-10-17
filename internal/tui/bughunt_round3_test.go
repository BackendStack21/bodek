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

// An unknown slash name keeps the draft (it may be a path-led prompt).
func TestRegressUnknownCommandKeepsDraft(t *testing.T) {
	m := newTestModel()
	m.ta.SetValue("/etc/hosts has a bad entry")
	m.runCommandLine("/etc/hosts has a bad entry")
	if got := m.ta.Value(); got != "/etc/hosts has a bad entry" {
		t.Fatalf("draft = %q", got)
	}
}

// Typing an exact command name selects it over longer prefix matches.
func TestRegressExactCommandLeadsPopup(t *testing.T) {
	m := newTestModel()
	m.openCmdAC("ru")
	m.ac.sel = len(m.ac.items) - 1
	m.openCmdAC("run")
	if got := m.ac.items[m.ac.sel].ID; got != "/run" {
		t.Fatalf("selected %q for /run", got)
	}
}

// The find bar takes spaces (KeySpace) for multi-word queries.
func TestRegressFindAcceptsSpace(t *testing.T) {
	m := newTestModel()
	m.openFind()
	m.handleFindKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m.handleFindKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m.handleFindKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if got := string(m.find.query); got != "a b" {
		t.Fatalf("query = %q", got)
	}
}

// The palette keeps multi-rune reads and pastes, not only single runes.
func TestRegressPaletteKeepsMultiRuneInput(t *testing.T) {
	m := newTestModel()
	m.togglePalette()
	m.handlePaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("se")})
	m.handlePaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s\x07"), Paste: true})
	m.handlePaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+enter")})
	if m.pal.query != "ses" {
		t.Fatalf("query = %q", m.pal.query)
	}
}
