package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestEmberThemeReadability checks the token pairs that carry body and
// secondary information. The reference backgrounds are explicit: dark uses
// the answer card surface, light uses its parchment card, and high contrast
// assumes the black terminal background it is designed for.
func TestEmberThemeReadability(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(oldProfile)

	cases := []struct {
		name string
		p    palette
		bg   string
	}{
		{"ember-dark", emberDark, string(emberDark.surface)},
		{"ember-light", emberLight, string(emberLight.surface)},
		{"high-contrast", emberHighContrast, "#000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for name, fg := range map[string]lipgloss.Color{
				"answer text":      tc.p.text,
				"secondary labels": tc.p.muted,
				"tool output":      tc.p.bodyText,
				"focused text":     tc.p.accentHi,
				"machine elements": tc.p.steel,
				"success status":   tc.p.green,
				"warning status":   tc.p.yellow,
				"error status":     tc.p.red,
			} {
				if got := contrastRatio(string(fg), tc.bg); got < 4.5 {
					t.Errorf("%s contrast against %s = %.2f, want at least 4.5", name, tc.bg, got)
				}
			}
		})
	}
}

// TestThemeSemanticStyles keeps the visual hierarchy intentional: focused
// rows use the highlight token, while secondary metadata remains readable
// without consuming the full-brightness answer token.
func TestThemeSemanticStyles(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(oldProfile)

	for name, p := range map[string]palette{
		"ember-dark":    emberDark,
		"ember-light":   emberLight,
		"high-contrast": emberHighContrast,
	} {
		th := themeFrom(p)
		focused := lipgloss.NewStyle().Foreground(p.accentHi).Bold(true).Render("focused")
		if got := th.acSel.Render("focused"); got != focused {
			t.Errorf("%s focused style does not use accentHi", name)
		}
		secondary := lipgloss.NewStyle().Foreground(p.muted).Italic(true).Render("metadata")
		if got := th.statsDim.Render("metadata"); got != secondary {
			t.Errorf("%s stats metadata does not use muted", name)
		}
		if got := th.acDetail.Render("detail"); got != lipgloss.NewStyle().Foreground(p.muted).Render("detail") {
			t.Errorf("%s detail metadata does not use muted", name)
		}
	}
}

func contrastRatio(fg, bg string) float64 {
	lf := relativeLuminance(parseRGB(fg))
	lb := relativeLuminance(parseRGB(bg))
	if lf < lb {
		lf, lb = lb, lf
	}
	return (lf + 0.05) / (lb + 0.05)
}

func parseRGB(hex string) [3]float64 {
	hex = strings.TrimPrefix(hex, "#")
	var rgb [3]float64
	for i := range rgb {
		v, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			panic(fmt.Sprintf("invalid test color %q: %v", hex, err))
		}
		rgb[i] = float64(v) / 255
	}
	return rgb
}

func relativeLuminance(rgb [3]float64) float64 {
	for i, v := range rgb {
		if v <= 0.04045 {
			rgb[i] = v / 12.92
		} else {
			rgb[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2]
}
