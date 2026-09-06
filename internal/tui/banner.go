package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// welcome is the empty-session home: where the agent will work, and one
// next action. Branding lives in the header; F1 / /help hold the rest.
func welcome(th theme, width int, cwd, lastSession string) string {
	var b strings.Builder
	if dir := shortenHome(cwd); dir != "" {
		b.WriteString(th.statsDim.Render(dir))
		b.WriteByte('\n')
		b.WriteByte('\n')
	}
	if title := sanitize(lastSession); title != "" {
		b.WriteString(th.statsDim.Render("last session · "+truncate(collapse(title), max(width-20, 12))) + "\n")
		b.WriteString(th.tipText.Render("/new starts fresh") + "\n")
		b.WriteByte('\n')
	}
	b.WriteString(th.tipKey.Render("type a task") + "  " + th.tipText.Render("⏎ sends · ^K everything") + "\n")

	block := strings.TrimRight(b.String(), "\n")
	// Left-aligned (no centering) with a small left margin for breathing room.
	return lipgloss.NewStyle().Width(width).PaddingLeft(2).Render(block)
}

// padLeft left-pads s with spaces to width n (right-aligns within the column).
func padLeft(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return strings.Repeat(" ", n-w) + s
}

// padRight right-pads s with spaces to width n (left-aligns within the column).
func padRight(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// shortenHome replaces a leading $HOME with "~" for a compact, readable path.
func shortenHome(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(os.PathSeparator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}
