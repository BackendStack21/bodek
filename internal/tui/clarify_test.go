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

func TestClarify_SpaceAndPunctuationType(t *testing.T) {
	// The spacebar is KeySpace, not KeyRunes — a KeyRunes-only handler
	// mashed words together and dropped punctuation that some terminals
	// also send as single-character special keys.
	m := newTestModel()
	m.handleEvent(client.Event{Type: "clarify_request", ID: "clr-1", Question: "Which approach?"})

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("use")})
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("the first, please!")})

	if got, want := m.clarifyBuf, "use the first, please!"; got != want {
		t.Fatalf("clarifyBuf = %q, want %q", got, want)
	}
	out := plain(m.View())
	if !strings.Contains(out, "use the first, please!") {
		t.Fatalf("typed answer missing from the card:\n%s", out)
	}
}

func TestClarify_PasteWordSpaceIsNotASpacebar(t *testing.T) {
	// The word "space" arriving as KeyRunes (paste / merged runes) must
	// insert those five letters, not collapse to a single KeySpace.
	m := newTestModel()
	m.handleEvent(client.Event{Type: "clarify_request", ID: "clr-1", Question: "q"})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("space")})
	if m.clarifyBuf != "space" {
		t.Fatalf("pasted %q, want %q", m.clarifyBuf, "space")
	}
}

func TestClarify_PrintableASCIITypes(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "clarify_request", ID: "clr-1", Question: "q"})
	var want strings.Builder
	for r := rune(' '); r <= '~'; r++ {
		if r == ' ' {
			m.Update(tea.KeyMsg{Type: tea.KeySpace})
		} else {
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		want.WriteRune(r)
	}
	if m.clarifyBuf != want.String() {
		var eaten []rune
		for _, r := range want.String() {
			if !strings.ContainsRune(m.clarifyBuf, r) {
				eaten = append(eaten, r)
			}
		}
		t.Fatalf("clarify form lost characters %q — typed %q", string(eaten), m.clarifyBuf)
	}
}

func TestClarify_LongAnswerWrapsInsteadOfTruncating(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "clarify_request", ID: "clr-1", Question: "q"})
	answer := strings.Repeat("word ", 40) + "end."
	for _, r := range answer {
		if r == ' ' {
			m.Update(tea.KeyMsg{Type: tea.KeySpace})
			continue
		}
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.clarifyBuf != answer {
		t.Fatalf("buffer dropped characters: %q", m.clarifyBuf)
	}
	out := plain(m.clarifyPanel())
	if strings.Contains(out, "…") {
		t.Fatalf("answer was truncated with an ellipsis:\n%s", out)
	}
	if !strings.Contains(out, "end.") {
		t.Fatalf("wrapped answer missing its tail:\n%s", out)
	}
}

func TestClarify_NewlineChordAndBackspace(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "clarify_request", ID: "clr-1", Question: "q"})
	m.Update(key("a"))
	m.Update(key("shift+enter"))
	m.Update(key("b"))
	if m.clarifyBuf != "a\nb" {
		t.Fatalf("newline chord: clarifyBuf = %q", m.clarifyBuf)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if m.clarifyBuf != "a\n" {
		t.Fatalf("delete: clarifyBuf = %q", m.clarifyBuf)
	}
}

func TestClarify_InternalSpacesSurviveTrimOnSend(t *testing.T) {
	m := newTestModel()
	m.clarify = &client.Event{Type: "clarify_request", ID: "clr-1", Question: "q"}
	m.clarifyBuf = "  the first  "
	// Empty after trim must not send; internal spaces must remain once
	// there is a non-space character. We only assert the trim here —
	// SendClarify needs a live client.
	if strings.TrimSpace(m.clarifyBuf) != "the first" {
		t.Fatal("precondition: trim keeps internal spaces")
	}
	m.clarifyBuf = "   "
	if cmd := m.sendClarifyAnswer(); cmd != nil {
		t.Fatal("whitespace-only answer must not send")
	}
}

func TestClarify_ModePillAndCtrlC(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "clarify_request", ID: "clr-1", Question: "q"})
	if got := m.modeName(); got != "question" {
		t.Fatalf("modeName = %q, want question", got)
	}
	m.Update(key("ctrl+c"))
	if m.confirm != confirmQuit || m.quitting {
		t.Fatalf("ctrl+c from clarify did not arm the gate: confirm=%v quitting=%v", m.confirm, m.quitting)
	}
}

func TestClarifyTypedDropsAltChords(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true}
	if got := clarifyTyped(msg); got != "" {
		t.Fatalf("alt chord must not type into the buffer: %q", got)
	}
	if got := clarifyTyped(tea.KeyMsg{Type: tea.KeySpace}); got != " " {
		t.Fatalf("KeySpace typed %q, want a space", got)
	}
}
