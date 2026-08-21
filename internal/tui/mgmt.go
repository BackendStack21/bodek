package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── management tabs: memory · skills · tools · config ───────────────────────
//
// The remaining REST surface lands as drawer tabs with the same grammar as
// sessions/runs/events. Each tab is rows + a small action set; provenance and
// trust gates render as badges, never bare colors.

// mgmtMsg carries one management fetch. tab routes it.
type mgmtMsg struct {
	tab  panelMode
	mem  client.MemoryView
	skl  []client.Skill
	tls  []client.Tool
	mcpN int
	mcp  []client.MCPServer
	cfg  map[string]any
	usr  client.Usage
	con  []client.Connection
	err  error
}

// memRow is one selectable memory row.
type memRow struct {
	kind      string // "user" | "env" | "episode"
	text      string
	sessionID string // episodes: promote target
}

// toolRow is one selectable tools/config row.
type toolRow struct {
	kind string // "tool" | "mcp"
	text string
	dim  string
	id   string // mcp: server name
}

// cfgRow is one config/usage/connection row.
type cfgRow struct {
	kind string // "cfg" | "usage" | "conn"
	k    string
	v    string
	id   string // conn: kickable id
}

// mgmtActionMsg reports a mutation outcome (delete/promote/consolidate/
// add/kick); success refetches the tab.
type mgmtActionMsg struct {
	tab panelMode
	err error
}

// panelEditMode for the memory add-fact editor.
const panelEditFact panelEditMode = 3

// panelEditShutdown is the typed death-gate for POST /api/shutdown — the
// literal word "shutdown", exactly like the approval friction gate: killing
// the server (possibly the one bodek spawned and rides) must never be one
// accidental keypress away.
const panelEditShutdown panelEditMode = 4

// shutdownDoneMsg reports the shutdown request outcome.
type shutdownDoneMsg struct{ err error }

// startShutdownConfirm opens the typed confirmation on the config tab.
func (m *Model) startShutdownConfirm() tea.Cmd {
	m.panelEdit = panelEditShutdown
	m.panelDraft = ""
	m.refresh()
	return nil
}

// confirmShutdown fires the request once the typed word matches.
func (m *Model) confirmShutdown() tea.Cmd {
	if strings.TrimSpace(m.panelDraft) != "shutdown" {
		m.panelDraft = "" // a mistyped word resets — retyping is the point
		m.refresh()
		return nil
	}
	m.panelEdit = panelEditNone
	m.panelDraft = ""
	m.shutdownReq = true // the socket drop that follows is expected
	m.refresh()
	cl := m.cl
	return func() tea.Msg { return shutdownDoneMsg{err: cl.Shutdown()} }
}

func (m *Model) openMemory() tea.Cmd {
	m.panel = panelMemory
	m.panelSel = 0
	m.panelEdit = panelEditNone
	m.panelMsg = "loading memory…"
	m.relayout()
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		mem, err := cl.Memory()
		return mgmtMsg{tab: panelMemory, mem: mem, err: err}
	}
}

func (m *Model) openSkills() tea.Cmd {
	m.panel = panelSkills
	m.panelSel = 0
	m.panelMsg = "loading skills…"
	m.relayout()
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		skl, err := cl.Skills()
		return mgmtMsg{tab: panelSkills, skl: skl, err: err}
	}
}

func (m *Model) openTools() tea.Cmd {
	m.panel = panelTools
	m.panelSel = 0
	m.panelMsg = "loading tools…"
	m.relayout()
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		tls, n, err := cl.Tools()
		msg := mgmtMsg{tab: panelTools, tls: tls, mcpN: n, err: err}
		msg.mcp, _ = cl.MCPServers()
		return msg
	}
}

func (m *Model) openConfig() tea.Cmd {
	m.panel = panelConfig
	m.panelSel = 0
	m.panelMsg = "loading config…"
	m.relayout()
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		cfg, err := cl.ConfigView()
		msg := mgmtMsg{tab: panelConfig, cfg: cfg, err: err}
		msg.usr, _ = cl.Usage()
		msg.con, _ = cl.Connections()
		return msg
	}
}

