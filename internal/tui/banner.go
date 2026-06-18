package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// bannerArt is the BODEK wordmark in block characters.
var bannerArt = []string{
	"██████   ██████  ██████  ███████ ██   ██",
	"██   ██ ██    ██ ██   ██ ██      ██  ██ ",
	"██████  ██    ██ ██   ██ █████   █████  ",
	"██   ██ ██    ██ ██   ██ ██      ██  ██ ",
	"██████   ██████  ██████  ███████ ██   ██",
}

// purple → pink gradient endpoints for the wordmark.
var (
	gradFrom = [3]int{0xA7, 0x8B, 0xFA}
	gradTo   = [3]int{0xF4, 0x72, 0xB6}
)

// welcome renders the splash shown in the conversation area before the first
// prompt: the wordmark, a tagline, and a few key bindings.
func welcome(th theme, width int) string {
	var b strings.Builder
	for _, line := range bannerArt {
		b.WriteString(gradient(line, gradFrom, gradTo))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(th.tagline.Render("a beautiful terminal interface for the odek agent"))
	b.WriteString("\n\n")

	tips := [][2]string{
		{"type a task", "and press enter to run the agent"},
		{"⏎ send", "·  ^J newline  ·  ^T toggle thinking"},
		{"^L clear", "·  PgUp/PgDn scroll  ·  ^C quit"},
		{"approvals", "answer with [a]pprove [d]eny [t]rust"},
	}
	for _, t := range tips {
		b.WriteString("  " + th.tipKey.Render(pad(t[0], 12)) + th.tipText.Render(t[1]) + "\n")
	}

	block := b.String()
	// Center the splash block within the available width for a polished look.
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(block)
}

// pad right-pads s with spaces to width n.
func pad(s string, n int) string {
	if lipgloss.Width(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-lipgloss.Width(s))
}
