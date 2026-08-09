package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// approvalOption is one selectable outcome in the approval panel; action is
// the protocol reply sent back to the server.
type approvalOption struct {
	label  string
	action string
}

// approvalOptions lists the panel's outcomes in display order; trust is only
// offered when the server allows it.
func (m *Model) approvalOptions() []approvalOption {
	opts := []approvalOption{
		{"approve", "approve"},
		{"deny", "deny"},
	}
	if m.approval.AllowTrust {
		opts = append(opts, approvalOption{"trust class", "trust"})
	}
	return opts
}

// handleApprovalKey drives the pending approval: arrows move the highlight,
// enter confirms it, esc denies, tab expands the full command/description,
// and the transcript scroll keys keep working. Bare letters never decide — a
// prompt typed mid-approval must not leak into a decision.
func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "left":
		if m.apprSel > 0 {
			m.apprSel--
		}
	case "down", "right":
		if m.apprSel < len(m.approvalOptions())-1 {
			m.apprSel++
		}
	case "enter":
		return m, m.answer(m.approvalOptions()[m.apprSel].action)
	case "esc":
		return m, m.answer("deny")
	case "tab":
		m.apprExpanded = !m.apprExpanded
		m.relayout() // the panel grows/shrinks with the full text
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case "ctrl+g":
		m.vp.GotoBottom()
		return m, nil
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) answer(action string) tea.Cmd {
	id := m.approval.ID
	m.approval = nil
	m.apprSel = 0
	m.apprExpanded = false
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
