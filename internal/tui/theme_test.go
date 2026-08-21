package tui

import (
	"strings"
	"testing"
)

// TestThemeVariants verifies every palette variant builds a complete theme
// and renders the welcome card without panicking — the degenerate-case guard
// for EMBER Terminal's token system.
func TestThemeVariants(t *testing.T) {
	for name, p := range map[string]func() theme{
		"ember-dark":    func() theme { return themeFrom(emberDark) },
		"ember-light":   func() theme { return themeFrom(emberLight) },
		"high-contrast": func() theme { return themeFrom(emberHighContrast) },
		"classic":       func() theme { return themeFrom(classic) },
	} {
		th := p()
		if got := plain(th.logo.Render("⬡ bodek")); !strings.Contains(got, "bodek") {
			t.Errorf("%s: logo render broken: %q", name, got)
		}
		if out := plain(welcome(th, 100, "/tmp")); !strings.Contains(out, "palette") {
			t.Errorf("%s: welcome card missing the palette tip", name)
		}
	}
}

// TestThemeSelection pins the BODEK_THEME env mapping.
func TestThemeSelection(t *testing.T) {
	cases := map[string]string{
		"":              "ember-dark",
		"classic":       "classic",
		"light":         "ember-light",
		"ember-light":   "ember-light",
		"high-contrast": "high-contrast",
		"CONTRAST":      "high-contrast",
	}
	for env, want := range cases {
		t.Setenv("BODEK_THEME", env)
		if got := themeName(); got != want {
			t.Errorf("BODEK_THEME=%q → %q, want %q", env, got, want)
		}
	}
}

// TestWelcomeTeachesPalette guards the first-run teaching surface.
func TestWelcomeTeachesPalette(t *testing.T) {
	out := plain(welcome(newTheme(), 100, "/somewhere"))
	for _, want := range []string{"^K palette", "approvals", "@ to attach"} {
		if !strings.Contains(out, want) {
			t.Errorf("welcome missing %q:\n%s", want, out)
		}
	}
}
