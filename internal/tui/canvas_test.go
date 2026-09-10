package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestLightCanvasCoversTerminalAndPreservesCardSurfaces(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	m := newTestModel()
	m.th = themeFrom(emberLight)
	m.width, m.height = 20, 4
	card := m.th.answerCard.Render("answer")
	out := m.paintCanvas("header\x1b[0m body\n" + card)
	bg := "\x1b[48;2;250;248;242m"
	if !strings.Contains(out, bg) {
		t.Fatal("light canvas missing on a dark host terminal")
	}
	if !strings.Contains(out, "\x1b[48;2;239;236;229m") {
		t.Fatal("answer card lost its own surface")
	}
	rows := strings.Split(plain(out), "\n")
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	for _, line := range rows {
		if lipgloss.Width(line) != 20 {
			t.Fatalf("unpainted gutter: %q", line)
		}
	}
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Fatal("canvas leaks into terminal after frame")
	}
	for _, p := range []palette{emberDark, emberHighContrast, classic} {
		m.th = themeFrom(p)
		if m.th.canvasColor != "" {
			t.Fatalf("theme %v unexpectedly has canvas", p.surface)
		}
		if got := m.paintCanvas("original"); got != "original" {
			t.Fatal("transparent theme acquired a canvas")
		}
	}
}

func TestLightThemeSwitchUpdatesComposerAndCanvas(t *testing.T) {
	old := themeOverride
	defer func() { themeOverride = old }()
	themeOverride = "ember-dark"
	m := newTestModel()
	m.resize(80, 24)
	m.ta.SetValue("keep this draft")
	m.switchTheme("ember-light")
	if m.th.canvasColor != emberLight.canvas {
		t.Fatal("light theme has no canvas")
	}
	if m.ta.FocusedStyle.Text.GetForeground() != emberLight.text || m.ta.BlurredStyle.Text.GetForeground() != emberLight.text {
		t.Fatal("composer text did not switch")
	}
	if m.ta.Value() != "keep this draft" {
		t.Fatal("theme switch lost draft")
	}
	m.switchTheme("ember-dark")
	if m.th.canvasColor != "" {
		t.Fatal("dark theme retained light canvas")
	}
}
