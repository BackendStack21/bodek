package tui

import (
	"encoding/json"
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
	sag  []client.SubagentEntry
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
	id   string            // mcp: server name
	srv  *client.MCPServer // mcp: full record for the detail view
}

// cfgRow is one config/usage/connection row.
type cfgRow struct {
	kind string // "cfg" | "usage" | "conn"
	k    string
	v    string
	id   string // conn: kickable id
	raw  string // cfg: pretty JSON when v is the "·" marker (detail view)
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
	m.panelDetail = false
	m.detailScroll = 0
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
	m.panelDetail = false
	m.detailScroll = 0
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
	m.panelDetail = false
	m.detailScroll = 0
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
	m.panelDetail = false
	m.detailScroll = 0
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
	case panelAgents:
		m.agentsReg = msg.sag
		if len(msg.sag) == 0 {
			m.panelMsg = "no sub-agent activity recorded"
		} else if m.confirm != confirmStopAgent {
			m.panelMsg = "" // keep the armed stop gate's prompt visible
		}
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
		rows = append(rows, toolRow{kind: "mcp", text: s.Name, dim: detail, id: s.Name, srv: &s})
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
			rows = appendCfgRow(rows, k, cfg[k])
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

// appendCfgRow flattens one level of a config value into list rows:
// scalars render as "k", a map's children as "k.child". Anything deeper
// (nested maps, slices) keeps a "·" marker with the raw JSON stashed in
// raw for the detail view.
func appendCfgRow(rows []cfgRow, k string, v any) []cfgRow {
	if isList(v) {
		return append(rows, cfgRow{kind: "cfg", k: k, v: "·", raw: rawJSON(v)})
	}
	if sub, ok := v.(map[string]any); ok && len(sub) > 0 {
		keys := make([]string, 0, len(sub))
		for ck := range sub {
			keys = append(keys, ck)
		}
		sort.Strings(keys)
		for _, ck := range keys {
			kk, vv := k+"."+ck, sub[ck]
			if _, nested := vv.(map[string]any); nested || isList(vv) {
				rows = append(rows, cfgRow{kind: "cfg", k: kk, v: "·", raw: rawJSON(vv)})
				continue
			}
			rows = append(rows, cfgRow{kind: "cfg", k: kk, v: scalarOrMarker(vv)})
		}
		return rows
	}
	r := cfgRow{kind: "cfg", k: k, v: scalarOrMarker(v)}
	if _, ok := v.(map[string]any); ok { // empty map: marker with raw body
		r.raw = rawJSON(v)
	}
	return append(rows, r)
}

func isList(v any) bool { _, ok := v.([]any); return ok }

// rawJSON pretty-prints a nested config value for the detail view; a
// marshal failure yields "" and the detail simply falls back to the row.
func rawJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
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

func (m *Model) memConsolidate(target string) tea.Cmd {
	cl := m.cl
	return func() tea.Msg { return mgmtActionMsg{tab: panelMemory, err: cl.ConsolidateMemory(target)} }
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
			if m.confirm == confirmFactDelete {
				prefix, lab = th.badgeDanger.Render("⚠ "), th.badgeDanger.Render(truncate(label, budget))
			} else {
				prefix, lab = th.acSel.Render("› "), th.acSel.Render(truncate(label, budget))
			}
		}
		rows = append(rows, prefix+lab+th.acDetail.Render(detail))
	}
	return rows
}

func (m *Model) skillRowsRender(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.skills)*2)
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
		// A dim description line under each name: the list stays scannable
		// while the skill's purpose is finally readable at a glance.
		if d := strings.TrimSpace(s.Description); d != "" {
			rows = append(rows, "    "+th.acDim.Render(truncate(collapse(sanitize(d)), w-4)))
		}
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

// ── detail view (management tabs) ───────────────────────────────────────────
//
// Enter on a list row expands it into a readable block: the full text the
// promote/delete gates assume the human can see. Esc folds back to the list
// with the selection intact.

// mgmtPanel reports whether p is a management drawer tab.
func mgmtPanel(p panelMode) bool {
	switch p {
	case panelAgents, panelPlan, panelMemory, panelSkills, panelTools, panelConfig:
		return true
	}
	return false
}

