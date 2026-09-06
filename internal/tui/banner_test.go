package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestWelcomeFitsWidth verifies the first-run home stays a cwd + next-action
// card: no header wordmark, and no line past the terminal width.
func TestWelcomeFitsWidth(t *testing.T) {
	th := newTheme()
	const w = 24
	out := plain(welcome(th, w, "/tmp/project", ""))
	if !strings.Contains(out, "/tmp/project") {
		t.Errorf("welcome missing cwd:\n%s", out)
	}
	if !strings.Contains(out, "type a task") || !strings.Contains(out, "^K") {
		t.Errorf("welcome missing the next-action tip:\n%s", out)
	}
	if strings.Contains(out, "⬡ bodek") {
		t.Error("first-run must not repeat the header wordmark")
	}
	// The box wraps its content at `width` and then adds its 2-column left
	// padding (pre-existing at every width).
	for i, ln := range strings.Split(out, "\n") {
		if got := lipgloss.Width(ln); got > w+2 {
			t.Errorf("line %d wraps past the rendered width (%d > %d): %q", i, got, w+2, ln)
		}
	}
}
