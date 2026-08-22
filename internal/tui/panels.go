package tui

import (
	"fmt"
	"os"
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
	panelRuns
	panelEvents
	panelMemory
	panelSkills
	panelTools
	panelConfig
)

// panelEditMode is the text-entry submode a panel can capture: `/` search in
// the sessions panel, `r` rename.
type panelEditMode int

const (
	panelEditNone panelEditMode = iota
	panelEditSearch
	panelEditRename
)

// confirmKind arms a destructive action: pressing d/x arms it, y confirms,
// any other key disarms. Row deletes never fire on a single keypress —
// the same anti-fatigue discipline as the shutdown death-gate, scaled to
// row scope.
type confirmKind int

const (
	confirmNone confirmKind = iota
	confirmSessionDelete
	confirmFactDelete
)

// handleConfirmKey resolves an armed delete: y fires it against the
// highlighted row; everything else disarms.
func (m *Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	kind := m.confirm
	m.confirm = confirmNone
	m.panelMsg = ""
	switch msg.String() {
	case "y", "Y":
		switch kind {
		case confirmSessionDelete:
			return m, m.deleteSelected()
		case confirmFactDelete:
			return m, m.memDeleteSelected()
		}
	}
	m.refresh()
	return m, nil
}

// armConfirm arms a row-scoped delete and shows the gate in the panel.
func (m *Model) armConfirm(kind confirmKind, what string) tea.Cmd {
	m.confirm = kind
	m.panelMsg = "delete " + what + "?  y confirm · any other key cancels"
	m.refresh()
	return nil
}

// sessPageSize is the sessions-panel page size (server caps limit at 200).
const sessPageSize = 50

// ── async results ────────────────────────────────────────────────────────────

type sessionsMsg struct {
	items []client.Session
	err   error
}

// sessionsPageMsg carries one server-side search/pagination result. append is
// true for "load more" fetches, false for fresh queries.
type sessionsPageMsg struct {
	page   client.SessionsPage
	append bool
	err    error
}

type modelsMsg struct {
	items []client.ModelInfo
	err   error
}

// pickerMsg carries the model picker's combined fetch: the configured model
// and the built-in profile catalog (profiles failing is soft).
type pickerMsg struct {
	models   []client.ModelInfo
	profiles []client.Profile
	err      error
}

type profilesMsg struct {
	items []client.Profile
	err   error
}

