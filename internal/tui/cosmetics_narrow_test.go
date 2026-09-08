package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestWelcomeTipFitsNarrowTerminal: the teaching tip must not wrap on an
// 80-column terminal — the smallest realistic first-run surface.
func TestWelcomeTipFitsNarrowTerminal(t *testing.T) {
	out := plain(welcome(newTheme(), 80, "/somewhere", ""))
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 80 {
			t.Errorf("welcome line %d is %d cols (>80): %q", i+1, w, line)
		}
	}
	if !strings.Contains(out, "click to copy") {
		t.Errorf("narrow welcome lost the copy affordance:\n%s", out)
	}
}
