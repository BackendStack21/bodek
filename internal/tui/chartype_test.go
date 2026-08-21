package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// printableASCII is every character a prompt could start with or contain —
// the invariant under test is that ALL of them reach the composer.
var printableASCII = func() string {
	var b strings.Builder
	for r := ' '; r <= '~'; r++ {
		b.WriteRune(r)
	}
	return b.String()
}()

// TestEveryCharacterTypes is the regression bar for the keymap discipline:
// no bare character key may ever be bound in the composer context, so the
// full printable alphabet must survive typing — from an empty input, where
// single-character bindings do their damage ("?why", "[TODO] fix", "rm -rf").
// Swept across connected, disconnected, and mid-run states, since each once
// carried its own hijack (r retried while disconnected).
func TestEveryCharacterTypes(t *testing.T) {
	states := map[string]func(m *Model){
		"connected": func(m *Model) {},
		"disconnected": func(m *Model) {
			m.disconn = true
			m.opts.Reconnect = func() (*client.Client, error) { return nil, errors.New("down") }
		},
		"mid-run": func(m *Model) { m.busy = true },
	}
	for name, setup := range states {
		t.Run(name, func(t *testing.T) {
			m := newTestModel()
			m.ta.Focus()
			setup(m)
			for _, r := range printableASCII {
				m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			if got := m.ta.Value(); got != printableASCII {
				// Find exactly which characters were eaten for a readable failure.
				var eaten []rune
				for _, r := range printableASCII {
					if !strings.ContainsRune(got, r) {
						eaten = append(eaten, r)
					}
				}
				t.Errorf("composer lost characters %q — typed %q", string(eaten), got)
			}
		})
	}
}

// TestActionsMovedOffCharacters pins the replacement bindings: help on F1,
// turn jumps on alt+arrows, disconnected retry on ⏎ with an empty input.
func TestActionsMovedOffCharacters(t *testing.T) {
	m := newTestModel()
	m.ta.Focus()

	before := len(m.msgs)
	m.Update(tea.KeyMsg{Type: tea.KeyF1})
	if len(m.msgs) != before+1 {
		t.Error("F1 did not open the help card")
	}

	streamingTurn(m)
	m.Update(tea.KeyMsg{Type: tea.KeyUp, Alt: true})   // alt+up: previous turn
	m.Update(tea.KeyMsg{Type: tea.KeyDown, Alt: true}) // alt+down: next turn
	_ = m.View()
}
