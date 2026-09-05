package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// ── skill suggestions (skill_prompt_response) ───────────────────────────────
//
// When the learn loop proposes a skill (skill_event "suggested"), a shelf
// chip sits above the composer as a PASSIVE affordance: it never captures
// ⏎ — a suggestion arriving mid-conversation must not block sending —
// and answers ride alt-chords (or esc to skip). The protocol reply is an
// acknowledgment; skill auto-save governs real persistence server-side.

// handleSuggestKeys answers a pending skill suggestion when one shows.
func (m *Model) handleSuggestKeys(s string) (tea.Model, tea.Cmd, bool) {
	if m.skillSuggest == nil {
		return m, nil, false
	}
	switch s {
	case "alt+s":
		return m, m.answerSuggestion("save"), true
	case "alt+x":
		return m, m.answerSuggestion("skip"), true
	}
	return m, nil, false
}

func (m *Model) answerSuggestion(action string) tea.Cmd {
	ev := *m.skillSuggest
	m.skillSuggest = nil
	m.relayout()
	m.refresh()
	cl := m.cl
	verb := "skipped"
	if action == "save" {
		verb = "saved"
	}
	note := m.transientNoteCmd("skill " + ev.SkillName + ": " + verb + " (auto-save governs persistence)")
	return tea.Batch(note, func() tea.Msg {
		if err := cl.SendSkillPromptResponse(action, ev.SkillName); err != nil {
			return skillSendErrMsg{ev: ev, err: err}
		}
		return nil
	})
}
