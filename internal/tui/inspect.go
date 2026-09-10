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
	m.refresh()
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
	m.invalidateInspect()
	p := targets[index]
	m.inspect = &p
	m.focusIdx = p.msgIdx
	m.msgs[p.msgIdx].collapsed = false
	m.invalidateInspect()
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
				m.vp.SetYOffset(max(0, r.line-1))
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
	case "tab", "down":
		m.moveInspect(false)
	case "shift+tab", "up":
		m.moveInspect(true)
	case "enter", " ":
		p := m.inspect
		if p.stepIdx >= 0 {
			s := &m.msgs[p.msgIdx].steps[p.stepIdx]
			wasExpanded := s.expanded || m.expandAll
			if m.expandAll {
				m.expandAll = false
				m.invalidateAllMsgBlocks()
				for i := range m.msgs {
					for j := range m.msgs[i].steps {
						clearStepBlockCache(&m.msgs[i].steps[j])
					}
				}
			}
			s.expanded = !wasExpanded
			s.detailOffset = 0
		} else {
			it := &m.msgs[p.msgIdx].items[p.itemIdx]
			it.open = !it.open
		}
		m.invalidateInspect()
		m.refresh()
		m.revealInspect()
	case "[", "]":
		if m.inspect.stepIdx >= 0 {
			s := &m.msgs[m.inspect.msgIdx].steps[m.inspect.stepIdx]
			delta := m.toolDetailRows()
			if msg.String() == "[" {
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

func (m *Model) toolDetailRows() int { return max(1, min(8, (m.height-12)/2)) }

// toolDetailPage bounds every detail body by display rows, including embedded
// newlines from renderers. Paging changes the slice, never the screen geometry.
func (m *Model) toolDetailPage(s *step, details []string, width int) []string {
	var rows []string
	for _, d := range details {
		for _, line := range strings.Split(d, "\n") {
			rows = append(rows, ansi.Truncate(line, max(1, width), ""))
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
	label := fmt.Sprintf("%d–%d/%d · Tab select · [ ] page", offset+1, end, len(rows))
	out = append(out, m.th.stepArg.Render(ansi.Truncate(label, max(1, width), "")))
	return out
}
