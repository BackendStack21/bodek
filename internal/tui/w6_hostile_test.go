package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// W6 — hostile terminals (contained items). Regression tests for the
// judge-5 audit E1 and the statusLine clamp.

// E1: below the old 3-row viewport floor the View was taller than the
// terminal — permanent scroll jitter in alt-screen. The View must fit at
// any height ≥ the minimum layout.
func TestViewFitsShortTerminals(t *testing.T) {
	for _, h := range []int{8, 9, 10, 12, 24} {
		m := newTestModel()
		m.width = 60
		m.height = h
		m.resize(60, h)
		m.msgs = append(m.msgs,
			message{role: roleUser, content: "q"},
			message{role: roleAsst, content: "answer"},
		)
		out := m.View()
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) > h {
			t.Errorf("height %d: View renders %d lines — taller than the terminal", h, len(lines))
		}
	}
}

// The busy status row is hard-clamped to the terminal width: a wrapped row
// costs a second line and breaks inputAreaHeight's contract.
func TestStatusLineNeverWraps(t *testing.T) {
	m := newTestModel()
	m.width = 30
	m.resize(30, 24)
	m.busy = true
	m.curIdx = -1
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.lastTool = "shell"
	m.lastArg = strings.Repeat("中", 60) // 120 columns of hostile arg
	row := m.statusLine()
	for i, line := range strings.Split(row, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("status line %d is %d cols, terminal %d", i, w, m.width)
		}
	}
}
