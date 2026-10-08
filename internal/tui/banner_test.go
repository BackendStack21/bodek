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
	if !strings.Contains(out, "⏎ send") || !strings.Contains(out, "@ attach a file") {
		t.Errorf("welcome missing the next-action tip:\n%s", out)
	}
	if strings.Contains(out, "type a task") || strings.Contains(out, "^K") {
		t.Errorf("welcome repeats the composer placeholder or footer key:\n%s", out)
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

// TestFirstRunHomeAnchorsAboveComposer: at 100x34 the home hugs the composer
// (cwd in the lower half of the transcript, one blank row above the input
// box), and at 40x12 the full view still fits the terminal.
func TestFirstRunHomeAnchorsAboveComposer(t *testing.T) {
	m := newTestModel()
	m.opts.CWD = "/workspace/payments"
	m.resize(100, 34)
	lines := strings.Split(plain(m.View()), "\n")
	if len(lines) != 34 {
		t.Fatalf("view = %d rows, want 34", len(lines))
	}
	cwd, tip, box := -1, -1, -1
	for i, ln := range lines {
		switch {
		case cwd < 0 && strings.Contains(ln, "/workspace/payments"):
			cwd = i
		case strings.Contains(ln, "⏎ send"):
			tip = i
		case box < 0 && strings.HasPrefix(strings.TrimSpace(ln), "╭"):
			box = i
		}
	}
	if cwd < 0 || tip < 0 || box < 0 {
		t.Fatalf("home or composer missing (cwd=%d tip=%d box=%d):\n%s", cwd, tip, box, strings.Join(lines, "\n"))
	}
	if top := headerHeight + m.vp.Height/2; cwd < top {
		t.Errorf("cwd on row %d, want the lower half of the transcript (row >= %d)", cwd, top)
	}
	if box-tip != 2 || strings.TrimSpace(lines[tip+1]) != "" {
		t.Errorf("last home line at row %d, composer box at row %d: want exactly one blank row between", tip, box)
	}

	m.resize(40, 12)
	if got := viewRows(m); got != 12 {
		t.Errorf("40x12 view = %d rows, want 12", got)
	}
	for i, ln := range strings.Split(plain(m.View()), "\n") {
		if w := lipgloss.Width(ln); w > 40 {
			t.Errorf("40x12 line %d is %d cols: %q", i, w, ln)
		}
	}
}
