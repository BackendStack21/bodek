package tui

import (
	"fmt"
	"time"

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
	if a := m.curApproval(); a != nil && a.AllowTrust && !a.Friction {
		opts = append(opts, approvalOption{"always allow", "trust"})
	}
	return opts
}

// handleApprovalKey keeps an approval card from hijacking the composer. Only
// explicit Alt chords decide; ordinary text, paste, cursor movement, and
// Enter continue to operate on the draft underneath the card.
//
// Friction mode adds a deliberate confirmation editor. Alt+A activates it,
// then the literal word "approve" followed by Enter forwards approval.
// Before activation ordinary text still goes to the composer. Alt+D always
// denies; friction never offers trust.
func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := m.curApproval()
	if a == nil {
		return m, nil
	}
	if m.apprExpanded {
		page := max(1, (m.height-12)/2)
		switch {
		case msg.Type == tea.KeyPgUp && msg.Alt:
			m.apprOffset -= page
			m.relayout()
			return m, nil
		case msg.Type == tea.KeyPgDown && msg.Alt:
			m.apprOffset += page
			m.relayout()
			return m, nil
		}
	}
	if a.Friction {
		return m.handleFrictionKey(msg)
	}
	// Plain-letter shortcuts: terminals that cannot deliver Alt chords
	// (macOS Option-as-UTF-8) still let a bare a/d/t decide — but only on
	// an empty composer and never from a paste, so typing always wins.
	if action, ok := m.plainApprovalAction(msg); ok {
		if action == "trust" && !a.AllowTrust {
			return m, nil
		}
		return m, m.answer(action)
	}
	if approvalAltRune(msg, 'a') {
		return m, m.answer("approve")
	}
	if approvalAltRune(msg, 'd') {
		return m, m.answer("deny")
	}
	if approvalAltRune(msg, 't') {
		if a.AllowTrust {
			return m, m.answer("trust")
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		if m.apprExpanded {
			m.apprExpanded = false
			m.relayout()
			return m, nil
		}
		if m.busy {
			return m, m.armConfirm(confirmCancel, "the running turn")
		}
		return m, nil
	case "tab":
		m.apprExpanded = !m.apprExpanded
		m.relayout() // the panel grows/shrinks with the full text
		return m, nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case "ctrl+g":
		m.vp.GotoBottom()
		return m, nil
	case "ctrl+c":
		return m, m.armConfirm(confirmQuit, "bodek")
	case "enter":
		return m, m.submit()
	case "shift+enter", "alt+enter", "ctrl+j":
		return m, m.insertNewline()
	}
	return m, m.updateApprovalComposer(msg)
}

// frictionWord is the literal confirmation the friction gate demands.
const frictionWord = "approve"

// handleFrictionKey either keeps the normal composer active or, after Alt+A,
// edits the literal confirmation. Enter approves only on an exact match. Esc
// leaves confirmation editing without deciding; Alt+D remains an immediate
// denial in either state.
func (m *Model) handleFrictionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if approvalAltRune(msg, 'd') {
		return m, m.answer("deny")
	}
	if !m.apprEditing {
		if approvalAltRune(msg, 'a') {
			m.apprEditing = true
			m.apprTyped = ""
			m.relayout()
			m.refresh()
			return m, nil
		}
		if approvalAltRune(msg, 't') {
			return m, nil
		}
		return m.handleApprovalComposerKey(msg)
	}
	switch msg.String() {
	case "enter":
		if m.apprTyped == frictionWord {
			return m, m.answer("approve")
		}
		m.apprTyped = ""
	case "shift+enter", "ctrl+enter":
		// Synthesized newline chords must not splice their sentinel text
		// into the confirmation buffer (a newline can never match the word).
		return m, nil
	case "esc":
		m.apprEditing = false
		m.apprTyped = ""
		m.relayout()
		m.refresh()
		return m, nil
	case "backspace":
		if runes := []rune(m.apprTyped); len(runes) > 0 {
			m.apprTyped = string(runes[:len(runes)-1])
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
		return m, m.armConfirm(confirmQuit, "bodek")
	default:
		// Single printable runes only — modifiers (ctrl+X, alt+X) must not
		// splice escape bytes into the confirmation buffer.
		if msg.Type == tea.KeyRunes && !msg.Alt && len(msg.Runes) > 0 {
			runes := append([]rune(m.apprTyped), msg.Runes...)
			m.apprTyped = string(runes[:min(len(runes), 64)])
		}
	}
	m.refresh()
	return m, nil
}

