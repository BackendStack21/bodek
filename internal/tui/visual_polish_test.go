package tui

import (
	"strings"
	"testing"

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