// closeDetail folds the detail view back to the list, selection intact.
func (m *Model) closeDetail() {
	m.panelDetail = false
	m.detailScroll = 0
	m.refresh()
}

// detailMaxScroll is the last scroll offset that keeps the final detail line
// on screen; scrolling stops there.
func (m *Model) detailMaxScroll() int {
	visible := m.height - 5 // border(2) + title(1) + breathing room
	if visible < 1 {
		visible = 1
	}
	return max(len(m.mgmtDetailLines(m.width-8))-visible, 0)
}

// skillSelRow maps the selected skill to its visual row in the list,
// accounting for the description line some skills render below their name.
func (m *Model) skillSelRow() int {
	row := 0
	for i := range m.skills {
		if i == m.panelSel {
			break
		}
		row++
		if strings.TrimSpace(m.skills[i].Description) != "" {
			row++
		}
	}
	return row
}

func (m *Model) toolSelected() *toolRow {
	if m.panel == panelTools && m.panelSel < len(m.toolRows) {
		return &m.toolRows[m.panelSel]
	}
	return nil
}

// mgmtDetailLines renders the selected row's detail block, wrapped to w.
// Everything from the wire goes through sanitize().
// agentRowsRender renders the agents tab: one row per registry entry —
// status glyph, redacted goal, and a compact usage tail.
func (m *Model) agentRowsRender(w int) []string {
	th := m.th
	rows := make([]string, 0, len(m.agentsReg))
	for i, e := range m.agentsReg {
		goal := e.Goal
		if goal == "" {
			goal = "(no goal recorded)"
		}
		var detail string
		if e.Phase == "finished" {
			detail = fmt.Sprintf("  %s · %d it · %s tok", e.Status, e.Iterations, human(e.TokensUsed))
		} else {
			detail = fmt.Sprintf("  running · step %d · %s", e.Step, e.LastTool)
		}
		if e.DurationSeconds > 0 {
			detail += fmt.Sprintf(" · %.1fs", e.DurationSeconds)
		}
		budget := w - 2 - lipgloss.Width(detail)
		label := agentStatusGlyph(e.Phase, e.Status) + " " + goal
		prefix, lab := "  ", th.acItem.Render(truncate(label, budget))
		if i == m.panelSel {
			prefix, lab = th.acSel.Render("› "), th.acSel.Render(truncate(label, budget))
		}
		rows = append(rows, prefix+lab+th.acDetail.Render(detail))
	}
	return rows
}

// agentStatusGlyph mirrors the live-card glyph set.
func agentStatusGlyph(phase, status string) string {
	if phase != "finished" {
		return "⟳"
	}
	switch status {
	case "success":
		return "✓"
	case "partial":
		return "◐"
	case "error":
		return "✗"
	case "cancelled":
		return "⊘"
	case "timeout":
		return "⏱"
	}
	return "•"
}

