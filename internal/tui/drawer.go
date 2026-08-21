package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── the drawer ──────────────────────────────────────────────────────────────
//
// One tabbed frame hosts every management surface, so integrations scale as
// tabs instead of modalities. ]/[ cycle, digits jump, esc closes — the same
// grammar across every tab. The models picker (^O) stays its own overlay.

// tabBar renders the drawer's tab strip after the panel title: the active
// tab in accent, the rest muted, with the digit shortcuts taught inline.
func (m *Model) tabBar() string {
	var parts []string
	for i, t := range drawerTabs() {
		label := fmt.Sprintf("%d %s", i+1, t.name)
		if t.mode == m.panel {
			parts = append(parts, m.th.acSel.Render(label))
		} else {
			parts = append(parts, m.th.acDetail.Render(label))
		}
	}
	return "  " + strings.Join(parts, m.th.footerSep.Render(" · "))
}

// runPollEvery is the runs-tab refresh cadence while visible (spec: poll
// headless runs every 3s).
const runPollEvery = 3 * time.Second

// runsTickMsg re-arms the runs-tab poll.
type runsTickMsg struct{ seq int }

// runsMsg carries a runs fetch.
type runsMsg struct {
	runs []client.Run
	err  error
}

// eventsMsg carries an events fetch.
type eventsMsg struct {
	events []client.RuntimeEvent
	err    error
}

// runActionMsg reports a run cancel / approval-answer outcome.
type runActionMsg struct{ err error }

// runStartedMsg reports a headless run started via /run or the palette.
type runStartedMsg struct {
	run client.Run
	err error
}

// startHeadlessRun submits a headless REST run (fresh session — it never
// races the interactive conversation) and opens the runs tab on success.
func (m *Model) startHeadlessRun(prompt string) tea.Cmd {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return m.transientNoteCmd("/run needs a prompt — type one or use the palette with a draft")
	}
	cl := m.cl
	thinking := ""
	if m.thinkOn {
		thinking = "enabled"
	}
	model := m.pendModel
	m.pendModel = "" // applied to the run, not held for the next turn
	return func() tea.Msg {
		run, err := cl.StartRun(prompt, client.RunOpts{Model: model, Thinking: thinking})
		return runStartedMsg{run: run, err: err}
	}
}

func (m *Model) handleRunStarted(msg runStartedMsg) tea.Cmd {
	if msg.err != nil {
		return m.transientNoteCmd("headless run failed: " + msg.err.Error())
	}
	note := m.transientNoteCmd("headless run started · " + shortID(msg.run.ID))
	m.refresh()
	return tea.Batch(note, m.openRuns())
}

// drawerTab is one tab of the management drawer.
type drawerTab struct {
	name string
	mode panelMode
	open func(m *Model) tea.Cmd
}

// drawerTabs lists the drawer's tabs in display order. Phase 3 appends
// memory/skills/tools/config here — nothing else changes.
func drawerTabs() []drawerTab {
	return []drawerTab{
		{"sessions", panelSessions, func(m *Model) tea.Cmd { return m.openSessions() }},
		{"runs", panelRuns, func(m *Model) tea.Cmd { return m.openRuns() }},
		{"events", panelEvents, func(m *Model) tea.Cmd { return m.openEvents() }},
	}
}

// drawerPanel reports whether the active panel is a drawer tab.
func drawerPanel(p panelMode) bool {
	for _, t := range drawerTabs() {
		if t.mode == p {
			return true
		}
	}
	return false
}

// switchDrawerTab moves the drawer to another tab, preserving nothing — each
// tab owns its state and fetches fresh on open.
func (m *Model) switchDrawerTab(mode panelMode) tea.Cmd {
	for _, t := range drawerTabs() {
		if t.mode == mode {
			return t.open(m)
		}
	}
	return nil
}

// cycleDrawerTab steps through the tabs (dir ±1).
func (m *Model) cycleDrawerTab(dir int) tea.Cmd {
	tabs := drawerTabs()
	for i, t := range tabs {
		if t.mode == m.panel {
			next := (i + dir + len(tabs)) % len(tabs)
			return m.switchDrawerTab(tabs[next].mode)
		}
	}
	return nil
}

// ── runs tab ────────────────────────────────────────────────────────────────

func (m *Model) openRuns() tea.Cmd {
	m.panel = panelRuns
	m.panelSel = 0
	m.panelEdit = panelEditNone
	m.panelMsg = "loading runs…"
	m.relayout()
	m.refresh()
	return m.fetchRuns()
}

// runsSeq guards against a stale poll tick after the tab closed.
func (m *Model) armRunPoll() tea.Cmd {
	seq := m.runsSeq + 1
	m.runsSeq = seq
	return tea.Tick(runPollEvery, func(time.Time) tea.Msg {
		return runsTickMsg{seq: seq}
	})
}

func (m *Model) fetchRuns() tea.Cmd {
	cl := m.cl
	return func() tea.Msg {
		runs, err := cl.Runs()
		return runsMsg{runs: runs, err: err}
	}
}

func (m *Model) handleRunsMsg(msg runsMsg) {
	if msg.err != nil {
		m.panelMsg = "error: " + msg.err.Error()
		return
	}
	m.runs = msg.runs
	if m.panelSel >= len(m.runs) {
		m.panelSel = max(len(m.runs)-1, 0)
	}
	if len(m.runs) == 0 {
		m.panelMsg = "no headless runs — start one from the palette or /run"
	} else {
		m.panelMsg = ""
	}
}