func (m *Model) handleMgmtMsg(msg mgmtMsg) {
	if m.panel != msg.tab {
		return
	}
	if msg.err != nil {
		m.panelMsg = "error: " + msg.err.Error()
		return
	}
	switch msg.tab {
	case panelMemory:
		m.memView = msg.mem
		m.memRows = buildMemRows(msg.mem)
		if len(m.memRows) == 0 {
			m.panelMsg = "no facts or pending episodes"
		} else {
			m.panelMsg = ""
		}
	case panelSkills:
		m.skills = msg.skl
		if len(msg.skl) == 0 {
			m.panelMsg = "no skills discovered"
		} else {
			m.panelMsg = ""
		}
	case panelTools:
		m.toolRows = buildToolRows(msg.tls, msg.mcp)
		if len(m.toolRows) == 0 {
			m.panelMsg = "no tools registered"
		} else {
			m.panelMsg = fmt.Sprintf("%d built-ins · %d MCP servers", len(msg.tls), msg.mcpN)
		}
	case panelConfig:
		m.cfgRows = buildCfgRows(msg.cfg, msg.usr, msg.con)
		m.panelMsg = ""
	}
	if m.panelSel >= m.panelLen() {
		m.panelSel = max(m.panelLen()-1, 0)
	}
}

func buildMemRows(v client.MemoryView) []memRow {
	var rows []memRow
	for _, target := range []string{"user", "env"} {
		for _, f := range v.Facts[target] {
			rows = append(rows, memRow{kind: target, text: f})
		}
	}
	for _, e := range v.Episodes.Pending {
		rows = append(rows, memRow{kind: "episode", text: e.Summary, sessionID: e.SessionID})
	}
	return rows
}

func buildToolRows(tools []client.Tool, servers []client.MCPServer) []toolRow {
	rows := make([]toolRow, 0, len(tools)+len(servers))
	for _, t := range tools {
		state := "on"
		if !t.Enabled {
			state = "off"
		}
		rows = append(rows, toolRow{kind: "tool", text: t.Name, dim: state})
	}
	for _, s := range servers {
		detail := s.Command
		if s.Project {
			detail += " · project"
		}
		if s.AutoApprove {
			detail += " · auto-approve"
		}
		rows = append(rows, toolRow{kind: "mcp", text: s.Name, dim: detail, id: s.Name})
	}
	return rows
}

func buildCfgRows(cfg map[string]any, usage client.Usage, conns []client.Connection) []cfgRow {
	var rows []cfgRow
	rows = append(rows, cfgRow{kind: "usage", k: "prompts", v: fmt.Sprintf("%d started · %d completed · %d failed",
		usage.PromptsStarted, usage.PromptsCompleted, usage.PromptsFailed)})
	rows = append(rows, cfgRow{kind: "usage", k: "tokens", v: fmt.Sprintf("⇥%s ↦%s", human(int(usage.TokensIn)), human(int(usage.TokensOut)))})
	if usage.PricesConfigured {
		rows = append(rows, cfgRow{kind: "usage", k: "lifetime cost", v: formatUSD(usage.EstimatedCostUSD)})
	}
	if cfg != nil {
		keys := make([]string, 0, len(cfg))
		for k := range cfg {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			rows = append(rows, cfgRow{kind: "cfg", k: k, v: scalarOrMarker(cfg[k])})
		}
	}
	for _, c := range conns {
		v := fmt.Sprintf("%s · %d prompts", c.RemoteAddr, c.Prompts)
		if c.Busy {
			v += " · busy"
		}
		rows = append(rows, cfgRow{kind: "conn", k: "connection " + shortID(c.ID), v: v, id: c.ID})
	}
	return rows
}

// scalarOrMarker renders a config value as a scalar, or a "·" marker for
// nested maps (their leaves are flattened one level in a future pass).
func scalarOrMarker(v any) string {
	switch x := v.(type) {
	case nil:
		return "—"
	case string:
		if x == "" {
			return "—"
		}
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%g", x)
	case map[string]any:
		return fmt.Sprintf("· %d keys", len(x))
	default:
		return fmt.Sprintf("%v", x)
	}
}

// ── memory actions ──────────────────────────────────────────────────────────

func (m *Model) memSelected() *memRow {
	if m.panel == panelMemory && m.panelSel < len(m.memRows) {
		return &m.memRows[m.panelSel]
	}
	return nil
}

func (m *Model) memDeleteSelected() tea.Cmd {
	r := m.memSelected()
	if r == nil || r.kind == "episode" {
		return nil
	}
	cl := m.cl
	target, text := r.kind, r.text
	return func() tea.Msg { return mgmtActionMsg{tab: panelMemory, err: cl.DeleteMemoryFact(target, text)} }
}

func (m *Model) memPromoteSelected() tea.Cmd {
	r := m.memSelected()
	if r == nil || r.kind != "episode" {
		return nil
	}
	cl := m.cl
	sid := r.sessionID
	return func() tea.Msg { return mgmtActionMsg{tab: panelMemory, err: cl.PromoteEpisode(sid)} }
}

func (m *Model) memConsolidate() tea.Cmd {
	cl := m.cl
	return func() tea.Msg { return mgmtActionMsg{tab: panelMemory, err: cl.ConsolidateMemory("user")} }
}

