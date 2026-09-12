package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// paintCanvas gives light mode its own background without changing the user's
// terminal settings. Padding covers blank rows and gutters; embedded resets
// return to the canvas while explicitly colored answer cards remain intact.
func (m *Model) paintCanvas(body string) string {
	if m.th.canvasColor == "" || m.width <= 0 || m.height <= 0 {
		return body
	}
	frame := lipgloss.NewStyle().Width(m.width).Height(m.height).Render(body)
	bg := surfaceSGR(m.th.canvas)
	if bg == "" {
		return frame
	}
	// A foreground-only probe gives the default text color in the active profile.
	probe := lipgloss.NewStyle().Foreground(m.th.canvas.GetForeground()).Render(" ")
	fg := ""
	if i := strings.IndexByte(probe, ' '); i >= 0 {
		fg = probe[:i]
	}
	base := fg + bg
	frame = strings.NewReplacer("\x1b[0m", "\x1b[0m"+base,
		"\x1b[m", "\x1b[m"+base, "\x1b[49m", bg, "\x1b[39m", fg).Replace(frame)
	// Reset-at-line-start insurance: a reset at the very end of a row clears
	// the background for the whole following row on some terminals, and rows
	// that never carried an escape (blank transcript rows, plain text lines,
	// bottom padding) were painted by nothing at all — the terminal's own
	// background bled through as black stripes on the parchment canvas.
	// Every row starts on the canvas.
	frame = strings.ReplaceAll(frame, "\n", "\n"+base)
	return base + frame + "\x1b[0m"
}
