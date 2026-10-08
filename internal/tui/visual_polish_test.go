package tui

import (
	"strings"
	"testing"
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

