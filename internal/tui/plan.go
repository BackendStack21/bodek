package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── planning surface state (docs/PLANNING_MODE_UI.md §3.2) ─────────────────
//
// bodek's WS carries triggers only: every engine plan mutation arrives as an
// ordinary plan tool_call/tool_result pair. The structured truth lives behind
// GET /api/sessions/{id}/plan. So: watch the WS, then fetch REST — debounced
// (a create→update burst is one request), guarded monotonically by the
// store's version, and silently degraded when an old engine lacks the route.

const (
	planDebounceEvery = 250 * time.Millisecond // WS-trigger coalescing window
	planPollEvery     = 3 * time.Second        // drawer-plan-tab visible cadence
)

// planAvailability tri-states the endpoint: unknown (not tried), available,
// unavailable (404/transport — silent degrade, retries only via explicit
// triggers, never the background poll).
type planAvailability uint8

const (
	planUnknown planAvailability = iota
	planAvailable
	planUnavailable
)

// planDebounceMsg arms the trailing edge of the WS-trigger window; a newer
// trigger supersedes it via sequence compare (the expiry-sweep pattern).
type planDebounceMsg struct{ seq int }

// planTickMsg re-arms the tab-visible poll (runsTickMsg pattern).
type planTickMsg struct{ seq int }

// planMsg carries one SessionPlan fetch outcome. want/seq identify the
// request so superseded replies cannot touch fresh state.
type planMsg struct {
	want string
	seq  int
	snap client.PlanSnapshot
	err  error
}

// schedulePlanRefresh arms (or re-arms) the debounced refresh timer.
func (m *Model) schedulePlanRefresh() tea.Cmd {
	m.planTrig = false
	m.planDebSeq++
	seq := m.planDebSeq
	return tea.Tick(planDebounceEvery, func(time.Time) tea.Msg {
		return planDebounceMsg{seq: seq}
	})
}

// handlePlanDebounce fires the fetch only for the newest armed window.
func (m *Model) handlePlanDebounce(msg planDebounceMsg) tea.Cmd {
	if msg.seq != m.planDebSeq || m.sessionID == "" {
		return nil // superseded window or not attached to a session yet
	}
	return m.fetchPlan()
}

// fetchPlan issues one snapshot request pinned to the current session.
func (m *Model) fetchPlan() tea.Cmd {
	if m.cl == nil || m.sessionID == "" {
		return nil
	}
	m.planReqSeq++
	cl := m.cl
	want := m.sessionID
	token := m.authToken
	seq := m.planReqSeq
	return func() tea.Msg {
		snap, err := cl.SessionPlan(want, token)
		return planMsg{want: want, seq: seq, snap: snap, err: err}
	}
}

// handlePlanMsg accepts-or-rejects a reply and keeps the tab-visible poll
// alive. Stale sequencing, foreign sessions, and version regressions are all
// dropped without touching state (ingestion stays idempotent); errors mark
// the surface unavailable — never surfaced as noise.
func (m *Model) handlePlanMsg(msg planMsg) (cmd tea.Cmd) {
	defer func() {
		if m.panel == panelPlan && m.planAvail != planUnavailable {
			cmd = tea.Batch(cmd, m.armPlanPoll())
		}
	}()
	if msg.err != nil {
		m.planAvail = planUnavailable
		if m.panel == panelPlan {
			m.syncPlanPanelMsg()
		}
		return nil
	}
	m.planAvail = planAvailable
	if msg.seq != m.planReqSeq || msg.want != m.sessionID {
		return nil // superseded by a newer request or a session switch
	}
	if msg.snap.SessionID != "" && msg.snap.SessionID != m.sessionID {
		return nil // defensive: wire disagrees about the target session
	}
	if m.planInit && msg.snap.Version < m.planVer {
		return nil // monotonic guard: stale snapshot, found:false included
	}
	m.plan = msg.snap
	m.planVer = msg.snap.Version
	m.planInit = true
	if m.panel == panelPlan {
		m.syncPlanPanelMsg()
	}
	m.refresh()
	return nil
}

// armPlanPoll schedules the next visible-tab tick (exactly one armed tick per
// cycle; closing the tab drains the chain via the seq check).
func (m *Model) armPlanPoll() tea.Cmd {
	m.planPollSeq++
	seq := m.planPollSeq
	return tea.Tick(planPollEvery, func(time.Time) tea.Msg {
		return planTickMsg{seq: seq}
	})
}

// handlePlanTick polls while the plan tab is visible; an unavailable endpoint
// stops the chain (re-entry or 'r' restarts it).
func (m *Model) handlePlanTick(msg planTickMsg) tea.Cmd {
	if m.panel != panelPlan || msg.seq != m.planPollSeq ||
		m.planAvail == planUnavailable {
		return nil
	}
	return m.fetchPlan()
}

// resetPlanState drops accepted knowledge (session switch / attach): pending
// timers and in-flight replies drain via their sequence bumps.
func (m *Model) resetPlanState() {
	m.plan = client.PlanSnapshot{}
	m.planVer, m.planInit = 0, false
	m.planAvail = planUnknown
	m.planDebSeq++
	m.planReqSeq++
}

// planFollowup / openPlan ─ rendering & drawer integration ────────────────

