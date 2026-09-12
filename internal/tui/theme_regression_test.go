package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestEmberLightCodeBlocksNotDark reproduces the "blacks still observed after
// enabling Ember Light" report: answerGlamourStyle adopts glamour's stock
// LightStyleConfig wholesale for ember-light, and that preset paints chroma
// code-block backgrounds #373737 — a near-black panel inside every fenced
// code block rendered on the parchment card. The light theme must not emit
// dark background SGRs for code blocks.
func TestEmberLightCodeBlocksNotDark(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(oldProfile)

	t.Setenv("BODEK_THEME", "ember-light")
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(answerGlamourStyle()),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render("```go\nfunc main() {}\n```\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "48;2;55;55;55") { // #373737
		t.Errorf("ember-light code block paints glamour's near-black #373737 background:\n%q", out)
	}
}

// TestLightCanvasPaintsEveryRow reproduces "blacks are still observed after
// enabling Ember Light": paintCanvas frames the body with an unstyled
// lipgloss Width/Height style and only re-asserts the canvas after embedded
// SGR resets — rows that contain no escapes at all (blank transcript rows,
// plain text lines, bottom padding) carry no background and fall back to the
// terminal's own (dark) background. Every visible row must paint the canvas.
func TestLightCanvasPaintsEveryRow(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(oldProfile)

	m := newTestModel()
	m.th = themeFrom(emberLight)
	m.width, m.height = 20, 5
	// Row 2 is completely escape-free, like a plain transcript line; row 4
	// is empty; row 5 only exists as bottom padding.
	out := m.paintCanvas("styled \x1b[0mrow\nplain row no escapes\n\nstyled \x1b[0magain")
	bg := "\x1b[48;2;250;248;242m"
	for i, line := range strings.Split(out, "\n") {
		if lipgloss.Width(plain(line)) == 0 && line == "" {
			continue
		}
		if !strings.Contains(line, bg) && !strings.Contains(line, "\x1b[48") {
			t.Errorf("row %d paints no canvas background (terminal black bleeds through): %q", i, line)
		}
	}
}

// TestHelpCardRethemedAfterSwitch reproduces "Ember Light misses some
// components": the raw /help card is a point-in-time styled snapshot and
// switchTheme's resize() skips raw messages, so after switching themes the
// help card keeps the previous palette's colors. It must re-render with the
// active theme.
func TestHelpCardRethemedAfterSwitch(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(oldProfile)

	old := themeOverride
	defer func() { themeOverride = old }()

	m := newTestModel()
	themeOverride = "ember-dark"
	m.th = themeFrom(emberDark)
	m.showHelp()

	rendered, _ := m.renderMessage(m.msgs[len(m.msgs)-1], len(m.msgs)-1, 0)
	block := rendered
	if !strings.Contains(block, "38;2;168;176;192") { // ember-dark muted #A8B0C0
		t.Fatalf("precondition: help card carries ember-dark muted color:\n%q", block)
	}

	m.switchTheme("ember-light")

	rendered, _ = m.renderMessage(m.msgs[len(m.msgs)-1], len(m.msgs)-1, 0)
	block = rendered
	if strings.Contains(block, "38;2;168;176;192") {
		t.Errorf("help card still styled with ember-dark muted after switch to ember-light:\n%q", block)
	}
	if !strings.Contains(block, "38;2;89;95;109") { // ember-light muted #5A5F6D (lipgloss quantizes g−1)
		t.Errorf("help card missing ember-light muted color after switch:\n%q", block)
	}
}
