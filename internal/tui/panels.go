package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// panelMode selects the full-area overlay shown in place of the transcript.
type panelMode int

const (
	panelNone panelMode = iota
	panelSessions
	panelModels
)

// ── async results ────────────────────────────────────────────────────────────

type sessionsMsg struct {
	items []client.Session
	err   error
}

type modelsMsg struct {
	items []client.ModelInfo
	err   error
}

type sessionDetailMsg struct {
	sess  client.Session
	token string
	err   error
}

type sessionDeletedMsg struct {
	id  string
	err error
}

type cancelDoneMsg struct{ err error }

// ── opening panels ───────────────────────────────────────────────────────────

func (m *Model) openSessions() tea.Cmd {
	m.panel = panelSessions
	m.panelSel = 0
	m.panelMsg = "loading sessions…"
	m.relayout()
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		items, err := cl.Sessions()
		return sessionsMsg{items: items, err: err}
	}
}

func (m *Model) openModels() tea.Cmd {
	m.panel = panelModels
	m.panelSel = 0
	m.panelMsg = "loading models…"
	m.relayout()
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		items, err := cl.Models()
		return modelsMsg{items: items, err: err}
	}
}

func (m *Model) closePanel() {
	m.panel = panelNone
	m.panelMsg = ""
	m.relayout()
	m.refresh()
}

// ── key handling ─────────────────────────────────────────────────────────────

func (m *Model) handlePanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc", "ctrl+r", "ctrl+o", "q":
		m.closePanel()
		return m, nil
	case "up", "ctrl+p", "k":
		if m.panelSel > 0 {
			m.panelSel--
			m.refresh()
		}
		return m, nil
	case "down", "ctrl+n", "j":
		if m.panelSel < m.panelLen()-1 {
			m.panelSel++
			m.refresh()
		}
		return m, nil
	case "enter":
		return m, m.panelSelect()
	case "d", "x":
		if m.panel == panelSessions {
			return m, m.deleteSelected()
		}
	}
	return m, nil
}

func (m *Model) panelLen() int {
	switch m.panel {
	case panelSessions:
		return len(m.sessions)
	case panelModels:
		return len(m.models)
	}
	return 0
}

func (m *Model) panelSelect() tea.Cmd {
	switch m.panel {
	case panelSessions:
		if m.panelSel < len(m.sessions) {
			return m.resumeSession(m.sessions[m.panelSel].ID)
		}
	case panelModels:
		if m.panelSel < len(m.models) {
			m.pendModel = m.models[m.panelSel].ID
			m.model = m.pendModel
			m.addNote("model set to " + m.pendModel + " (applies next turn)")
			m.closePanel()
		}
	}
	return nil
}

func (m *Model) resumeSession(id string) tea.Cmd {
	m.panelMsg = "loading session…"
	m.refresh()
	cl := m.cl
	token := m.tokens.Get(id)
	return func() tea.Msg {
		sess, eff, err := cl.SessionDetail(id, token)
		return sessionDetailMsg{sess: sess, token: eff, err: err}
	}
}

func (m *Model) deleteSelected() tea.Cmd {
	if m.panelSel >= len(m.sessions) {
		return nil
	}
	s := m.sessions[m.panelSel]
	cl := m.cl
	token := m.tokens.Get(s.ID)
	return func() tea.Msg {
		// Resolve the token (some legacy sessions mint one on first access),
		// then delete.
		_, eff, err := cl.SessionDetail(s.ID, token)
		if err != nil {
			return sessionDeletedMsg{id: s.ID, err: err}
		}
		return sessionDeletedMsg{id: s.ID, err: cl.DeleteSession(s.ID, eff)}
	}
}

// cancelRun aborts the in-flight prompt via the cancel API.
func (m *Model) cancelRun() tea.Cmd {
	if !m.busy || m.sessionID == "" {
		return nil
	}
	m.status = "cancelling"
	m.refresh()
	cl := m.cl
	sid, tok := m.sessionID, m.authToken
	return func() tea.Msg {
		return cancelDoneMsg{err: cl.Cancel(sid, tok)}
	}
}

// ── async result handling ────────────────────────────────────────────────────

func (m *Model) handleSessionsMsg(msg sessionsMsg) {
	if msg.err != nil {
		m.panelMsg = "error: " + msg.err.Error()
		return
	}
	m.sessions = msg.items
	m.panelSel = 0
	if len(m.sessions) == 0 {
		m.panelMsg = "no saved sessions yet"
	} else {
		m.panelMsg = ""
	}
}

func (m *Model) handleModelsMsg(msg modelsMsg) {
	if msg.err != nil {
		m.panelMsg = "error: " + msg.err.Error()
		return
	}
	m.models = msg.items
	m.panelSel = 0
	if len(m.models) == 0 {
		m.panelMsg = "no models advertised"
	} else {
		m.panelMsg = ""
	}
}

