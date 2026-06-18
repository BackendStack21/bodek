package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette — a cohesive, Charm-inspired dark theme. Purple/pink brand accents
// over soft greys, with semantic colors for status and tool activity.
var (
	colBrand    = lipgloss.Color("#A78BFA") // primary purple
	colBrand2   = lipgloss.Color("#F472B6") // pink (gradient end / user)
	colCyan     = lipgloss.Color("#67E8F9")
	colGreen    = lipgloss.Color("#34D399")
	colYellow   = lipgloss.Color("#FBBF24")
	colRed      = lipgloss.Color("#F87171")
	colFg       = lipgloss.Color("#E5E7EB")
	colMuted    = lipgloss.Color("#9CA3AF")
	colFaint    = lipgloss.Color("#6B7280")
	colHairline = lipgloss.Color("#3B3B4F")
)

// Layout — fixed heights for the chrome around the scrollable transcript.
const (
	headerHeight = 2 // logo bar + hairline rule
	inputHeight  = 5 // bordered textarea (3 rows + top/bottom border)
	footerHeight = 1 // keybinding / status line
)

// theme holds every reusable style. Built once and shared by the model.
type theme struct {
	logo       lipgloss.Style
	headerMeta lipgloss.Style
	headerKey  lipgloss.Style
	rule       lipgloss.Style

	userLabel lipgloss.Style
	userBar   lipgloss.Style
	asstLabel lipgloss.Style
	asstBar   lipgloss.Style
	sysLabel  lipgloss.Style
	sysBar    lipgloss.Style

	stepName lipgloss.Style
	stepArg  lipgloss.Style
	stepRun  lipgloss.Style
	stepDone lipgloss.Style
	stepRes  lipgloss.Style

	spinner lipgloss.Style

	taCursorLine lipgloss.Style
	inputBox     lipgloss.Style

	noticeStyle lipgloss.Style
	thinkStyle  lipgloss.Style

	apprBox  lipgloss.Style
	apprHead lipgloss.Style
	apprBody lipgloss.Style
	apprKey  lipgloss.Style

	statusReady lipgloss.Style
	statusBusy  lipgloss.Style

	footer    lipgloss.Style
	footerKey lipgloss.Style
	footerSep lipgloss.Style

	tagline lipgloss.Style
	tipKey  lipgloss.Style
	tipText lipgloss.Style
}

func newTheme() theme {
	return theme{
		logo:       lipgloss.NewStyle().Bold(true),
		headerMeta: lipgloss.NewStyle().Foreground(colMuted),
		headerKey:  lipgloss.NewStyle().Foreground(colCyan),
		rule:       lipgloss.NewStyle().Foreground(colHairline),

		userLabel: lipgloss.NewStyle().Foreground(colBrand2).Bold(true),
		userBar:   lipgloss.NewStyle().Foreground(colFg).Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(colBrand2).PaddingLeft(1),
		asstLabel: lipgloss.NewStyle().Foreground(colBrand).Bold(true),
		asstBar:   lipgloss.NewStyle().Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(colBrand).PaddingLeft(1),
		sysLabel:  lipgloss.NewStyle().Foreground(colRed).Bold(true),
		sysBar:    lipgloss.NewStyle().Foreground(colRed).Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(colRed).PaddingLeft(1),

		stepName: lipgloss.NewStyle().Foreground(colCyan),
		stepArg:  lipgloss.NewStyle().Foreground(colFaint),
		stepRun:  lipgloss.NewStyle().Foreground(colYellow),
		stepDone: lipgloss.NewStyle().Foreground(colGreen),
		stepRes:  lipgloss.NewStyle().Foreground(colFaint).Italic(true),

		spinner: lipgloss.NewStyle().Foreground(colBrand),

		taCursorLine: lipgloss.NewStyle(),
		inputBox:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colHairline).Padding(0, 1),

		noticeStyle: lipgloss.NewStyle().Foreground(colFaint).Italic(true),
		thinkStyle:  lipgloss.NewStyle().Foreground(colFaint).Italic(true),

		apprBox:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colYellow).Padding(0, 1),
		apprHead: lipgloss.NewStyle().Foreground(colYellow).Bold(true),
		apprBody: lipgloss.NewStyle().Foreground(colFg),
		apprKey:  lipgloss.NewStyle().Foreground(colGreen).Bold(true),

		statusReady: lipgloss.NewStyle().Foreground(colGreen),
		statusBusy:  lipgloss.NewStyle().Foreground(colYellow),

		footer:    lipgloss.NewStyle().Foreground(colFaint),
		footerKey: lipgloss.NewStyle().Foreground(colMuted).Bold(true),
		footerSep: lipgloss.NewStyle().Foreground(colHairline),

		tagline: lipgloss.NewStyle().Foreground(colMuted).Italic(true),
		tipKey:  lipgloss.NewStyle().Foreground(colCyan).Bold(true),
		tipText: lipgloss.NewStyle().Foreground(colMuted),
	}
}

// gradient colors a string left-to-right by interpolating between two RGB
// endpoints — used for the banner. Whitespace is passed through untouched.
func gradient(s string, from, to [3]int) string {
	runes := []rune(s)
	n := len(runes)
	var b strings.Builder
	for i, r := range runes {
		if r == ' ' {
			b.WriteRune(r)
			continue
		}
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		cr := int(float64(from[0]) + float64(to[0]-from[0])*t)
		cg := int(float64(from[1]) + float64(to[1]-from[1])*t)
		cb := int(float64(from[2]) + float64(to[2]-from[2])*t)
		col := lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", cr, cg, cb))
		b.WriteString(lipgloss.NewStyle().Foreground(col).Render(string(r)))
	}
	return b.String()
}
