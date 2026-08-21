package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── design tokens ──────────────────────────────────────────────────────────
//
// bodek speaks EMBER — the same design language as odek's WebUI — mapped to
// the terminal: electric amber over blue-charcoal, hairline structure, and
// semantic color reserved for meaning (risk, failure, budget pressure).
// "steel" is EMBER's cool blue-grey (the --line hue family) and plays the
// machine voice: tool names, keys, metrics.

// palette is one named theme's color set. Themes differ only in these values;
// every style is derived mechanically, so adding a theme is a struct literal.
type palette struct {
	accent   lipgloss.Color // primary brand — amber
	accentHi lipgloss.Color // gradient top / highlights
	accentLo lipgloss.Color // gradient bottom / user-turn warmth
	steel    lipgloss.Color // machine elements (tool names, keys, metrics)

	green  lipgloss.Color
	yellow lipgloss.Color
	red    lipgloss.Color

	text     lipgloss.Color // answers, values — the only full-brightness body
	muted    lipgloss.Color // labels, secondary text
	faint    lipgloss.Color // chrome only — too dim for body text
	bodyText lipgloss.Color // machinery output (tool results, reasoning)
	hairline lipgloss.Color // rules, borders, tree glyphs

	grad [2][3]int // banner gradient endpoints (hi → lo)
}

var (
	// emberDark is the default: EMBER's dark tokens on a transparent
	// terminal background.
	emberDark = palette{
		accent:   "#FFB224",
		accentHi: "#FFC95E",
		accentLo: "#FF8A3D",
		steel:    "#98AAC8",
		green:    "#34D399",
		yellow:   "#FBBF24",
		red:      "#F87171",
		text:     "#E7E9EE",
		muted:    "#9AA0AE",
		faint:    "#6B7280",
		bodyText: "#8B95A8",
		hairline: "#2E3242",
		grad:     [2][3]int{{0xFF, 0xC9, 0x5E}, {0xFF, 0x8A, 0x3D}},
	}

	// emberLight mirrors the WebUI light theme (parchment base, deeper amber
	// for contrast on light terminals).
	emberLight = palette{
		accent:   "#D98E00",
		accentHi: "#B47300",
		accentLo: "#E07000",
		steel:    "#6B7A99",
		green:    "#0E9F6E",
		yellow:   "#B45309",
		red:      "#C22B2B",
		text:     "#22252C",
		muted:    "#5A5F6D",
		faint:    "#8A8578",
		bodyText: "#4A4E59",
		hairline: "#CFC9BD",
		grad:     [2][3]int{{0xB4, 0x73, 0x00}, {0xE0, 0x70, 0x00}},
	}

	// emberHighContrast is pure neutrals plus amber, for low-vision and
	// washed-out displays.
	emberHighContrast = palette{
		accent:   "#FFD24A",
		accentHi: "#FFE08A",
		accentLo: "#FFB224",
		steel:    "#B8C4E0",
		green:    "#4ADE80",
		yellow:  "#FBBF24",
		red:      "#FF6B6B",
		text:     "#FFFFFF",
		muted:    "#C0C0C0",
		faint:    "#909090",
		bodyText: "#E0E0E0",
		hairline: "#707070",
		grad:     [2][3]int{{0xFF, 0xE0, 0x8A}, {0xFF, 0xB2, 0x24}},
	}

	// classic is the pre-EMBER Charm-inspired palette, kept for muscle memory
	// (BODEK_THEME=classic).
	classic = palette{
		accent:   "#A78BFA",
		accentHi: "#A78BFA",
		accentLo: "#F472B6",
		steel:    "#67E8F9",
		green:    "#34D399",
		yellow:   "#FBBF24",
		red:      "#F87171",
		text:     "#E5E7EB",
		muted:    "#9CA3AF",
		faint:    "#6B7280",
		bodyText: "#8B93A3",
		hairline: "#3B3B4F",
		grad:     [2][3]int{{0xA7, 0x8B, 0xFA}, {0xF4, 0x72, 0xB6}},
	}
)

