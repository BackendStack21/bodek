package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// approvalOption is one selectable outcome in the approval panel; action is
// the protocol reply sent back to the server.
type approvalOption struct {
	label  string
	action string
}

// approvalOptions lists the panel's outcomes in display order. Trust is only
// offered when the server allows it for the risk class, and is withdrawn in
// friction mode — a burst of same-class approvals must not widen into a
// class-trust shortcut (mirrors the TTY approver policy).
func (m *Model) approvalOptions() []approvalOption {
	opts := []approvalOption{
		{"approve", "approve"},
		{"deny", "deny"},
	}
	if m.approval.AllowTrust && !m.approval.Friction {
		opts = append(opts, approvalOption{"trust class", "trust"})
	}
	return opts
}

// handleApprovalKey drives the pending approval: arrows move the highlight,
// enter confirms it, esc denies, tab expands the full command/description,
// and the transcript scroll keys keep working. Bare letters never decide — a
// prompt typed mid-approval must not leak into a decision.
//
// Friction mode (server flag: 3+ same-class approvals inside 60s) replaces
// the selection UI entirely: the literal word "approve" must be typed and
// confirmed with enter before an approval is forwarded — no highlight
// shortcut. Denial stays one keypress (esc): friction slows approving, not
// refusing.
func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.approval.Friction {
		return m.handleFrictionKey(msg)
	}
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

// frictionWord is the literal confirmation the friction gate demands.
const frictionWord = "approve"

// handleFrictionKey edits the typed confirmation. Enter approves only on an
// exact match (a mismatch resets the buffer — retyping is the point); esc
// still denies; tab still expands; scrolling still works.
func (m *Model) handleFrictionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.apprTyped == frictionWord {
			return m, m.answer("approve")
		}
		m.apprTyped = ""
	case "esc":
		return m, m.answer("deny")
	case "backspace":
		if n := len(m.apprTyped); n > 0 {
			m.apprTyped = m.apprTyped[:n-1]
		}
	case "tab":
		m.apprExpanded = !m.apprExpanded
		m.relayout()
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
	default:
		// Single printable runes only — modifiers (ctrl+X, alt+X) must not
		// splice escape bytes into the confirmation buffer.
		if s := msg.String(); len([]rune(s)) == 1 {
			m.apprTyped += s
		}
	}
	m.refresh()
	return m, nil
}

// frictionHint renders the friction line: the recent-approval count the
// server insists the user sees, plus the typed confirmation buffer.
func (m *Model) frictionHint() string {
	n := m.approval.FrictionApprovals
	if n < 1 {
		n = 1 // gate engaged but the count was omitted — don't print "0"
	}
	typed := m.apprTyped
	if typed == "" {
		typed = "…"
	}
	return m.th.noticeStyle.Render(fmt.Sprintf(
		"⏳ friction: %d approvals in the last 60s — type %q + ⏎ (esc denies)   %s",
		n, frictionWord, typed))
}

// resetApprovalInput clears the selection/typed-confirmation state when a
// new approval_request arrives or one is answered.
func (m *Model) resetApprovalInput() {
	m.apprSel = 0
	m.apprExpanded = false
	m.apprTyped = ""
}

// answer sends the decision and reopens the run. The approval_ack the server
// sends in reply needs no further UI — the panel is gone by then.
func (m *Model) answer(action string) tea.Cmd {
	id := m.approval.ID
	m.approval = nil
	m.resetApprovalInput()
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