type limitsMsg struct {
	resp client.LimitsResponse
	err  error
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

// sessionUpdatedMsg reports a pin/unpin outcome (pinned is the new state).
type sessionUpdatedMsg struct {
	id     string
	pinned bool
	err    error
}

// sessionRenamedMsg reports a rename outcome.
type sessionRenamedMsg struct {
	id   string
	name string
	err  error
}

// sessionExportedMsg reports where an exported transcript was written.
type sessionExportedMsg struct {
	id   string
	path string
	err  error
}

// sessionSwitchMsg reports a failed session_switch (adopt without prompting).
type sessionSwitchMsg struct{ err error }

type cancelDoneMsg struct{ err error }

// ── opening panels ───────────────────────────────────────────────────────────

func (m *Model) openSessions() tea.Cmd {
	m.panel = panelSessions
	m.panelSel = 0
	m.sessQuery = ""
	m.panelEdit = panelEditNone
	m.panelDraft = ""
	m.panelMsg = "loading sessions…"
	m.relayout()
	m.refresh()
	return m.fetchSessionsPage("", 0, false)
}

// fetchSessionsPage runs one server-side session search. Query is scoped per
// call (the search box edits panelDraft until enter applies it).
func (m *Model) fetchSessionsPage(query string, offset int, appendMode bool) tea.Cmd {
	cl := m.cl
	return func() tea.Msg {
		page, err := cl.SearchSessions(query, sessPageSize, offset)
		return sessionsPageMsg{page: page, append: appendMode, err: err}
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
		// One picker fetch: the configured model plus the built-in catalog.
		// A profiles failure is soft — the picker shows the configured model.
		items, err := cl.Models()
		msg := pickerMsg{models: items, err: err}
		msg.profiles, _ = cl.Profiles()
		return msg
	}
}

func (m *Model) closePanel() {
	m.panel = panelNone
	m.panelMsg = ""
	m.panelEdit = panelEditNone
	m.panelDetail = false
	m.detailScroll = 0
	m.confirm = confirmNone
	m.relayout()
	m.refresh()
}

// ── key handling ─────────────────────────────────────────────────────────────

func (m *Model) handlePanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Text-entry submodes capture everything except quit.
	if m.panelEdit != panelEditNone {
		return m.handlePanelEditKey(msg)
	}
	// Detail submode: scrolling, folding, and the in-place promote; the
	// drawer tab keys (] [ and digits) fall through and switch tabs, which
	// resets the detail via the open* constructors.
	if m.panelDetail && mgmtPanel(m.panel) {
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "esc", "q":
			m.closeDetail()
			return m, nil
		case "up", "ctrl+p", "k":
			if m.detailScroll > 0 {
				m.detailScroll--
				m.refresh()
			}
			return m, nil
		case "down", "ctrl+n", "j":
			if m.detailScroll < m.detailMaxScroll() {
				m.detailScroll++
				m.refresh()
			}
			return m, nil
		case "p":
			if m.panel == panelSkills {
				return m, m.skillPromote(false)
			}
			if m.panel == panelMemory {
				return m, m.memPromoteSelected()
			}
			return m, nil
		case "P":
			if m.panel == panelSkills {
				return m, m.skillPromote(true)
			}
			return m, nil
		}
		// Everything else is swallowed by the detail view — except the
		// drawer navigation keys, which fall through to switch tabs.
		s := msg.String()
		drawerNav := s == "]" || s == "[" || s == "left" || s == "right" ||
			(len(s) == 1 && s[0] >= '1' && s[0] <= '9')
		if !drawerNav {
			return m, nil
		}
	}
	// Drawer-level keys: tab cycling and digit jumps work on every drawer tab.
	if drawerPanel(m.panel) {
		tabs := drawerTabs()
		switch msg.String() {
		case "]", "right":
			return m, m.cycleDrawerTab(1)
		case "[", "left":
			return m, m.cycleDrawerTab(-1)
		}
		if d := msg.String(); len(d) == 1 && d[0] >= '1' && d[0] <= '9' {
			if idx := int(d[0] - '1'); idx < len(tabs) {
				return m, m.switchDrawerTab(tabs[idx].mode)
			}
		}
	}
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
	case "a":
		if m.panel == panelRuns {
			return m, m.answerSelectedRunApproval("approve")
		}
		if m.panel == panelMemory {
			m.panelEdit = panelEditFact
			m.memTarget = "user"
			m.panelDraft = ""
			m.refresh()
		}
		return m, nil
	case "A":
		if m.panel == panelRuns {
			return m, m.answerSelectedRunApproval("approve")
		}
		if m.panel == panelMemory {
			m.panelEdit = panelEditFact
			m.memTarget = "env"
			m.panelDraft = ""
			m.refresh()
		}
		return m, nil
	case "d", "x":
		if m.panel == panelEvents {
			// No deletes here — x doubles as the filter-clear key.
			return m, m.clearEventFilters()
		}
		if m.panel == panelSessions {
			if m.panelSel < len(m.sessions) {
				return m, m.armConfirm(confirmSessionDelete, "session "+shortID(m.sessions[m.panelSel].ID))
			}
			return m, nil
		}
		if m.panel == panelMemory {
			if r := m.memSelected(); r != nil && r.kind != "episode" {
				return m, m.armConfirm(confirmFactDelete, r.kind+" fact")
			}
			return m, nil
		}
		if m.panel == panelConfig {
			return m, m.cfgKickSelected()
		}
	case "D":
		if m.panel == panelRuns {
			return m, m.answerSelectedRunApproval("deny")
		}
	case "t", "T":
		if m.panel == panelRuns {
			return m, m.answerSelectedRunApproval("trust")
		}
	case "c":
		if m.panel == panelRuns {
			return m, m.cancelSelectedRun()
		}
		if m.panel == panelMemory {
			return m, m.memConsolidate("user")
		}
	case "f", "F":
		if m.panel == panelEvents {
			return m, m.toggleEventFilter()
		}
	case "/":
		if m.panel == panelSessions {
			m.panelEdit = panelEditSearch
			m.panelDraft = m.sessQuery
			m.refresh()
		}
		return m, nil
	case "p":
		if m.panel == panelSessions {
			return m, m.togglePinSelected()
		}
		if m.panel == panelRuns {
			return m, m.refreshSelectedRunApprovals()
		}
		if m.panel == panelMemory {
			return m, m.memPromoteSelected()
		}
		if m.panel == panelSkills {
			return m, m.skillPromote(false)
		}
	case "P":
		if m.panel == panelSkills {
			return m, m.skillPromote(true)
		}
	case "s", "S":
		if m.panel == panelConfig {
			return m, m.startShutdownConfirm()
		}
	case "e":
		if m.panel == panelSessions {
			return m, m.exportSelected("md")
		}
		if m.panel == panelRuns {
			// Drill into the highlighted run's event trail.
			return m, m.drillIntoRunEvents()
		}
	case "E":
		if m.panel == panelSessions {
			return m, m.exportSelected("json")
		}
		if m.panel == panelMemory {
			return m, m.memConsolidate("env")
		}
	case "r":
		if m.panel == panelSessions {
			if m.panelSel < len(m.sessions) {
				m.panelEdit = panelEditRename
				m.panelDraft = m.sessions[m.panelSel].Task
				m.refresh()
			}
			return m, nil
		}
		if drawerPanel(m.panel) {
			m.panelMsg = "refreshing…"
			m.refresh()
			switch m.panel {
			case panelRuns:
				return m, m.fetchRuns()
			case panelEvents:
				return m, m.fetchEvents()
			default:
				return m, m.switchDrawerTab(m.panel)
			}
		}
		return m, nil
	case "n":
		if m.panel == panelSessions && m.sessHasMore {
			m.panelMsg = "loading more…"
			m.refresh()
			return m, m.fetchSessionsPage(m.sessQuery, len(m.sessions), true)
		}
		return m, nil
	}
	return m, nil
}