// motionEnabled gates every animation (spinner, gauge flash). NO_MOTION=1
// freezes the UI to static glyphs — a calm-terminal guarantee, not a
// reduced-functionality mode.
var motionEnabled = os.Getenv("NO_MOTION") != "1"

// themeName resolves the configured palette. BODEK_THEME selects
// ember-dark (default) · ember-light · high-contrast · classic.
func themeName() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BODEK_THEME"))) {
	case "classic":
		return "classic"
	case "ember-light", "light":
		return "ember-light"
	case "high-contrast", "contrast":
		return "high-contrast"
	default:
		return "ember-dark"
	}
}

func paletteByName(name string) palette {
	switch name {
	case "classic":
		return classic
	case "ember-light":
		return emberLight
	case "high-contrast":
		return emberHighContrast
	default:
		return emberDark
	}
}

// Layout — fixed heights for the chrome around the scrollable transcript.
const (
	headerHeight = 2 // cockpit bar + hairline rule
	inputHeight  = 5 // bordered textarea (3 rows + top/bottom border)
	footerHeight = 1 // status bar
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
	sysBar    lipgloss.Style

	stepName lipgloss.Style
	stepArg  lipgloss.Style
	stepRun  lipgloss.Style
	stepDone lipgloss.Style
	stepErr  lipgloss.Style
	stepRes  lipgloss.Style
	stepTree lipgloss.Style

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

	// header/status badges (sandbox state, connectivity)
	badgeOK     lipgloss.Style
	badgeWarn   lipgloss.Style
	badgeDanger lipgloss.Style

	toolIcon lipgloss.Style
	scroll   lipgloss.Style

	// per-turn stat line + session dashboard + context gauge
	statLine  lipgloss.Style
	statSep   lipgloss.Style
	statTime  lipgloss.Style
	statCtx   lipgloss.Style
	statTool  lipgloss.Style
	statThink lipgloss.Style
	statGlyph lipgloss.Style

	gaugeOK   lipgloss.Style
	gaugeWarn lipgloss.Style
	gaugeHot  lipgloss.Style

	statsLabel lipgloss.Style
	statsValue lipgloss.Style
	statsDim   lipgloss.Style

	opChip       lipgloss.Style
	untrustedTag lipgloss.Style

	footer       lipgloss.Style
	footerKey    lipgloss.Style
	footerSep    lipgloss.Style
	footerDanger lipgloss.Style

	tagline lipgloss.Style
	tipKey  lipgloss.Style
	tipText lipgloss.Style

	acBox    lipgloss.Style
	acTitle  lipgloss.Style
	acItem   lipgloss.Style
	acSel    lipgloss.Style
	acDim    lipgloss.Style
	acDetail lipgloss.Style
	acIcon   lipgloss.Style

	// typed renderer tints (+/- diff lines)
	diffAdd lipgloss.Style
	diffDel lipgloss.Style

	grad [2][3]int // banner gradient endpoints for this theme
}

// newTheme builds the configured theme.
func newTheme() theme {
	return themeFrom(paletteByName(themeName()))
}

