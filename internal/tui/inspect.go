package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// inspectTarget identifies an item without depending on its rendered line number.
// stepIdx is -1 for reasoning; itemIdx is -1 for a tool.
type inspectTarget struct{ msgIdx, stepIdx, itemIdx int }

func (m *Model) inspectTargets() []inspectTarget {
	var out []inspectTarget
	for i, msg := range m.msgs {
		if msg.role != roleAsst || msg.raw {
			continue
		}
		seen := make(map[int]bool)
		for j, item := range msg.items {
			if item.thinking && strings.TrimSpace(item.text) != "" {
				out = append(out, inspectTarget{i, -1, j})
			} else if !item.reply && !item.thinking && item.stepIdx >= 0 && item.stepIdx < len(msg.steps) {
				out = append(out, inspectTarget{i, item.stepIdx, -1})
				seen[item.stepIdx] = true
			}
		}
		for j := range msg.steps {
			if !seen[j] {
				out = append(out, inspectTarget{i, j, -1})
			}
		}
	}
	return out
}

func (m *Model) validInspect() bool {
	p := m.inspect
	if p == nil || p.msgIdx < 0 || p.msgIdx >= len(m.msgs) {
		return false
	}
	msg := &m.msgs[p.msgIdx]
	if p.stepIdx >= 0 {
		return p.stepIdx < len(msg.steps)
	}
	return p.itemIdx >= 0 && p.itemIdx < len(msg.items) && msg.items[p.itemIdx].thinking
}

// Inspection borrows the composer rows so a short terminal can show the
// selected step's heading, details, and pager together. The draft is kept in
// the textarea and returns unchanged on Escape.
func (m *Model) inspectChrome() bool {
	return m.validInspect() && m.curApproval() == nil && m.clarify == nil &&
		m.panel == panelNone && !m.pal.open && !m.find.open && !m.ac.open &&
		!m.qfocus && !m.popover
}

func (m *Model) invalidateInspect() {
	if !m.validInspect() {
		return
	}
	i := m.inspect.msgIdx
	m.invalidateMsgBlock(i)
	for j := range m.msgs[i].steps {
		clearStepBlockCache(&m.msgs[i].steps[j])
	}
}

func (m *Model) clearInspect() {
	m.invalidateInspect()
	m.inspect = nil
	m.relayout()
	m.refresh()
}

// openInspectStep makes deliberate inspection a single-step view. Global
// details remain available with ^E after leaving inspection.
func (m *Model) openInspectStep(msgIdx, stepIdx int) {
	if msgIdx < 0 || msgIdx >= len(m.msgs) || stepIdx < 0 || stepIdx >= len(m.msgs[msgIdx].steps) {
		return
	}
	m.invalidateInspect()
	if m.expandAll {
		m.expandAll = false
		m.invalidateAllMsgBlocks()
	}
	for i := range m.msgs {
		for j := range m.msgs[i].steps {
			s := &m.msgs[i].steps[j]
			open := i == msgIdx && j == stepIdx
			focusChanged := open && s.clearAgentFocus()
			if s.expanded != open || (open && s.detailOffset != 0) || focusChanged {
				s.expanded = open
				s.detailOffset = 0
				clearStepBlockCache(s)
				m.invalidateMsgBlock(i)
			}
		}
	}
	m.inspect = &inspectTarget{msgIdx: msgIdx, stepIdx: stepIdx, itemIdx: -1}
	m.focusIdx = msgIdx
	m.msgs[msgIdx].collapsed = false
	m.relayout()
	m.refresh()
	m.revealInspect()
}

func (m *Model) moveInspect(back bool) {
	targets := m.inspectTargets()
	if len(targets) == 0 {
		m.inspect = nil
		return
	}
	index := -1
	if m.validInspect() {
		for i, p := range targets {
			if p == *m.inspect {
				index = i
				break
			}
		}
	}
	if index < 0 {
		// Start in the explicitly selected turn, or the latest tool-bearing turn.
		targetMsg := targets[len(targets)-1].msgIdx
		if m.focusIdx >= 0 {
			for _, p := range targets {
				if p.msgIdx == m.focusIdx {
					targetMsg = m.focusIdx
					break
				}
			}
		}
		for i, p := range targets {
			if p.msgIdx == targetMsg {
				index = i
				break
			}
		}
		if back {
			for i, p := range targets {
				if p.msgIdx == targetMsg {
					index = i
				}
			}
		}
	} else if back {
		index = (index + len(targets) - 1) % len(targets)
	} else {
		index = (index + 1) % len(targets)
	}
	if m.validInspect() && m.inspect.stepIdx >= 0 && !m.expandAll {
		old := &m.msgs[m.inspect.msgIdx].steps[m.inspect.stepIdx]
		if old.expanded {
			old.expanded = false
			old.detailOffset = 0
			clearStepBlockCache(old)
			m.invalidateMsgBlock(m.inspect.msgIdx)
		}
	}
	m.invalidateInspect()
	p := targets[index]
	m.inspect = &p
	m.focusIdx = p.msgIdx
	m.msgs[p.msgIdx].collapsed = false
	m.invalidateInspect()
	m.relayout()
	m.refresh()
	m.revealInspect()
}

