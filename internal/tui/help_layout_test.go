package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// helpBindingKeys is every key the /help card taught before it was grouped.
// Grouping and the two-column layout must keep each one on the card.
var helpBindingKeys = []string{
	"⏎", "⇧⏎", "@", "↑↓", "alt+↑↓", "alt+y", "alt+i", "alt+m", "^Y", "alt+r",
	"^F", "↑↓ (inspecting)", "Pg↑↓", "^P^N", "^G", "^R", "^Q", "^O", "^K", "^T",
	"^S", "^X", "^L", "^E", "alt+f", "esc", "/server", "F1", "^C", "wheel",
}

// helpCardAt renders the /help card on a terminal of the given width.
func helpCardAt(t *testing.T, width int) string {
	t.Helper()
	m := newTestModel()
	m.width = width
	m.showHelp()
	return plain(m.msgs[len(m.msgs)-1].content)
}

func TestHelpCardTwoColumnsWhenWide(t *testing.T) {
	wide := helpCardAt(t, 120)
	narrow := helpCardAt(t, 80)

	rows := func(s string) int { return strings.Count(s, "\n") + 1 }
	if rows(wide) >= rows(narrow) {
		t.Errorf("help card at 120 cols has %d rows, want fewer than %d at 80 cols",
			rows(wide), rows(narrow))
	}
	for _, card := range []string{wide, narrow} {
		for _, k := range helpBindingKeys {
			if !strings.Contains(card, k) {
				t.Errorf("help card omits binding %q", k)
			}
		}
		for _, h := range []string{"compose", "navigate", "inspect & copy", "session", "general"} {
			if !strings.Contains(card, h) {
				t.Errorf("help card omits section %q", h)
			}
		}
	}
	for _, line := range strings.Split(wide, "\n") {
		if w := lipgloss.Width(line); w > 120 {
			t.Errorf("wide help line is %d cells, exceeds terminal width 120: %q", w, line)
		}
	}
	// Side by side: the first column's title and the second column's title
	// share a row.
	shared := false
	for _, line := range strings.Split(wide, "\n") {
		if strings.Contains(line, "compose") && strings.Contains(line, "inspect & copy") {
			shared = true
		}
	}
	if !shared {
		t.Errorf("wide card does not place sections side by side:\n%s", wide)
	}
}

func TestHelpCardSingleColumnNarrow(t *testing.T) {
	narrow := helpCardAt(t, 80)
	for _, line := range strings.Split(narrow, "\n") {
		if strings.Contains(line, "compose") && strings.Contains(line, "navigate") {
			t.Errorf("narrow card should stack sections, found a shared row: %q", line)
		}
	}
}