// handlePanelEditKey edits the in-panel text draft (search query or rename).
// Enter applies it; esc abandons the edit keeping any applied state.
func (m *Model) handlePanelEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.panelEdit = panelEditNone
		m.panelDraft = ""
		m.refresh()
		return m, nil
	case "enter":
		// The shutdown gate manages its own lifecycle: a mistyped word must
		// hold the gate open (retyping is the point), not exit the editor.
		if m.panelEdit == panelEditShutdown {
			return m, m.confirmShutdown()
		}
		mode, draft := m.panelEdit, m.panelDraft
		m.panelEdit = panelEditNone
		m.panelDraft = ""
		switch mode {
		case panelEditSearch:
			m.sessQuery = strings.TrimSpace(draft)
			m.panelSel = 0
			m.panelMsg = "searching…"
			m.refresh()
			return m, m.fetchSessionsPage(m.sessQuery, 0, false)
		case panelEditRename:
			return m, m.renameSelected(strings.TrimSpace(draft))
		case panelEditFact:
			return m, m.memFactDraft()
		case panelEditShutdown:
			return m, m.confirmShutdown()
		}
		return m, nil
	case "backspace":
		if n := len(m.panelDraft); n > 0 {
			m.panelDraft = m.panelDraft[:n-1]
		}
		m.refresh()
		return m, nil
	case "space":
		m.panelDraft += " "
		m.refresh()
		return m, nil
	default:
		// Single printable runes only — ctrl/alt combos must not splice
		// escape bytes into the draft.
		if s := msg.String(); len([]rune(s)) == 1 {
			m.panelDraft += s
			m.refresh()
		}
		return m, nil
	}
}

