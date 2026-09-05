package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ── verbosity dial ─────────────────────────────────────────────────────────
//
// One dial for the noise policy instead of five separate toggles. Quiet
// hides info-tier traces (the strip keeps errors, warnings, hints, and the
// dial's own ack) and forces compact steps; detailed implies the ^E
// expand-all view; normal is exactly the historical default. The dial rides
// /verbosity and the palette — no new chords, the hint layer teaches it.

// Dial states (Model.verbosity). Normal is the zero value so an
// unconfigured Model defaults to today's behavior.
const (
	verbosityNormal   = 0 // today's default behavior
	verbosityQuiet    = 1 // info notes hidden, steps compact
	verbosityDetailed = 2 // steps expand (the ^E view)
)

// verbosityFrom resolves a startup/argument name to a dial state. Unknown
// or empty keeps the default: normal.
func verbosityFrom(name string) int {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "quiet":
		return verbosityQuiet
	case "detailed":
		return verbosityDetailed
	default:
		return verbosityNormal
	}
}

func verbosityName(v int) string {
	switch v {
	case verbosityQuiet:
		return "quiet"
	case verbosityDetailed:
		return "detailed"
	default:
		return "normal"
	}
}

// dialNote pushes a note on the operator-feedback path (always visible,
// every dial state — the dial's own ack must survive quiet) and returns
// the sweep that fades it.
func (m *Model) dialNote(s string) tea.Cmd {
	return m.transientNoteCmd(s)
}

// setVerbosity applies the dial and acknowledges the new state.
func (m *Model) setVerbosity(v int) tea.Cmd {
	m.verbosity = v
	m.expandAll = v == verbosityDetailed
	m.invalidateAllMsgBlocks()
	for i := range m.msgs {
		for j := range m.msgs[i].steps {
			clearStepBlockCache(&m.msgs[i].steps[j])
		}
	}
	m.refresh()
	var desc string
	switch v {
	case verbosityQuiet:
		desc = "engine traces hidden · steps compact"
	case verbosityDetailed:
		desc = "steps expand · engine traces shown"
	default:
		desc = "engine traces shown · steps compact"
	}
	return m.dialNote("verbosity: " + verbosityName(v) + " — " + desc)
}

// cycleVerbosity advances normal → quiet → detailed → normal: from the
// default, the first turn of the dial goes calmer.
func (m *Model) cycleVerbosity() tea.Cmd {
	order := []int{verbosityNormal, verbosityQuiet, verbosityDetailed}
	for i, v := range order {
		if v == m.verbosity {
			return m.setVerbosity(order[(i+1)%len(order)])
		}
	}
	return m.setVerbosity(verbosityQuiet) // unreachable; quiet is the safe landing
}