// memFactDraft submits the add-fact editor (panelDraft → target).
func (m *Model) memFactDraft() tea.Cmd {
	cl := m.cl
	target, text := m.memTarget, strings.TrimSpace(m.panelDraft)
	if text == "" {
		return nil
	}
	return func() tea.Msg { return mgmtActionMsg{tab: panelMemory, err: cl.AddMemoryFact(target, text)} }
}

// afterMgmtAction refetches the tab after a successful mutation.
func (m *Model) afterMgmtAction(msg mgmtActionMsg) tea.Cmd {
	if msg.err != nil {
		m.panelMsg = "action failed: " + msg.err.Error()
		m.refresh()
		return nil
	}
	switch msg.tab {
	case panelMemory:
		return m.openMemory()
	case panelSkills:
		return m.openSkills()
	case panelTools:
		return m.openTools()
	case panelConfig:
		return m.openConfig()
	}
	return nil
}

// ── skills actions ──────────────────────────────────────────────────────────

func (m *Model) skillSelected() *client.Skill {
	if m.panel == panelSkills && m.panelSel < len(m.skills) {
		return &m.skills[m.panelSel]
	}
	return nil
}

func (m *Model) skillPromote(force bool) tea.Cmd {
	s := m.skillSelected()
	if s == nil || !s.NeedsReview {
		return nil
	}
	cl := m.cl
	name := s.Name
	return func() tea.Msg { return mgmtActionMsg{tab: panelSkills, err: cl.PromoteSkill(name, force)} }
}

// ── config actions ──────────────────────────────────────────────────────────

func (m *Model) cfgSelected() *cfgRow {
	if m.panel == panelConfig && m.panelSel < len(m.cfgRows) {
		return &m.cfgRows[m.panelSel]
	}
	return nil
}

func (m *Model) cfgKickSelected() tea.Cmd {
	r := m.cfgSelected()
	if r == nil || r.kind != "conn" {
		return nil
	}
	cl := m.cl
	id := r.id
	return func() tea.Msg { return mgmtActionMsg{tab: panelConfig, err: cl.KickConnection(id)} }
}

// ── rendering ───────────────────────────────────────────────────────────────

func (m *Model) memRowsRender(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.memRows))
	for i, r := range m.memRows {
		label := r.text
		detail := "  fact · " + r.kind
		if r.kind == "episode" {
			detail = "  ⏳ pending episode · " + shortID(r.sessionID)
		}
		budget := w - 2 - lipgloss.Width(detail)
		prefix, lab := "  ", th.acItem.Render(truncate(label, budget))
		if i == m.panelSel {
			prefix, lab = th.acSel.Render("› "), th.acSel.Render(truncate(label, budget))
		}
		rows = append(rows, prefix+lab+th.acDetail.Render(detail))
	}
	return rows
}

func (m *Model) skillRowsRender(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.skills))
	for i, s := range m.skills {
		badges := ""
		if s.NeedsReview {
			badges += "  ⚠ needs review"
		}
		if s.Untrusted {
			badges += "  ⚠ untrusted"
		}
		detail := fmt.Sprintf("  ×%d · %s", s.UsageCount, s.Source) + badges
		budget := w - 2 - lipgloss.Width(detail)
		prefix, lab := "  ", th.acItem.Render(truncate(s.Name, budget))
		if i == m.panelSel {
			prefix, lab = th.acSel.Render("› "), th.acSel.Render(truncate(s.Name, budget))
		}
		rows = append(rows, prefix+lab+th.acDetail.Render(detail))
	}
	return rows
}

func (m *Model) toolRowsRender(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.toolRows))
	for i, r := range m.toolRows {
		detail := "  " + r.dim
		if r.kind == "mcp" {
			detail = "  mcp · " + r.dim
		}
		budget := w - 2 - lipgloss.Width(detail)
		prefix, lab := "  ", th.acItem.Render(truncate(r.text, budget))
		if i == m.panelSel {
			prefix, lab = th.acSel.Render("› "), th.acSel.Render(truncate(r.text, budget))
		}
		rows = append(rows, prefix+lab+th.acDetail.Render(detail))
	}
	return rows
}

func (m *Model) cfgRowsRender(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.cfgRows))
	for i, r := range m.cfgRows {
		detail := "  " + r.v
		if r.kind == "conn" {
			detail += "  · d kicks"
		}
		budget := w - 2 - lipgloss.Width(detail)
		prefix, lab := "  ", th.acItem.Render(truncate(r.k, budget))
		if i == m.panelSel {
			prefix, lab = th.acSel.Render("› "), th.acSel.Render(truncate(r.k, budget))
		}
		rows = append(rows, prefix+lab+th.acDetail.Render(detail))
	}
	return rows
}