func (m *Model) mgmtDetailLines(w int) []string {
	th := m.th
	var out []string
	switch m.panel {
	case panelPlan:
		st := m.planStepAt(m.panelSel)
		if st == nil {
			return []string{th.acDim.Render("no step selected")}
		}
		out = append(out, th.acSel.Render("› "+planGlyph(st.Status)+" "+sanitize(st.ID)))
		meta := []string{string(st.Status)}
		if m.planInit {
			meta = append(meta, fmt.Sprintf("v%d", m.plan.Version))
		}
		out = append(out, th.acDetail.Render(strings.Join(meta, " · ")))
		if t := strings.TrimSpace(st.Title); t != "" {
			out = append(out, "")
			out = append(out, wrapText(sanitize(t), w)...)
		}
		if n := strings.TrimSpace(st.Note); n != "" {
			out = append(out, "")
			out = append(out, th.acDetail.Render("note:"))
			out = append(out, wrapText(sanitize(n), w)...)
		}
	case panelSkills:
		s := m.skillSelected()
		if s == nil {
			return []string{th.acDim.Render("no skill selected")}
		}
		out = append(out, th.acSel.Render("› "+sanitize(s.Name)))
		meta := []string{fmt.Sprintf("×%d used", s.UsageCount), sanitize(s.Source)}
		if s.AutoLoad {
			meta = append(meta, "auto-load")
		}
		if s.NeedsReview {
			meta = append(meta, "needs review")
		}
		if s.Untrusted {
			meta = append(meta, "untrusted")
		}
		out = append(out, th.acDetail.Render(strings.Join(meta, " · ")))
		if d := strings.TrimSpace(s.Description); d != "" {
			out = append(out, "")
			out = append(out, wrapText(sanitize(d), w)...)
		}
	case panelMemory:
		r := m.memSelected()
		if r == nil {
			return []string{th.acDim.Render("no row selected")}
		}
		if r.kind == "episode" {
			out = append(out, th.acSel.Render("› pending episode"))
			out = append(out, th.acDetail.Render("session "+sanitize(r.sessionID)))
		} else {
			out = append(out, th.acSel.Render("› "+sanitize(r.kind)+" fact"))
		}
		out = append(out, "")
		out = append(out, wrapText(sanitize(r.text), w)...)
	case panelAgents:
		if m.panelSel >= len(m.agentsReg) {
			return []string{th.acDim.Render("no entry selected")}
		}
		e := m.agentsReg[m.panelSel]
		out = append(out, th.acSel.Render("› "+agentStatusGlyph(e.Phase, e.Status)+" "+sanitize(e.Goal)))
		meta := []string{e.Phase, e.Status, "task " + sanitize(e.TaskID)}
		if e.LastTool != "" {
			meta = append(meta, "last "+sanitize(e.LastTool))
		}
		out = append(out, th.acDetail.Render(strings.Join(meta, " · ")))
		out = append(out, "")
		out = append(out, th.acDetail.Render(fmt.Sprintf("run %s · %d iterations · %d tokens · %.1fs",
			sanitize(e.RunKey), e.Iterations, e.TokensUsed, e.DurationSeconds)))
		if !e.StartedAt.IsZero() {
			out = append(out, th.acDetail.Render("started "+e.StartedAt.String()))
		}
		if !e.FinishedAt.IsZero() {
			out = append(out, th.acDetail.Render("finished "+e.FinishedAt.String()))
		}
	case panelTools:
		r := m.toolSelected()
		if r == nil {
			return []string{th.acDim.Render("no row selected")}
		}
		if r.kind == "mcp" && r.srv != nil {
			srv := r.srv
			out = append(out, th.acSel.Render("› "+sanitize(srv.Name)+" · mcp server"))
			cmd := sanitize(srv.Command)
			for _, a := range srv.Args {
				cmd += " " + sanitize(a)
			}
			out = append(out, th.acDetail.Render(cmd))
			var meta []string
			if srv.Project {
				meta = append(meta, "project-scoped")
			}
			if srv.AutoApprove {
				meta = append(meta, "auto-approve")
			}
			if srv.TimeoutSeconds > 0 {
				meta = append(meta, fmt.Sprintf("timeout %ds", srv.TimeoutSeconds))
			}
			if srv.MaxResponseBytes > 0 {
				meta = append(meta, fmt.Sprintf("max response %s", human(int(srv.MaxResponseBytes))))
			}
			if srv.MaxResultChars > 0 {
				meta = append(meta, fmt.Sprintf("max result %s chars", human(srv.MaxResultChars)))
			}
			if len(meta) > 0 {
				out = append(out, th.acDetail.Render(strings.Join(meta, " · ")))
			}
		} else {
			out = append(out, th.acSel.Render("› "+sanitize(r.text)))
			state := "enabled"
			if r.dim != "on" {
				state = "disabled"
			}
			out = append(out, th.acDetail.Render("built-in tool · "+state))
		}
	case panelConfig:
		r := m.cfgSelected()
		if r == nil {
			return []string{th.acDim.Render("no row selected")}
		}
		out = append(out, th.acSel.Render("› "+sanitize(r.k)))
		out = append(out, th.acDetail.Render(sanitize(r.v)))
		if r.raw != "" {
			out = append(out, "")
			out = append(out, wrapText(sanitize(r.raw), w)...)
		}
	}
	return out
}
