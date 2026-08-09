package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestWelcomeNarrowFallback verifies the welcome splash drops the block art
// for a one-line wordmark when the terminal is too narrow for it, and that
// the compact render stays within the terminal width.
func TestWelcomeNarrowFallback(t *testing.T) {
	th := newTheme()
	artW := lipgloss.Width(bannerArt[0])

	wide := plain(welcome(th, artW+2, "/tmp"))
	if !strings.Contains(wide, "██████") {
		t.Error("banner at art width should show the block art")
	}

	narrow := plain(welcome(th, artW, "/tmp")) // one column short of art+padding
	if strings.Contains(narrow, "██████") {
		t.Error("narrow banner should drop the block art")
	}
	if !strings.Contains(narrow, "bodek") || !strings.Contains(narrow, "terminal interface") {
		t.Errorf("compact banner should keep wordmark and tagline:\n%s", narrow)
	}
	// The box wraps its content at `width` and then adds its 2-column left
	// padding (pre-existing at every width) — the fallback's job is that no
	// line exceeds that.
	for i, ln := range strings.Split(narrow, "\n") {
		if w := lipgloss.Width(ln); w > artW+2 {
			t.Errorf("line %d wraps past the rendered width (%d > %d): %q", i, w, artW+2, ln)
		}
	}
}
