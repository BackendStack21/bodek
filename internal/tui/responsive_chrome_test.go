package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestCrowdedHeaderPreservesSafetyAndConnection(t *testing.T) {
	m := newTestModel()
	m.model = strings.Repeat("long-model-", 12)
	m.bodekVersion = "v1.11.2"
	m.odekVersion = "v2.14.0"
	m.thinking = "high"
	m.disconn = true
	m.status = "reconnecting"
	for _, width := range []int{40, 60, 80} {
		m.resize(width, 30)
		bar, _, _ := strings.Cut(m.header(), "\n")
		if lipgloss.Width(bar) > width {
			t.Errorf("%d: header overflow", width)
		}
		if !strings.Contains(plain(bar), "host access") || !strings.Contains(plain(bar), lampReconnect) {
			t.Errorf("%d: safety/connection hidden: %s", width, plain(bar))
		}
	}
}

func TestWelcomeFitsCellsAndKeepsKeyboardHints(t *testing.T) {
	for _, width := range []int{12, 24, 40, 80, 120} {
		out := welcome(newTheme(), width, "/workspace/项目/very-long-directory-name", "")
		for _, line := range strings.Split(out, "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("%d: line overflow: %q", width, line)
			}
		}
		if width >= 24 && (!strings.Contains(plain(out), "⇧⏎ newline") || !strings.Contains(plain(out), "^K commands")) {
			t.Errorf("%d: missing keyboard affordances", width)
		}
	}
}
