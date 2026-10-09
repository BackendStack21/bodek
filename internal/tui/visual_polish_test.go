package tui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Footer hints join through one separator: an idle mode name never trails a
// dangling "·", and no hint segment bakes its own separator into the text.
func TestFooterHasNoDanglingSeparators(t *testing.T) {
	cases := map[string]func(m *Model){
		"composer": func(*Model) {},
		"memory":   func(m *Model) { m.panel = panelMemory },
		"queue":    func(m *Model) { m.panel = panelQueue },
		"runs":     func(m *Model) { m.panel = panelRuns },
		"skills":   func(m *Model) { m.panel = panelSkills; m.panelDetail = true },
		"busy":     func(m *Model) { busyTurn(m) },
		"expanded": func(m *Model) { m.expandAll = true },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			m := newTestModel()
			m.resize(200, 30)
			setup(m)
			got := strings.TrimRight(plain(m.footer()), " ")
			if strings.HasSuffix(got, "·") {
				t.Errorf("footer ends with a separator: %q", got)
			}
			if strings.Contains(got, "·  ·") || strings.Contains(got, "· ·") {
				t.Errorf("footer has an empty segment: %q", got)
			}
		})
	}
}

// The slash popup names its own mode; "attach" belongs to @-references.
func TestFooterNamesSlashPopupAsCommands(t *testing.T) {
	m := newTestModel()
	m.ac.open, m.ac.mode = true, acCmd
	if got := m.modeName(); got != "commands" {
		t.Fatalf("modeName = %q, want commands", got)
	}
	m.ac.mode = acRef
	if got := m.modeName(); got != "attach" {
		t.Fatalf("modeName = %q, want attach", got)
	}
}

// A reply painted before its first render flush starts in the column the
// glamour-rendered card will use, so finalizing a turn never shifts text.
func TestReplyColumnStableAcrossRenderFlush(t *testing.T) {
	col := func(view string) int {
		for _, ln := range strings.Split(plain(view), "\n") {
			if i := strings.Index(ln, "cart survives"); i >= 0 {
				return i
			}
		}
		return -1
	}
	m := newTestModel()
	m.resize(100, 30)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "token", Content: "The cart survives payment failure."})
	m.refresh()
	live := col(m.View())
	m.handleEvent(client.Event{Type: "done"})
	m.refresh()
	done := col(m.View())
	if live < 0 || live != done {
		t.Fatalf("reply column live=%d done=%d, want equal", live, done)
	}
}

// The header rule is one hairline span, not a per-cell brand gradient.
func TestHeaderRuleIsSingleHairline(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	m := newTestModel()
	m.resize(120, 30)
	r := m.rule()
	if got := strings.Count(r, "\x1b["); got > 2 {
		t.Fatalf("rule carries %d SGR sequences, want one span", got)
	}
	if plain(r) != strings.Repeat("─", 120) {
		t.Fatalf("rule = %q, want a full-width hairline", plain(r))
	}
}

// Warning color must not share the brand accent's hue, or a warning reads as
// ordinary chrome (the approval border looked like branding).
func TestWarningHueDistinctFromAccent(t *testing.T) {
	hue := func(c lipgloss.Color) float64 {
		var r, g, b int
		if _, err := fmt.Sscanf(string(c), "#%02x%02x%02x", &r, &g, &b); err != nil {
			t.Fatalf("bad color %q", c)
		}
		R, G, B := float64(r)/255, float64(g)/255, float64(b)/255
		mx, mn := math.Max(R, math.Max(G, B)), math.Min(R, math.Min(G, B))
		d := mx - mn
		if d == 0 {
			return 0
		}
		var h float64
		switch mx {
		case R:
			h = math.Mod((G-B)/d, 6)
		case G:
			h = (B-R)/d + 2
		default:
			h = (R-G)/d + 4
		}
		return math.Mod(h*60+360, 360)
	}
	for _, name := range []string{"ember-dark", "ember-light", "high-contrast", "classic"} {
		p := paletteByName(name)
		d := math.Abs(hue(p.yellow) - hue(p.accent))
		if d > 180 {
			d = 360 - d
		}
		if d < 10 {
			t.Errorf("%s: warning hue %.0f° within %.0f° of accent", name, hue(p.yellow), d)
		}
	}
}

// A step head carries one status-bearing glyph after the disclosure chevron:
// the tool icon on success, ✗ on failure, a static ▸ while live.
func TestStepHeadSingleStatusGlyph(t *testing.T) {
	head := func(s step) string {
		m := newTestModel()
		m.resize(100, 30)
		out, _, _ := m.renderStep(s, !s.done, -1, -1, 0)
		return plain(strings.Split(out, "\n")[0])
	}
	shell := toolGlyph("shell")
	ok := head(step{name: "shell", arg: "go test", done: true, result: "ok"})
	if strings.Contains(ok, "✓") || !strings.Contains(ok, shell+" shell") {
		t.Errorf("done head = %q, want the tool icon without a ✓", ok)
	}
	bad := head(step{name: "shell", arg: "go test", done: true, isErr: true, result: "boom"})
	if !strings.Contains(bad, "✗ shell") || strings.Contains(bad, shell) {
		t.Errorf("failed head = %q, want ✗ in place of the tool icon", bad)
	}
	live := head(step{name: "shell", arg: "go test"})
	if !strings.Contains(live, "▸") || strings.Contains(live, shell) {
		t.Errorf("live head = %q, want a static ▸ in place of the tool icon", live)
	}
}

// The sandbox chip never borrows the connection lamp's dot vocabulary.
func TestSandboxBadgeIsNotADot(t *testing.T) {
	m := newTestModel()
	m.sandbox = true
	got := plain(m.sandboxBadge())
	for _, dot := range []string{lampReady, lampLive, lampReconnect, lampDown} {
		if strings.Contains(got, dot) {
			t.Fatalf("sandbox badge %q reuses lamp glyph %q", got, dot)
		}
	}
}

// A high-risk approval escalates its border to the danger color; other
// risks keep the warning border.
func TestHighRiskApprovalBorderIsRed(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	border := func(risk string) string {
		m := newTestModel()
		m.th = themeFrom(emberDark)
		m.resize(100, 30)
		busyTurn(m)
		m.handleEvent(client.Event{Type: "approval_request", ID: "a", Risk: risk, Command: "make"})
		return strings.Split(m.approvalPanel(), "\n")[0]
	}
	red := surfaceSGR(lipgloss.NewStyle().Background(emberDark.red))
	red = strings.Replace(red, "[48;", "[38;", 1)
	if !strings.Contains(border("high"), red) {
		t.Errorf("high-risk border is not red: %q", border("high"))
	}
	if strings.Contains(border("shell_exec"), red) {
		t.Errorf("shell_exec border escalated to red: %q", border("shell_exec"))
	}
}

// Help rows keep a usable description column on very narrow cards.
func TestHelpRowLinesNarrowFloor(t *testing.T) {
	m := newTestModel()
	lines := helpRowLines(m.th, [2]string{"^K", "command palette with a long description"}, 10)
	if len(lines) < 2 {
		t.Fatalf("narrow help row did not wrap: %q", lines)
	}
}

// The cockpit's session block trails the start time when it is known.
func TestCockpitSessionStartLine(t *testing.T) {
	m := newTestModel()
	m.resize(100, 34)
	m.odekVersion = "v2.33.3" // a row, so the block renders past its empty state
	m.sessionStart = time.Now().Add(-3 * time.Minute)
	if got := plain(m.sessionBlock(true, 12)); !strings.Contains(got, "started ") {
		t.Fatalf("cockpit session block lacks start time:\n%s", got)
	}
}
