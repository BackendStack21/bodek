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
	case "backspace", "delete", "ctrl+h":
		m.backspaceClarify()
		return m, nil
	case "ctrl+c":
		return m, m.armConfirm(confirmQuit, "bodek")
	case "shift+enter", "alt+enter", "ctrl+j":
		m.appendClarify("\n")
		return m, nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case "ctrl+g":
		m.vp.GotoBottom()
		return m, nil
	}
	if text := clarifyTyped(msg); text != "" {
		m.appendClarify(text)
	}
	return m, nil
}

// clarifyTyped returns text to insert from a keypress. Bubble Tea sends the
// spacebar as KeySpace (String is " " or "space"), not KeyRunes — a
// KeyRunes-only path mashed words together. Letters, punctuation, and
// paste still arrive as KeyRunes.
func clarifyTyped(msg tea.KeyMsg) string {
	if msg.Type == tea.KeySpace {
		return " "
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 && !msg.Alt {
		return string(msg.Runes)
	}
	if s := msg.String(); len([]rune(s)) == 1 {
		return s
	}
	return ""
}

func (m *Model) appendClarify(s string) {
	if s == "" {
		return
	}
	m.clarifyBuf += s
	m.relayout()
	m.refresh()
}

func (m *Model) backspaceClarify() {
	if m.clarifyBuf == "" {
		return
	}
	r := []rune(m.clarifyBuf)
	m.clarifyBuf = string(r[:len(r)-1])
	m.relayout()
	m.refresh()
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
	prompt := "answer: " + sanitize(m.clarifyBuf)
	for _, ln := range wrapText(prompt, budget) {
		lines = append(lines, th.apprKey.Render(ln))
	}
	lines = append(lines, th.apprBody.Render("enter send · ⇧⏎ newline · type to answer"))
	return strings.Join(lines, "\n")
}
