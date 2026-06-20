package tui

import (
	"os"
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
// prompt: the wordmark, a tagline, the working directory, and a few key
// bindings — left-aligned with a gentle margin.
func welcome(th theme, width int, cwd string) string {
	var b strings.Builder
	for _, line := range bannerArt {
		b.WriteString(gradient(line, gradFrom, gradTo))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(th.tagline.Render("a beautiful terminal interface for the odek agent"))
	b.WriteByte('\n')
	if dir := shortenHome(cwd); dir != "" {
		b.WriteString(th.statsDim.Render(dir))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	// key column is left-aligned to a fixed width so both the keys and their
	// descriptions line up on a flush left edge.
	tips := [][2]string{
		{"type a task", "and press enter to run the agent"},
		{"/ commands", "type / for commands, e.g. /help /sessions /model"},
		{"/stats", "session metrics & live context-window gauge"},
		{"@ to attach", "attach files, e.g. @main.go"},
		{"⏎ send", "·  ^J newline  ·  ^T toggle thinking"},
		{"^L clear", "·  ↑/↓ scroll  ·  PgUp/PgDn page  ·  ^C quit"},
		{"approvals", "answer with [a]pprove [d]eny [t]rust"},
	}
	const keyW = 11
	for _, t := range tips {
		b.WriteString(th.tipKey.Render(padRight(t[0], keyW)) + "  " + th.tipText.Render(t[1]) + "\n")
	}

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