func (m *Model) handleSessionDetail(msg sessionDetailMsg) {
	if msg.err != nil {
		m.panelMsg = "error: " + msg.err.Error()
		return
	}
	// Replay the saved transcript into the local view and resume server-side
	// on the next prompt via session_id + auth_token.
	m.sessionID = msg.sess.ID
	m.authToken = msg.token
	m.tokens.Set(msg.sess.ID, msg.token)
	if msg.sess.Model != "" {
		m.model = msg.sess.Model
	}
	m.sandbox = msg.sess.Sandbox
	m.msgs = m.msgs[:0]
	for _, mm := range msg.sess.Messages {
		// Persisted transcripts are attacker-influenced (agent output, and the
		// session file itself); strip terminal control sequences before display.
		content := sanitize(mm.Content)
		switch mm.Role {
		case "user":
			m.msgs = append(m.msgs, message{role: roleUser, content: content})
		case "assistant":
			if strings.TrimSpace(content) == "" {
				continue
			}
			m.msgs = append(m.msgs, message{role: roleAsst, content: content, rendered: m.render(content)})
		}
	}
	m.addNote("resumed session " + shortID(msg.sess.ID))
	m.closePanel()
}

func (m *Model) handleSessionDeleted(msg sessionDeletedMsg) tea.Cmd {
	if msg.err != nil {
		m.panelMsg = "delete failed: " + msg.err.Error()
		m.refresh()
		return nil
	}
	m.tokens.Delete(msg.id)
	if m.panelSel < len(m.sessions) && m.sessions[m.panelSel].ID == msg.id {
		m.sessions = append(m.sessions[:m.panelSel], m.sessions[m.panelSel+1:]...)
		if m.panelSel >= len(m.sessions) && m.panelSel > 0 {
			m.panelSel--
		}
	}
	if len(m.sessions) == 0 {
		m.panelMsg = "no saved sessions yet"
	}
	m.refresh()
	return nil
}

// ── rendering ────────────────────────────────────────────────────────────────

// renderPanel draws the active overlay sized to fill the transcript area.
func (m *Model) renderPanel(w, h int) string {
	th := m.th
	var title string
	var rows []string

	switch m.panel {
	case panelSessions:
		title = "⟳ resume a session"
		rows = m.sessionRows(w - 6)
	case panelModels:
		title = "✦ choose a model"
		rows = m.modelRows(w - 6)
	}

	header := th.acTitle.Render(title)
	body := header
	if m.panelMsg != "" {
		body += "\n" + th.acDim.Render(m.panelMsg)
	}
	if len(rows) > 0 {
		// Window the rows around the selection to fit the available height.
		visible := h - 4 // border(2) + title(1) + breathing room
		if visible < 1 {
			visible = 1
		}
		body += "\n" + strings.Join(windowRows(rows, m.panelSel, visible), "\n")
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBrand).
		Padding(0, 1).
		Width(w - 2).
		Height(h - 2).
		Render(body)
	return box
}

func (m *Model) sessionRows(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.sessions))
	for i, s := range m.sessions {
		task := s.Task
		if task == "" {
			task = "(untitled)"
		}
		meta := fmt.Sprintf("  %s · %d turns · %s", shortID(s.ID), s.Turns, ago(s.UpdatedAt))
		budget := w - 2
		task = truncate(collapse(task), budget-lipgloss.Width(meta))
		prefix, label := "  ", th.acItem.Render(task)
		if i == m.panelSel {
			prefix, label = th.acSel.Render("› "), th.acSel.Render(task)
		}
		rows = append(rows, prefix+label+th.acDetail.Render(meta))
	}
	return rows
}

func (m *Model) modelRows(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.models))
	for i, md := range m.models {
		label := md.ID
		detail := ""
		if md.Description != "" {
			detail = "  " + md.Description
		}
		if md.Current {
			detail += "  (current)"
		}
		label = truncate(label, w-2-lipgloss.Width(detail))
		prefix, lab := "  ", th.acItem.Render(label)
		if i == m.panelSel {
			prefix, lab = th.acSel.Render("› "), th.acSel.Render(label)
		}
		rows = append(rows, prefix+lab+th.acDetail.Render(detail))
	}
	return rows
}

// windowRows returns at most n rows centered on sel.
func windowRows(rows []string, sel, n int) []string {
	if len(rows) <= n {
		return rows
	}
	start := sel - n/2
	if start < 0 {
		start = 0
	}
	if start+n > len(rows) {
		start = len(rows) - n
	}
	return rows[start : start+n]
}

// shortID trims a session ID for display.
func shortID(id string) string {
	if len(id) > 17 {
		return id[:17] + "…"
	}
	return id
}

// ago renders a coarse relative time.
func ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