func (m *Model) handleRunsTick(msg runsTickMsg) tea.Cmd {
	if m.panel != panelRuns || msg.seq != m.runsSeq {
		return nil // tab closed or superseded
	}
	// The fetch's runsMsg re-arms the next tick — exactly one armed tick
	// exists per cycle, and closing the tab drains the chain.
	return m.fetchRuns()
}

// selectedRun returns the highlighted run, if any.
func (m *Model) selectedRun() *client.Run {
	if m.panel == panelRuns && m.panelSel < len(m.runs) {
		return &m.runs[m.panelSel]
	}
	return nil
}

// cancelSelectedRun aborts the highlighted headless run.
func (m *Model) cancelSelectedRun() tea.Cmd {
	r := m.selectedRun()
	if r == nil || r.Terminal() {
		return nil
	}
	cl := m.cl
	id := r.ID
	return func() tea.Msg {
		return runActionMsg{err: cl.CancelRun(id)}
	}
}

// answerSelectedRunApproval answers the highlighted run's first pending
// approval (a second answer re-fetches the queue — the poll refreshes it).
func (m *Model) answerSelectedRunApproval(action string) tea.Cmd {
	r := m.selectedRun()
	if r == nil || len(r.PendingApprovals) == 0 {
		return nil
	}
	ap := r.PendingApprovals[0]
	if action == "trust" && (!ap.AllowTrust || ap.Friction) {
		return nil
	}
	cl := m.cl
	runID, apID := r.ID, ap.ID
	return func() tea.Msg {
		return runActionMsg{err: cl.AnswerRunApproval(runID, apID, action)}
	}
}

func (m *Model) handleRunAction(msg runActionMsg) tea.Cmd {
	if msg.err != nil {
		m.panelMsg = "run action failed: " + msg.err.Error()
		m.refresh()
		return nil
	}
	return m.fetchRuns()
}

// runStatusGlyph renders a run's status at a glance.
func runStatusGlyph(r client.Run) string {
	switch r.Status {
	case "running":
		return "▶"
	case "waiting_approval":
		return "⏳"
	case "completed":
		return "✓"
	case "failed":
		return "✗"
	case "cancelled":
		return "⊘"
	}
	return "·"
}

func (m *Model) runRows(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.runs))
	for i, r := range m.runs {
		meta := fmt.Sprintf("  %s · %s", shortID(r.ID), r.Status)
		if !r.StartedAt.IsZero() {
			if r.EndedAt.IsZero() {
				meta += " · " + formatDuration(time.Since(r.StartedAt))
			} else {
				meta += " · " + formatDuration(r.EndedAt.Sub(r.StartedAt))
			}
		}
		if r.InputTokens > 0 || r.OutputTokens > 0 {
			meta += fmt.Sprintf(" · ⇥%s ↦%s", human(int(r.InputTokens)), human(int(r.OutputTokens)))
		}
		if n := len(r.PendingApprovals); n > 0 {
			meta += fmt.Sprintf(" · ⚠ %d approval", n)
		}
		label := runStatusGlyph(r) + "  " + orDash(r.Model)
		if r.Result != "" {
			label += "  —  " + truncate(collapse(r.Result), 30)
		} else if r.Error != "" {
			label += "  —  " + truncate(collapse(r.Error), 30)
		}
		budget := w - 2
		label = truncate(label, budget-lipgloss.Width(meta))
		rows = append(rows, m.renderRunStatus(i, label)+th.acDetail.Render(meta))
	}
	return rows
}

// renderRunStatus styles a run row with selection state.
func (m *Model) renderRunStatus(i int, label string) string {
	if i == m.panelSel {
		return m.th.acSel.Render("› " + label)
	}
	return m.th.acItem.Render("  " + label)
}

// ── events tab ──────────────────────────────────────────────────────────────

func (m *Model) openEvents() tea.Cmd {
	m.panel = panelEvents
	m.panelSel = 0
	m.panelEdit = panelEditNone
	m.panelMsg = "loading events…"
	m.relayout()
	m.refresh()
	return m.fetchEvents()
}

func (m *Model) fetchEvents() tea.Cmd {
	cl := m.cl
	sid := ""
	if m.evSessionFilter {
		sid = m.sessionID
	}
	return func() tea.Msg {
		evs, err := cl.RuntimeEvents(100, "", sid)
		return eventsMsg{events: evs, err: err}
	}
}

// toggleEventFilter flips the this-session filter and refetches.
func (m *Model) toggleEventFilter() tea.Cmd {
	m.evSessionFilter = !m.evSessionFilter
	return m.fetchEvents()
}

func (m *Model) handleEventsMsg(msg eventsMsg) {
	if msg.err != nil {
		m.panelMsg = "error: " + msg.err.Error()
		return
	}
	m.feed = msg.events
	if len(m.feed) == 0 {
		m.panelMsg = "no runtime events yet — every WS prompt and REST run feeds this ring"
	} else {
		m.panelMsg = ""
	}
}

func (m *Model) eventRows(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.feed))
	// Newest last (server order is oldest-first) so the ring reads like a log
	// tail; the selection windows around whatever the user picks.
	for _, ev := range m.feed {
		ts := ""
		if !ev.Timestamp.IsZero() {
			ts = ev.Timestamp.Format("15:04:05")
		}
		label := ts + "  " + ev.Type
		if ev.Tool != "" {
			label += " · " + ev.Tool
		}
		if ev.Iteration > 0 {
			label += fmt.Sprintf(" · iter %d", ev.Iteration)
		}
		detail := ""
		if ev.SessionID != "" {
			detail = "  " + shortID(ev.SessionID)
		}
		rows = append(rows, th.acItem.Render("  "+truncate(label, w-2-lipgloss.Width(detail)))+th.acDetail.Render(detail))
	}
	return rows
}
