package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) clearClarify() {
	m.clarify = nil
	m.clarifyBuf = ""
}

func (m *Model) handleClarifyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.clarify == nil {
		return m, nil
	}
	switch msg.String() {
	case "enter":
		return m, m.sendClarifyAnswer()
	case "esc":
		return m, nil
	case "backspace":
		if m.clarifyBuf != "" {
			r := []rune(m.clarifyBuf)
			m.clarifyBuf = string(r[:len(r)-1])
			m.refresh()
		}
		return m, nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	if msg.Type == tea.KeyRunes {
		m.clarifyBuf += string(msg.Runes)
		m.refresh()
	}
	return m, nil
}

func (m *Model) sendClarifyAnswer() tea.Cmd {
	if m.clarify == nil {
		return nil
	}
	answer := strings.TrimSpace(m.clarifyBuf)
	if answer == "" {
		return nil
	}
	id := m.clarify.ID
	m.clearClarify()
	m.setRunStatus("thinking")
	m.relayout()
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		if err := cl.SendClarify(id, answer); err != nil {
			return errMsg{err: err}
		}
		return nil
	}
}

func (m *Model) clarifyPanel() string {
	return m.th.apprBox.Width(m.cardWidth()).Render(m.clarifyBody())
}

func (m *Model) clarifyBody() string {
	th := m.th
	if m.clarify == nil {
		return ""
	}
	head := th.apprHead.Render("❓ question from the agent")
	budget := m.cardInner()
	lines := []string{head}
	for _, ln := range wrapText(sanitize(m.clarify.Question), budget) {
		lines = append(lines, th.apprBody.Render(ln))
	}
	prompt := "answer: " + m.clarifyBuf
	lines = append(lines, th.apprKey.Render(truncate(prompt, budget)))
	lines = append(lines, th.apprBody.Render("enter send · type to answer"))
	return strings.Join(lines, "\n")
}