func (m *Model) revealInspect() {
	if !m.validInspect() {
		return
	}
	p := m.inspect
	if p.stepIdx >= 0 {
		for _, r := range m.stepLineIndex {
			if r.msgIdx == p.msgIdx && r.stepIdx == p.stepIdx && r.x1 <= r.x0 {
				top := r.line - 1
				if m.inspectChrome() {
					top = r.line
					s := &m.msgs[p.msgIdx].steps[p.stepIdx]
					chips := len(packChipRows(s.agentChips(), max(m.cardInner()-2, 8)))
					if s.expanded && 1+chips+m.toolDetailRows()+1 > m.vp.Height {
						top += 1 + chips // keep the active detail page visible
					}
				}
				m.vp.SetYOffset(max(0, top))
				m.relayout()
				return
			}
		}
	}
	m.scrollToMessage(p.msgIdx)
}

func (m *Model) handleInspectKey(msg tea.KeyMsg) bool {
	if !m.validInspect() {
		m.inspect = nil
		return false
	}
	switch msg.String() {
	case "down":
		m.moveInspect(false)
	case "up":
		m.moveInspect(true)
	case "enter", " ":
		p := m.inspect
		if p.stepIdx >= 0 {
			s := &m.msgs[p.msgIdx].steps[p.stepIdx]
			wasExpanded := s.expanded || m.expandAll
			if !wasExpanded || m.expandAll {
				m.openInspectStep(p.msgIdx, p.stepIdx)
				return true
			}
			s.expanded = false
			s.detailOffset = 0
		} else {
			it := &m.msgs[p.msgIdx].items[p.itemIdx]
			it.open = !it.open
		}
		m.invalidateInspect()
		m.refresh()
		m.revealInspect()
	case "pgup", "pgdown":
		if m.inspect.stepIdx >= 0 {
			s := &m.msgs[m.inspect.msgIdx].steps[m.inspect.stepIdx]
			delta := m.toolDetailRows()
			if msg.String() == "pgup" {
				delta = -delta
			}
			s.detailOffset = max(0, s.detailOffset+delta)
			s.expanded = true
			m.invalidateInspect()
			m.refresh()
			m.revealInspect()
		}
	case "right":
		if m.inspect.stepIdx >= 0 {
			s := &m.msgs[m.inspect.msgIdx].steps[m.inspect.stepIdx]
			chips := s.agentChips()
			if len(chips) > 0 {
				next := chips[0].idx
				for i, c := range chips {
					if c.idx == s.focusedIdx() {
						next = -1
						if i+1 < len(chips) {
							next = chips[i+1].idx
						}
						break
					}
				}
				s.setAgentFocus(next)
				s.detailOffset = 0
				m.invalidateInspect()
				m.refresh()
			}
		}
	case "esc":
		m.clearInspect()
	default:
		// Typing deliberately returns to the composer. Modified shortcuts retain
		// their usual action, including copy and stop.
		if msg.Type == tea.KeyRunes && !msg.Alt {
			m.clearInspect()
		}
		return false
	}
	return true
}

func (m *Model) toolDetailRows() int {
	if m.inspectChrome() {
		room := m.vp.Height - 2 // heading and pager
		if m.inspect.stepIdx >= 0 {
			s := &m.msgs[m.inspect.msgIdx].steps[m.inspect.stepIdx]
			if len(s.agentChips()) > 0 {
				// The chip strip remains in the transcript. When it pushes the
				// detail page below the viewport, revealInspect scrolls to the
				// page and the compact input line keeps the tool identity visible.
				room = m.vp.Height - 1
			}
		}
		return max(1, min(8, room))
	}
	return max(1, min(8, (m.height-12)/2))
}

// toolDetailPage bounds every detail body by display rows, including embedded
// newlines from renderers. The pager names the visible section, so even a
// one-row page has context when its section heading has scrolled away.
func (m *Model) toolDetailPage(s *step, details []string, width, sectionBreak int, firstSection, nextSection string) []string {
	var rows []string
	var sections []string
	for i, d := range details {
		section := nextSection
		if i < sectionBreak {
			section = firstSection
		}
		for _, line := range strings.Split(d, "\n") {
			rows = append(rows, ansi.Truncate(line, max(1, width), ""))
			sections = append(sections, section)
		}
	}
	limit := m.toolDetailRows()
	if len(rows) <= limit {
		return rows
	}
	offset := min(s.detailOffset, max(0, ((len(rows)-1)/limit)*limit))
	s.detailOffset = offset
	end := min(len(rows), offset+limit)
	out := append([]string(nil), rows[offset:end]...)
	section := sections[offset]
	if sections[end-1] != section {
		section += " → " + sections[end-1]
	}
	label := fmt.Sprintf("%s · %d–%d/%d · PgUp PgDn page", section, offset+1, end, len(rows))
	out = append(out, m.th.stepArg.Render(ansi.Truncate(label, max(1, width), "")))
	return out
}