func (m *Model) panelLen() int {
	switch m.panel {
	case panelSessions:
		return len(m.sessions)
	case panelModels:
		return len(m.modelEntries())
	case panelRuns:
		return len(m.runs)
	case panelEvents:
		return len(m.feed)
	case panelMemory:
		return len(m.memRows)
	case panelSkills:
		return len(m.skills)
	case panelTools:
		return len(m.toolRows)
	case panelConfig:
		return len(m.cfgRows)
	}
	return 0
}

// modelEntry is one row of the model picker: the server's configured model
// plus the built-in profile catalog (ids are model-id prefixes).
type modelEntry struct {
	id      string
	label   string
	detail  string
	current bool
}

// modelEntries merges /api/models (the configured model, with its window)
// with /api/profiles (the built-in catalog), configured first, duplicates
// skipped, each annotated with its context size.
func (m *Model) modelEntries() []modelEntry {
	out := make([]modelEntry, 0, len(m.models)+len(m.profiles))
	for _, md := range m.models {
		e := modelEntry{id: md.ID, label: md.ID, current: md.Current}
		if md.MaxContext > 0 {
			e.detail = fmt.Sprintf("%s ctx", humanCtx(md.MaxContext))
		}
		if md.Description != "" {
			e.detail = strings.TrimSpace(md.Description + "  " + e.detail)
		}
		out = append(out, e)
	}
	for _, p := range m.profiles {
		dup := false
		for _, md := range m.models {
			if md.ID == p.ID {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		label := p.Label
		if label == "" {
			label = p.ID
		}
		e := modelEntry{id: p.ID, label: label}
		if p.MaxContext > 0 {
			e.detail = fmt.Sprintf("%s ctx", humanCtx(p.MaxContext))
		}
		if p.ID == m.model {
			e.current = true
		}
		out = append(out, e)
	}
	return out
}

func (m *Model) panelSelect() tea.Cmd {
	switch m.panel {
	case panelSessions:
		if m.panelSel < len(m.sessions) {
			return m.resumeSession(m.sessions[m.panelSel].ID)
		}
	case panelModels:
		entries := m.modelEntries()
		if m.panelSel < len(entries) {
			e := entries[m.panelSel]
			m.pendModel = e.id
			m.model = e.id
			m.resolveMaxContext()
			note := m.transientNoteCmd("model set to " + e.id + " (applies next turn)")
			m.closePanel()
			return note
		}
	case panelRuns:
		// Enter refreshes the highlighted run's detail (result tail and the
		// pending-approval queue).
		if r := m.selectedRun(); r != nil {
			m.panelMsg = "refreshing…"
			m.refresh()
			return m.fetchRuns()
		}
	case panelEvents:
		return m.fetchEvents()
	case panelMemory, panelSkills, panelTools, panelConfig:
		// Enter expands the selected row into its detail view — the
		// promote/delete gates assume the human can read what they gate.
		if m.panelLen() == 0 {
			return nil
		}
		m.panelDetail = true
		m.detailScroll = 0
		m.refresh()
		return nil
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

// togglePinSelected pins/unpins the highlighted session (POST {pinned}).
func (m *Model) togglePinSelected() tea.Cmd {
	if m.panelSel >= len(m.sessions) {
		return nil
	}
	s := m.sessions[m.panelSel]
	cl := m.cl
	token := m.tokens.Get(s.ID)
	id, next := s.ID, !s.Pinned
	return func() tea.Msg {
		_, eff, err := cl.SessionDetail(id, token)
		if err != nil {
			return sessionUpdatedMsg{id: id, err: err}
		}
		return sessionUpdatedMsg{id: id, pinned: next, err: cl.UpdateSession(id, eff, nil, &next)}
	}
}

// renameSelected applies a new label to the highlighted session (POST {name}).
func (m *Model) renameSelected(name string) tea.Cmd {
	if m.panelSel >= len(m.sessions) || name == "" {
		return nil
	}
	s := m.sessions[m.panelSel]
	cl := m.cl
	token := m.tokens.Get(s.ID)
	id := s.ID
	return func() tea.Msg {
		_, eff, err := cl.SessionDetail(id, token)
		if err != nil {
			return sessionRenamedMsg{id: id, err: err}
		}
		return sessionRenamedMsg{id: id, name: name, err: cl.UpdateSession(id, eff, &name, nil)}
	}
}

// exportSelected downloads the highlighted session's transcript and writes it
// next to the user (markdown by default, raw JSON with shift-E).
func (m *Model) exportSelected(format string) tea.Cmd {
	if m.panelSel >= len(m.sessions) {
		return nil
	}
	s := m.sessions[m.panelSel]
	cl := m.cl
	token := m.tokens.Get(s.ID)
	id := s.ID
	return func() tea.Msg {
		_, eff, err := cl.SessionDetail(id, token)
		if err != nil {
			return sessionExportedMsg{id: id, err: err}
		}
		data, err := cl.ExportSession(id, eff, format)
		if err != nil {
			return sessionExportedMsg{id: id, err: err}
		}
		path := fmt.Sprintf("bodek-%s.%s", id, format)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return sessionExportedMsg{id: id, err: err}
		}
		return sessionExportedMsg{id: id, path: path}
	}
}

// cancelRun aborts the in-flight prompt: the WebSocket cancel first (the
// server answers with a cancelled event and the run's trailing error turns
// into a clean cancel marker), with the REST endpoint as fallback when the
// socket write itself fails. Queued prompts belong to the user, not the
// cancelled turn: hand them back to the input for editing instead of firing
// them into a cancelled session.
func (m *Model) cancelRun() tea.Cmd {
	if !m.busy || m.sessionID == "" {
		cmd := m.transientNoteCmd("nothing to cancel")
		m.refresh()
		return cmd
	}
	var note tea.Cmd
	if len(m.queue) > 0 {
		draft := strings.Join(m.queue, "\n")
		if cur := m.ta.Value(); cur != "" {
			draft = cur + "\n" + draft
		}
		m.ta.SetValue(draft)
		m.ta.CursorEnd()
		m.queue = nil
		// The textarea content just changed out from under the user — say why.
		note = m.transientNoteCmd("queued prompts returned to the input")
	}
	m.status = "cancelling"
	m.refresh()
	cl := m.cl
	sid, tok := m.sessionID, m.authToken
	return tea.Batch(note, func() tea.Msg {
		if err := cl.SendCancel(sid, tok); err == nil {
			return nil // the cancelled event settles the turn
		}
		return cancelDoneMsg{err: cl.Cancel(sid, tok)}
	})
}

// adoptSession switches the live connection to a session (restores the
// server-side memory buffer) without sending a prompt. The server replies
// with the standard session event, which re-syncs local state.
func (m *Model) adoptSession() tea.Cmd {
	if m.sessionID == "" || m.cl == nil {
		return nil
	}
	cl := m.cl
	sid, tok := m.sessionID, m.authToken
	return func() tea.Msg {
		if err := cl.SessionSwitch(sid, tok); err != nil {
			return sessionSwitchMsg{err: err}
		}
		return nil
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

// handleSessionsPageMsg applies a search/pagination envelope: replace on
// fresh queries, append on "load more". A full page means more may follow.
func (m *Model) handleSessionsPageMsg(msg sessionsPageMsg) {
	if msg.err != nil {
		m.panelMsg = "error: " + msg.err.Error()
		return
	}
	if msg.append {
		m.sessions = append(m.sessions, msg.page.Sessions...)
	} else {
		m.sessions = msg.page.Sessions
		if m.panelSel >= len(m.sessions) {
			m.panelSel = 0
		}
	}
	m.sessHasMore = msg.page.Count >= msg.page.Limit && msg.page.Count > 0
	switch {
	case len(m.sessions) == 0:
		if m.sessQuery != "" {
			m.panelMsg = "no matches for “" + m.sessQuery + "”"
		} else {
			m.panelMsg = "no saved sessions yet"
		}
	default:
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
	m.resolveMaxContext() // now that the budget is known, the header gauge can show
	if len(m.models) == 0 {
		m.panelMsg = "no models advertised"
	} else {
		m.panelMsg = ""
	}
}

// handleProfilesMsg stores the built-in catalog. Errors are soft: the picker
// simply lists only the configured model, and the gauge falls back to
// /api/models.
func (m *Model) handleProfilesMsg(msg profilesMsg) {
	if msg.err != nil {
		return
	}
	m.profiles = msg.items
	m.resolveMaxContext()
}

// handleLimitsMsg stores the server's budget limits and token prices. Errors
// are swallowed: odek never hard-codes prices, so when they are unknown the
// cost display simply stays hidden rather than reporting $0.
func (m *Model) handleLimitsMsg(msg limitsMsg) {
	if msg.err != nil {
		return
	}
	m.limits = msg.resp.Limits
	m.serverModel = msg.resp.Model
	m.effectivePrx = msg.resp.EffectivePrices
}

func (m *Model) handleSessionDetail(msg sessionDetailMsg) tea.Cmd {
	if msg.err != nil {
		m.panelMsg = "error: " + msg.err.Error()
		return nil
	}
	// Replay the saved transcript into the local view, then adopt the session
	// on the connection (session_switch restores the server-side memory
	// buffer; the reply's session event re-syncs state).
	m.sessionID = msg.sess.ID
	m.authToken = msg.token
	m.tokens.Set(msg.sess.ID, msg.token)
	if msg.sess.Model != "" {
		m.model = msg.sess.Model
		m.resolveMaxContext()
	}
	m.sandbox = msg.sess.Sandbox
	// Resuming a session swaps in a fresh transcript, so the session-scoped
	// telemetry must reset too — otherwise /stats, the header gauge, and the
	// footer would report the previous session's (monotonically accumulating)
	// turns, tools, tokens, and age. The next done event repopulates them.
	m.turnStats = nil
	m.toolTotal = 0
	m.sessionStart = time.Time{}
	m.sessCtxTok = 0
	m.sessOutTok = 0
	m.winCtxTok = 0
	m.runCtxCum = 0
	m.lastLatency = 0
	m.msgs = m.msgs[:0]
	m.convCount = -1 // transcript swapped for the resumed one — drop the cache
	m.replayTranscript(msg.sess.Messages)
	note := m.transientNoteCmd("resumed session " + shortID(msg.sess.ID))
	m.closePanel()
	return tea.Batch(note, m.adoptSession())
}

// handleSessionUpdated applies a pin/unpin outcome to the list in place.
func (m *Model) handleSessionUpdated(msg sessionUpdatedMsg) tea.Cmd {
	if msg.err != nil {
		m.panelMsg = "pin failed: " + msg.err.Error()
		m.refresh()
		return nil
	}
	for i := range m.sessions {
		if m.sessions[i].ID == msg.id {
			m.sessions[i].Pinned = msg.pinned
			break
		}
	}
	m.refresh()
	return nil
}

// handleSessionRenamed applies a rename outcome to the list in place.
func (m *Model) handleSessionRenamed(msg sessionRenamedMsg) tea.Cmd {
	if msg.err != nil {
		m.panelMsg = "rename failed: " + msg.err.Error()
		m.refresh()
		return nil
	}
	for i := range m.sessions {
		if m.sessions[i].ID == msg.id {
			m.sessions[i].Task = msg.name
			break
		}
	}
	m.refresh()
	return nil
}

// handleSessionExported reports where the transcript landed.
func (m *Model) handleSessionExported(msg sessionExportedMsg) tea.Cmd {
	if msg.err != nil {
		m.panelMsg = "export failed: " + msg.err.Error()
		m.refresh()
		return nil
	}
	cmd := m.transientNoteCmd("exported " + msg.path)
	m.refresh()
	return cmd
}

// handleSessionSwitch surfaces a failed adopt; success stays quiet (the
// session event reply speaks for itself).
func (m *Model) handleSessionSwitch(msg sessionSwitchMsg) tea.Cmd {
	if msg.err != nil {
		cmd := m.transientNoteCmd("session switch failed: " + msg.err.Error())
		m.refresh()
		return cmd
	}
	return nil
}

// replayTranscript rebuilds a saved transcript turn by turn so a resumed
// session renders identically to a live one: a user message opens a turn,
// and everything up to the next user message accumulates into a single
// assistant message — reasoning blocks, reply segments, tool calls/results
// interleaved in arrival order, mirroring live event ingestion. System
// messages are dropped; blank assistant messages (no reply, reasoning, or
// steps) are skipped.
func (m *Model) replayTranscript(msgs []client.SessionMessage) {
	var cur *message // current turn's assistant message, not yet flushed
	stepByCallID := map[string]int{}

	// flush closes the current assistant message like finalize() does and
	// flushes it into the transcript.
	flush := func() {
		if cur == nil {
			return
		}
		m.closeTurn(cur)
		if strings.TrimSpace(cur.content) != "" || len(cur.items) > 0 {
			m.msgs = append(m.msgs, *cur)
		}
		cur = nil
		stepByCallID = map[string]int{}
	}

	for _, mm := range msgs {
		// Persisted transcripts are attacker-influenced (agent output, and the
		// session file itself); strip terminal control sequences before display.
		switch mm.Role {
		case "user":
			flush()
			m.msgs = append(m.msgs, message{role: roleUser, content: sanitize(mm.Content)})
		case "assistant":
			if cur == nil {
				cur = &message{role: roleAsst}
			}
			if rc := sanitize(mm.ReasoningContent); strings.TrimSpace(rc) != "" {
				// Full text, like live turns — the rendered excerpt is capped
				// at render time, expandAll unfolds the whole block.
				cur.items = append(cur.items, turnItem{thinking: true, text: rc})
			}
			if c := sanitize(mm.Content); strings.TrimSpace(c) != "" {
				// One reply segment per persisted assistant record — the same
				// think→reply pairing live turns build from token events.
				appendReply(cur, c)
			}
			for _, tc := range mm.ToolCalls {
				name := tc.Function.Name
				cur.steps = append(cur.steps, step{
					name:     name,
					arg:      argPreview(tc.Function.Arguments),
					subagent: isSubagent(name),
				})
				stepByCallID[tc.ID] = len(cur.steps) - 1
				cur.items = append(cur.items, turnItem{stepIdx: len(cur.steps) - 1})
			}
		case "tool":
			if cur == nil {
				continue // a tool result with no assistant message to attach to
			}
			// Persisted results are wrapped in odek's prompt-injection frame;
			// live tool_result events carry the raw output — strip it so both
			// render identically. resultPreview sanitizes the unwrapped output.
			result := resultPreview(stripToolResultFrame(mm.Content))
			// Match by tool_call_id first; fall back to the live tool_result
			// behavior of scanning backwards by name for an unfinished step.
			idx, ok := stepByCallID[mm.ToolCallID]
			if ok && cur.steps[idx].done {
				ok = false
			}
			if !ok {
				for j := len(cur.steps) - 1; j >= 0; j-- {
					if cur.steps[j].name == mm.Name && !cur.steps[j].done {
						idx, ok = j, true
						break
					}
				}
			}
			if !ok {
				continue
			}
			cur.steps[idx].done = true
			cur.steps[idx].result = result
			cur.steps[idx].isErr = looksLikeError(result)
		}
	}
	flush()
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
		title = "⟳ sessions"
		rows = m.sessionRows(w - 6)
	case panelModels:
		title = "✦ choose a model"
		rows = m.modelRows(w - 6)
	case panelRuns:
		title = "▶ headless runs"
		rows = m.runRows(w - 6)
	case panelEvents:
		title = "☰ events"
		if m.evRunFilter != "" {
			title += th.acDetail.Render("  ·  run " + shortID(m.evRunFilter))
		} else if m.evSessionFilter {
			title += th.acDetail.Render("  ·  this session")
		}
		rows = m.eventRows(w - 6)
	case panelMemory:
		title = "❖ memory"
		rows = m.memRowsRender(w - 6)
	case panelSkills:
		title = "✦ skills"
		rows = m.skillRowsRender(w - 6)
	case panelTools:
		title = "⚒ tools"
		rows = m.toolRowsRender(w - 6)
	case panelConfig:
		title = "⚙ config"
		rows = m.cfgRowsRender(w - 6)
	}

	header := th.acTitle.Render(title)
	if drawerPanel(m.panel) {
		header += m.tabBar(w - 4 - lipgloss.Width(title))
	}
	if m.panel == panelSessions && m.sessQuery != "" {
		header += th.acDetail.Render("  /" + m.sessQuery)
	}
	body := header
	if m.panelEdit != panelEditNone {
		// The live draft line doubles as the panel's status while editing.
		prompt := "search"
		if m.panelEdit == panelEditRename {
			prompt = "rename"
		}
		body += "\n" + th.acSel.Render(prompt+": "+m.panelDraft+"▏")
	}
	if m.panelMsg != "" {
		body += "\n" + th.acDim.Render(m.panelMsg)
	}
	if m.panelDetail && mgmtPanel(m.panel) {
		// Detail view replaces the list: window the wrapped block by the
		// scroll offset, clamped so the final line stays reachable.
		lines := m.mgmtDetailLines(w - 8)
		visible := h - 5 // border(2) + title(1) + breathing room
		if visible < 1 {
			visible = 1
		}
		if m.detailScroll > max(len(lines)-visible, 0) {
			m.detailScroll = max(len(lines)-visible, 0)
		}
		win := lines
		if len(lines) > visible {
			win = lines[m.detailScroll : m.detailScroll+visible]
		}
		body += "\n" + strings.Join(win, "\n")
	} else if len(rows) > 0 {
		// Window the rows around the selection to fit the available height.
		visible := h - 4 // border(2) + title(1) + breathing room
		if m.panelEdit != panelEditNone {
			visible-- // the draft line claims one
		}
		if visible < 1 {
			visible = 1
		}
		sel := m.panelSel
		if m.panel == panelSkills {
			sel = m.skillSelRow() // description lines shift visual rows
		}
		body += "\n" + strings.Join(windowRows(rows, sel, visible), "\n")
	}

	// acBox is exactly the rounded brand box this panel used to hand-build.
	return th.acBox.Width(w - 2).Height(h - 2).Render(body)
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
		if s.InputTokens > 0 || s.OutputTokens > 0 {
			meta += fmt.Sprintf(" · ⇥%s ↦%s", human(int(s.InputTokens)), human(int(s.OutputTokens)))
		}
		if s.Pinned {
			task = "📌 " + task
		}
		budget := w - 2
		task = truncate(collapse(task), budget-lipgloss.Width(meta))
		prefix, label := "  ", th.acItem.Render(task)
		if i == m.panelSel {
			if m.confirm == confirmSessionDelete {
				prefix, label = th.badgeDanger.Render("⚠ "), th.badgeDanger.Render(task)
			} else {
				prefix, label = th.acSel.Render("› "), th.acSel.Render(task)
			}
		}
		rows = append(rows, prefix+label+th.acDetail.Render(meta))
	}
	return rows
}

func (m *Model) modelRows(w int) []string {
	th := m.th
	entries := m.modelEntries()
	rows := make([]string, 0, len(entries))
	for i, e := range entries {
		label := e.label
		detail := ""
		if e.detail != "" {
			detail = "  " + e.detail
		}
		if e.current {
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
