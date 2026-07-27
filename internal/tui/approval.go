package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleApprovalKey answers the pending approval (or quits); any other key
// is swallowed while the panel waits for a decision.
func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "a", "y":
		return m, m.answer("approve")
	case "d", "n":
		return m, m.answer("deny")
	case "t":
		if m.approval.AllowTrust {
			return m, m.answer("trust")
		}
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) answer(action string) tea.Cmd {
	id := m.approval.ID
	m.approval = nil
	m.status = "thinking"
	m.relayout()
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		if err := cl.SendApproval(id, action); err != nil {
			return errMsg{err}
		}
		return nil
	}
}
