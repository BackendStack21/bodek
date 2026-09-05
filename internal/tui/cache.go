package tui

// msgBlockCache holds one finalized transcript message's rendered block.
// Ref line numbers are relative to the block start (offset at assembly time).
type msgBlockCache struct {
	block string
	refs  []stepRef
	width int
	valid bool
}

func (m *Model) ensureMsgBlocks() {
	if len(m.msgBlocks) >= len(m.msgs) {
		return
	}
	m.msgBlocks = append(m.msgBlocks, make([]msgBlockCache, len(m.msgs)-len(m.msgBlocks))...)
}

// invalidateMsgBlock drops one message's cached block (step expand, agent
// focus, collapse — without rebuilding siblings).
func (m *Model) invalidateMsgBlock(i int) {
	if i < 0 || i >= len(m.msgBlocks) {
		return
	}
	m.msgBlocks[i].valid = false
}

// invalidateAllMsgBlocks drops every cached message block (resize, expand-all,
// transcript wholesale replace).
func (m *Model) invalidateAllMsgBlocks() {
	for i := range m.msgBlocks {
		m.msgBlocks[i].valid = false
	}
	m.convCount = -1
}

func (m *Model) resetMsgBlocks() {
	m.msgBlocks = nil
	m.convCount = -1
}

// msgBlockAt returns a cached or freshly rendered block for message i and
// step refs offset to lineOffset in the full transcript.
func (m *Model) msgBlockAt(i, lineOffset int) (string, []stepRef) {
	m.ensureMsgBlocks()
	bc := &m.msgBlocks[i]
	if !bc.valid || bc.width != m.vp.Width {
		s, r := m.renderMessage(m.msgs[i], i, 0)
		bc.block = m.clampLines(s)
		bc.refs = r
		bc.width = m.vp.Width
		bc.valid = true
	}
	return bc.block, offsetStepRefs(bc.refs, lineOffset)
}

func clearStepBlockCache(s *step) {
	s.blockCache = ""
	s.blockRefs = nil
	s.blockWidth = 0
}

func stepBlockCacheValid(s step, st *step, m *Model, expanded bool) bool {
	if !s.done || s.blockCache == "" || st == nil {
		return false
	}
	if st.name != s.name || st.done != s.done || st.result != s.result || st.expanded != s.expanded {
		return false
	}
	if st.agentSel != s.agentSel {
		return false
	}
	if s.blockWidth != m.vp.Width {
		return false
	}
	if s.blockExpanded != expanded {
		return false
	}
	if s.blockExpandAll != m.expandAll {
		return false
	}
	return true
}