// themeFrom derives every style from one palette — the single mapping from
// EMBER tokens to component styles.
func themeFrom(p palette) theme {
	return theme{
		grad: p.grad,

		logo:       lipgloss.NewStyle().Bold(true).Foreground(p.accent),
		headerMeta: lipgloss.NewStyle().Foreground(p.muted),
		headerKey:  lipgloss.NewStyle().Foreground(p.steel),
		rule:       lipgloss.NewStyle().Foreground(p.hairline),

		// The user speaks in amber-warm; odek answers in neutral bright —
		// the accent belongs to the human, the answer owns the brightness.
		userLabel: lipgloss.NewStyle().Foreground(p.accentLo).Bold(true),
		userBar:   lipgloss.NewStyle().Foreground(p.text).Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(p.accentLo).PaddingLeft(1),
		asstLabel: lipgloss.NewStyle().Foreground(p.muted).Bold(true),
		asstBar:   lipgloss.NewStyle().Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(p.hairline).PaddingLeft(1),
		sysBar:    lipgloss.NewStyle().Foreground(p.red).Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(p.red).PaddingLeft(1),

		stepName: lipgloss.NewStyle().Foreground(p.steel),
		stepArg:  lipgloss.NewStyle().Foreground(p.faint),
		stepRun:  lipgloss.NewStyle().Foreground(p.yellow),
		stepDone: lipgloss.NewStyle().Foreground(p.green),
		stepErr:  lipgloss.NewStyle().Foreground(p.red).Bold(true),
		stepRes:  lipgloss.NewStyle().Foreground(p.bodyText),
		stepTree: lipgloss.NewStyle().Foreground(p.hairline),

		spinner: lipgloss.NewStyle().Foreground(p.accent),

		taCursorLine: lipgloss.NewStyle(),
		inputBox:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.hairline).Padding(0, 1),

		noticeStyle: lipgloss.NewStyle().Foreground(p.bodyText).Italic(true),
		thinkStyle:  lipgloss.NewStyle().Foreground(p.bodyText).Italic(true),

		apprBox:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.yellow).Padding(0, 1),
		apprHead: lipgloss.NewStyle().Foreground(p.yellow).Bold(true),
		apprBody: lipgloss.NewStyle().Foreground(p.text),
		apprKey:  lipgloss.NewStyle().Foreground(p.green).Bold(true),

		statusReady: lipgloss.NewStyle().Foreground(p.green),
		statusBusy:  lipgloss.NewStyle().Foreground(p.yellow),

		badgeOK:     lipgloss.NewStyle().Foreground(p.green),
		badgeWarn:   lipgloss.NewStyle().Foreground(p.yellow),
		badgeDanger: lipgloss.NewStyle().Foreground(p.red),

		toolIcon: lipgloss.NewStyle().Foreground(p.steel),
		scroll:   lipgloss.NewStyle().Foreground(p.faint),

		// Per-turn stat line: glyphs carry the hue, numbers recede in faint so
		// the row sits quietly beneath the prose.
		statLine:  lipgloss.NewStyle().Foreground(p.faint),
		statSep:   lipgloss.NewStyle().Foreground(p.hairline),
		statTime:  lipgloss.NewStyle().Foreground(p.accent),
		statCtx:   lipgloss.NewStyle().Foreground(p.steel),
		statTool:  lipgloss.NewStyle().Foreground(p.steel),
		statThink: lipgloss.NewStyle().Foreground(p.accent),
		statGlyph: lipgloss.NewStyle().Foreground(p.faint),

		gaugeOK:   lipgloss.NewStyle().Foreground(p.green),
		gaugeWarn: lipgloss.NewStyle().Foreground(p.yellow),
		gaugeHot:  lipgloss.NewStyle().Foreground(p.red),

		statsLabel: lipgloss.NewStyle().Foreground(p.muted),
		statsValue: lipgloss.NewStyle().Foreground(p.text),
		statsDim:   lipgloss.NewStyle().Foreground(p.faint).Italic(true),

		opChip:       lipgloss.NewStyle().Foreground(p.steel),
		untrustedTag: lipgloss.NewStyle().Foreground(p.yellow),

		footer:       lipgloss.NewStyle().Foreground(p.faint),
		footerKey:    lipgloss.NewStyle().Foreground(p.muted).Bold(true),
		footerSep:    lipgloss.NewStyle().Foreground(p.hairline),
		footerDanger: lipgloss.NewStyle().Foreground(p.red),

		tagline: lipgloss.NewStyle().Foreground(p.muted).Italic(true),
		tipKey:  lipgloss.NewStyle().Foreground(p.steel).Bold(true),
		tipText: lipgloss.NewStyle().Foreground(p.muted),

		acBox:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.accent).Padding(0, 1),
		acTitle:  lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		acItem:   lipgloss.NewStyle().Foreground(p.text),
		acSel:    lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		acDim:    lipgloss.NewStyle().Foreground(p.faint).Italic(true),
		acDetail: lipgloss.NewStyle().Foreground(p.faint),
		acIcon:   lipgloss.NewStyle().Foreground(p.steel),

		diffAdd: lipgloss.NewStyle().Foreground(p.green),
		diffDel: lipgloss.NewStyle().Foreground(p.red),
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