// handleApprovalComposerKey applies the shared approval-mode controls that
// must remain available while the card is waiting: scrolling, quit, and the
// ordinary Esc dismissal/cancel ladder.
func (m *Model) handleApprovalComposerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.apprExpanded {
			m.apprExpanded = false
			m.relayout()
			return m, nil
		}
		if m.busy {
			return m, m.armConfirm(confirmCancel, "the running turn")
		}
		return m, nil
	case "tab":
		m.apprExpanded = !m.apprExpanded
		m.relayout()
		return m, nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case "ctrl+g":
		m.vp.GotoBottom()
		return m, nil
	case "ctrl+c":
		return m, m.armConfirm(confirmQuit, "bodek")
	case "enter":
		return m, m.submit()
	case "shift+enter", "ctrl+enter", "alt+enter", "ctrl+j":
		return m, m.insertNewline()
	default:
		return m, m.updateApprovalComposer(msg)
	}
}

// updateApprovalComposer forwards all ordinary approval-mode input to the
// textarea. This intentionally includes paste and cursor keys.
func (m *Model) updateApprovalComposer(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	m.syncComposer()
	return cmd
}

func approvalAltRune(msg tea.KeyMsg, want rune) bool {
	if msg.Type != tea.KeyRunes || !msg.Alt || len(msg.Runes) != 1 {
		return false
	}
	return lowerRune(msg.Runes[0]) == want
}

// plainApprovalAction maps a bare a/d/t KeyMsg to its decision while the
// composer draft is empty. Modified or pasted runes never qualify — a
// non-empty draft must keep routing the letters to the composer.
func (m *Model) plainApprovalAction(msg tea.KeyMsg) (string, bool) {
	if msg.Type != tea.KeyRunes || msg.Alt || msg.Paste || len(msg.Runes) != 1 {
		return "", false
	}
	if m.ta.Value() != "" {
		return "", false
	}
	switch lowerRune(msg.Runes[0]) {
	case 'a':
		return "approve", true
	case 'd':
		return "deny", true
	case 't':
		return "trust", true
	}
	return "", false
}

func lowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 'a' - 'A'
	}
	return r
}

// frictionHint renders the friction line: the recent-approval count the
// server insists the user sees, plus the typed confirmation buffer.
func (m *Model) frictionHint() string {
	a := m.curApproval()
	if a == nil {
		return ""
	}
	n := a.FrictionApprovals
	if n < 1 {
		n = 1 // gate engaged but the count was omitted — don't print "0"
	}
	typed := truncate(collapse(sanitize(m.apprTyped)), 24)
	if typed == "" {
		typed = "…"
	}
	if !m.apprEditing {
		return m.th.noticeStyle.Render(fmt.Sprintf(
			"⏳ friction: %d approvals in the last 60s — Alt+A to confirm · Alt+D denies",
			n))
	}
	return m.th.noticeStyle.Render(fmt.Sprintf(
		"⏳ friction: %d approvals in the last 60s — type %q + ⏎ · esc returns to draft   %s",
		n, frictionWord, typed))
}

// resetApprovalInput clears the selection/typed-confirmation state when a
// new approval_request becomes the queue head or one is answered.
func (m *Model) resetApprovalInput() {
	m.apprSel = 0
	m.apprExpanded = false
	m.apprTyped = ""
	m.apprEditing = false
	m.apprOffset = 0
}

// answer sends the decision for the queue head and reopens the run. The
// approval_ack the server sends in reply needs no further UI — the panel is
// already on the next request (or gone).
func (m *Model) answer(action string) tea.Cmd {
	a := m.curApproval()
	if a == nil {
		return nil
	}
	id := a.ID
	head := *a
	var dl time.Time
	if len(m.apprDeadlines) > 0 {
		dl = m.apprDeadlines[0]
		m.apprDeadlines = m.apprDeadlines[1:] // keep the parallel expiry queue in lockstep
	}
	m.approvals = m.approvals[1:]
	m.resetApprovalInput()
	if len(m.approvals) > 0 {
		m.setRunStatus("approval required")
	} else {
		m.setRunStatus("thinking")
	}
	m.relayout()
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		if err := cl.SendApproval(id, action); err != nil {
			return approvalSendErrMsg{ev: head, dl: dl, err: err}
		}
		return nil
	}
}
