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

// TestObsidianCanvasIsTrueBlack pins obsidian's own #000 canvas: a gray or
// blue-tinted host terminal still renders pure black, and the graphite
// answer card keeps its surface on top of it.
func TestObsidianCanvasIsTrueBlack(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	m := newTestModel()
	m.th = themeFrom(obsidian)
	m.width, m.height = 20, 3
	out := m.paintCanvas("header\x1b[0m body\n" + m.th.answerCard.Render("answer"))
	if !strings.HasPrefix(out, "\x1b[38;2;243;243;245m\x1b[48;2;0;0;0m") {
		t.Fatalf("obsidian frame does not start on the black canvas: %q", out)
	}
	if !strings.Contains(out, "\x1b[48;2;14;14;17m") {
		t.Fatal("answer card lost its graphite surface")
	}
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Fatal("canvas leaks into terminal after frame")
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
