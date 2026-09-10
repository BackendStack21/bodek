package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/charmbracelet/lipgloss"
)

func TestThemePalettePreselectsCurrentThemeAndFitsSmallTerminal(t *testing.T) {
	old := themeOverride
	t.Cleanup(func() { themeOverride = old })
	themeOverride = "ember-light"

	m := newTestModel()
	m.resize(40, 16)
	m.openThemePalette()

	if !m.pal.open || m.pal.mode != palModeThemes {
		t.Fatalf("theme selector state = %+v", m.pal)
	}
	if got := m.pal.items[m.pal.sel].title; got != "ember-light" {
		t.Fatalf("selected theme = %q, want ember-light", got)
	}
	out := plain(m.palPopup())
	if !strings.Contains(out, "⌘ themes") || !strings.Contains(out, "✓") {
		t.Fatalf("theme selector missing title/current marker:\n%s", out)
	}
	if lipgloss.Width(out) > 40 {
		t.Fatalf("theme selector overflows width: %d", lipgloss.Width(out))
	}
	if m.palHeight() > 16 {
		t.Fatalf("theme selector overflows height: %d", m.palHeight())
	}
	view := plain(m.View())
	lines := strings.Split(view, "\n")
	if len(lines) > 16 {
		t.Fatalf("theme selector view overflows height: %d rows", len(lines))
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > 40 {
			t.Fatalf("theme selector view line %d overflows width: %d", i, got)
		}
	}

	m.busy = true
	m.status = "working"
	m.openThemePalette()
	m.pal.query = "light"
	m.filterPalette()
	if out := plain(m.palPopup()); !strings.Contains(out, "light") {
		t.Fatalf("theme selector query is not visible:\n%s", out)
	}
	view = plain(m.View())
	lines = strings.Split(view, "\n")
	if len(lines) > 16 {
		t.Fatalf("busy theme selector view overflows height: %d rows", len(lines))
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > 40 {
			t.Fatalf("busy theme selector view line %d overflows width: %d", i, got)
		}
	}
}

func TestThemePaletteSelectionPersistsAndEscapeLeavesThemeUnchanged(t *testing.T) {
	old := themeOverride
	t.Cleanup(func() { themeOverride = old })
	themeOverride = "ember-dark"

	m := newTestModel()
	m.opts.OnThemeChange = func(name string) error {
		if name != "ember-light" {
			t.Errorf("persisted theme = %q, want ember-light", name)
		}
		return nil
	}
	m.openThemePalette()
	m.Update(key("down"))
	_, cmd := m.Update(key("enter"))
	exec(cmd)
	if themeOverride != "ember-light" {
		t.Fatalf("themeOverride = %q, want ember-light", themeOverride)
	}
	if m.pal.open {
		t.Fatal("theme selector remained open after selection")
	}

	themeOverride = "ember-dark"
	m.openThemePalette()
	m.Update(key("esc"))
	if m.pal.open {
		t.Fatal("escape did not close theme selector")
	}
	if themeOverride != "ember-dark" {
		t.Fatalf("escape changed theme to %q", themeOverride)
	}
}

func TestThemeCommandOpensSelectorAndLateSessionsAreIgnored(t *testing.T) {
	old := themeOverride
	t.Cleanup(func() { themeOverride = old })
	themeOverride = "ember-dark"

	m := newTestModel()
	m.togglePalette()
	if m.pal.mode != palModeCommands {
		t.Fatalf("command palette mode = %d", m.pal.mode)
	}
	cmd := runTheme(m, "")
	if cmd != nil {
		t.Fatal("opening theme selector returned an unexpected command")
	}
	if !m.pal.open || m.pal.mode != palModeThemes {
		t.Fatal("/theme did not open the theme selector")
	}
	before := len(m.pal.all)
	m.handlePalSessions(palSessionsMsg{items: []client.Session{{ID: "late", Task: "late session"}}})
	if len(m.pal.all) != before {
		t.Fatal("late session result appended to theme selector")
	}

	m.pal.open = false
	m.togglePalette()
	cmd = m.runCommand("theme", "")
	if cmd != nil || !m.pal.open || m.pal.mode != palModeThemes {
		t.Fatal("command palette dispatch did not reopen theme selector")
	}
}