// openPlan is the drawer tab opener: same grammar as openRuns — reset the
// selection, show a status line, fetch immediately. The visible poll chain
// is maintained by handlePlanMsg's re-arm branch.
func (m *Model) openPlan() tea.Cmd {
	m.panel = panelPlan
	m.panelSel = 0
	m.panelEdit = panelEditNone
	m.panelDetail = false
	m.detailScroll = 0
	m.syncPlanPanelMsg()
	m.relayout()
	m.refresh()
	return m.fetchPlan()
}

// planStripLabel renders the live-run summary shown next to the busy
// indicator: "plan 2/5 · <active step> · ⛔1". Empty unless a run is active,
// the endpoint is healthy, and a non-collapsed plan exists.
func (m *Model) planStripLabel() string {
	if !m.busy || !m.planInit || m.planAvail != planAvailable || !m.plan.Found {
		return ""
	}
	total := len(m.plan.Steps)
	if total == 0 {
		return "" // collapsed all-done plan — the drawer tab carries the record
	}
	done, blocked := 0, 0
	active := ""
	for _, st := range m.plan.Steps {
		switch st.Status {
		case client.PlanDone:
			done++
		case client.PlanBlocked:
			blocked++
		case client.PlanInProgress:
			if active == "" {
				active = collapse(sanitize(st.Title))
			}
		}
	}
	s := fmt.Sprintf("plan %d/%d", done, total)
	if active != "" {
		s += " · " + truncate(active, 32)
	}
	if blocked > 0 {
		s += fmt.Sprintf(" · ⛔%d", blocked)
	}
	return s
}

// syncPlanPanelMsg refreshes the tab's empty/unavailable copy from state.
func (m *Model) syncPlanPanelMsg() {
	switch {
	case m.cl == nil || m.sessionID == "":
		// No live session: fetchPlan is a silent no-op, so "loading plan…"
		// would never resolve. Say why instead (attach completes → reset +
		// refetch at the tail resolves this to a real snapshot).
		m.panelMsg = "no active session yet — the plan loads when a run starts"
	case m.planAvail == planUnavailable:
		m.panelMsg = "plan unavailable on this engine · r retries"
	case m.planAvail == planUnknown && !m.planInit:
		m.panelMsg = "loading plan…"
	case m.planInit && !m.plan.Found:
		m.panelMsg = "no active plan in this session."
	default:
		m.panelMsg = ""
	}
}

// planGlyph maps a step status to its row glyph.
func planGlyph(s client.PlanStepStatus) string {
	switch s {
	case client.PlanDone:
		return "✅"
	case client.PlanInProgress:
		return "🔄"
	case client.PlanBlocked:
		return "⛔"
	case client.PlanPending:
		return "⬜"
	default:
		return "⬜" // unknown future statuses degrade to pending
	}
}

// planStepAt returns the selected step, if any.
func (m *Model) planStepAt(i int) *client.PlanStep {
	if i >= 0 && i < len(m.plan.Steps) {
		return &m.plan.Steps[i]
	}
	return nil
}

// planTitle composes the header line: base title plus the Telegram-parity
// summary badge ("v7 · 2/4 done · 1 blocked"), styled like the events tab's
// filter badge so it never participates in row selection.
func (m *Model) planTitle() string {
	base := "📋 plan"
	if !m.planInit || m.planAvail != planAvailable || !m.plan.Found {
		return base
	}
	done, blocked := 0, 0
	for _, st := range m.plan.Steps {
		switch st.Status {
		case client.PlanDone:
			done++
		case client.PlanBlocked:
			blocked++
		}
	}
	badge := fmt.Sprintf("v%d", m.plan.Version)
	if total := len(m.plan.Steps); total == 0 {
		badge += " · ✓ all done"
	} else {
		badge += fmt.Sprintf(" · %d/%d done", done, total)
		if blocked > 0 {
			badge += fmt.Sprintf(" · %d blocked", blocked)
		}
	}
	return base + m.th.acDetail.Render("  ·  "+badge)
}

// planRows renders one line per step: glyph, id, truncated title, dim note
// preview — same shape discipline as eventRows.
func (m *Model) planRows(w int) []string {
	rows := make([]string, 0, len(m.plan.Steps))
	for _, st := range m.plan.Steps {
		label := planGlyph(st.Status) + " " + sanitize(st.ID)
		if t := sanitize(st.Title); t != "" {
			label += "  " + collapse(t)
		}
		detail := ""
		if n := sanitize(st.Note); n != "" {
			detail = "  " + collapse(n)
		}
		maxLabel := w - 2 - lipgloss.Width(detail)
		if maxLabel < 8 {
			maxLabel = 8
		}
		rows = append(rows,
			m.th.acItem.Render("  "+truncate(label, maxLabel))+m.th.acDetail.Render(detail))
	}
	return rows
}

// planTrig / planResetPending consume-at-tail helpers: ordinary event cases
// share one render-coalescing exit, so these two flags ride the existing
// batch instead of restructuring hot paths.
func (m *Model) planFollowup() tea.Cmd {
	var cmds []tea.Cmd
	if m.planResetPending {
		m.planResetPending = false
		m.resetPlanState()
		if c := m.fetchPlan(); c != nil {
			cmds = append(cmds, c)
		}
	}
	if m.planTrig {
		cmds = append(cmds, m.schedulePlanRefresh())
	}
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}
