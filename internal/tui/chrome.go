package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── drawer bottom sheet ─────────────────────────────────────────────────────

const (
	sheetTranscriptMin = 8 // rows of conversation kept above the sheet
	sheetMin           = 6
	sheetMax           = 14
)

// drawerFullBleed reports a terminal too short to keep transcript + sheet.
func (m *Model) drawerFullBleed() bool {
	if m.panel == panelNone {
		return false
	}
	return m.drawerAvail() < sheetTranscriptMin+sheetMin
}

func (m *Model) drawerAvail() int {
	return m.height - headerHeight - footerHeight - m.inputAreaHeight()
}

// drawerSheetHeight is the panel's row budget when split above the composer.
// Zero with no drawer. On a short terminal the sheet claims the whole body.
func (m *Model) drawerSheetHeight() int {
	if m.panel == panelNone {
		return 0
	}
	avail := m.drawerAvail()
	if avail < 1 {
		return 0
	}
	if avail < sheetTranscriptMin+sheetMin {
		return avail
	}
	if m.panelDetail {
		sheet := avail - sheetTranscriptMin
		if sheet < sheetMin {
			return avail
		}
		return sheet
	}
	sheet := avail / 2
	if sheet > sheetMax {
		sheet = sheetMax
	}
	if sheet < sheetMin {
		sheet = sheetMin
	}
	if avail-sheet < sheetTranscriptMin {
		sheet = avail - sheetTranscriptMin
	}
	if sheet < 1 {
		return avail
	}
	return sheet
}

// drawerLayoutBudget is the body rows the sheet consumes, so relayout can
// shrink the viewport by exactly what View will paint.
func (m *Model) drawerLayoutBudget() int {
	if m.panel == panelNone || m.drawerFullBleed() {
		return 0
	}
	if sh := m.drawerSheetHeight(); sh > 0 {
		return sh
	}
	return 0
}

// chromeBodyHeight is the header-to-input block: transcript viewport, plus
// the drawer sheet when it shares the body.
func (m *Model) chromeBodyHeight() int {
	if m.panel != panelNone && m.drawerFullBleed() {
		if n := m.drawerAvail(); n > 0 {
			return n
		}
		return 1
	}
	h := m.vp.Height
	if m.panel != panelNone && !m.drawerFullBleed() {
		if sh := m.drawerSheetHeight(); sh > 0 {
			h += sh
		}
	}
	return h
}

// ── composer shelf ──────────────────────────────────────────────────────────

func (m *Model) shelfVisible() bool {
	return m.shelfView() != ""
}

func (m *Model) shelfHeight() int {
	if m.shelfVisible() {
		return 1
	}
	return 0
}

// shelfView is the single multiplexed row above the composer: staged
// files, queue count (when the strip is folded), new-output, skill hint.
func (m *Model) shelfView() string {
	th := m.th
	var chips []string
	if n := len(m.attachments); n > 0 {
		names := make([]string, 0, n)
		for _, a := range m.attachments {
			names = append(names, sanitize(a.Name))
		}
		chips = append(chips, th.headerKey.Render("📎 "+truncate(strings.Join(names, " · "), max(m.width/3, 12))))
	}
	if n := len(m.queue); n > 0 && !m.qfocus && m.curApproval() == nil && m.panel != panelQueue {
		chips = append(chips, th.scroll.Render(fmt.Sprintf("▸ %d queued", n)))
	}
	if m.busy && !m.vp.AtBottom() {
		chips = append(chips, th.scroll.Render("↓ new output"))
	}
	if m.skillSuggest != nil {
		name := m.skillSuggest.SkillName
		if name == "" {
			name = "skill"
		}
		chips = append(chips, th.acTitle.Render("✦ "+truncate(sanitize(name), 20))+
			th.acDim.Render(" alt+s save · alt+x skip"))
	}
	if len(chips) == 0 {
		return ""
	}
	row := "  " + strings.Join(chips, th.footerSep.Render("   "))
	if m.width > 0 && lipgloss.Width(row) > m.width {
		row = lipgloss.NewStyle().MaxWidth(m.width).Render(row)
	}
	return row
}

// ── header instruments ──────────────────────────────────────────────────────

func (m *Model) headerPlanLabel() string {
	if !m.planInit || m.planAvail != planAvailable || !m.plan.Found {
		return ""
	}
	total := len(m.plan.Steps)
	if total == 0 {
		return ""
	}
	done := 0
	for _, st := range m.plan.Steps {
		if st.Status == client.PlanDone {
			done++
		}
	}
	return fmt.Sprintf("plan %d/%d", done, total)
}

func (m *Model) headerJobsLabel() string {
	n := 0
	failed := false
	for _, j := range m.jobs {
		switch j.Status {
		case "running":
			n++
		case "failed", "timeout", "killed":
			failed = true
		}
	}
	if n > 0 {
		return "● " + plural(n, "job", "jobs")
	}
	if failed {
		return "✗ job"
	}
	return ""
}

func (m *Model) headerInstruments() string {
	var parts []string
	if s := m.headerPlanLabel(); s != "" {
		parts = append(parts, s)
	}
	if s := m.headerJobsLabel(); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// ── mode pill ───────────────────────────────────────────────────────────────

func (m *Model) modeName() string {
	switch {
	case m.confirm != confirmNone:
		return "confirm"
	case m.pal.open:
		return "palette"
	case m.curApproval() != nil:
		return "approval"
	case m.panel != panelNone:
		return panelModeName(m.panel)
	case m.popover:
		return "cockpit"
	case m.find.open:
		return "find"
	case m.ac.open:
		return "attach"
	case m.qfocus:
		return "queue"
	default:
		return "composer"
	}
}

func panelModeName(p panelMode) string {
	for _, t := range drawerTabs() {
		if t.mode == p {
			return t.name
		}
	}
	if p == panelModels {
		return "models"
	}
	if p == panelQueue {
		return "queue"
	}
	if p == panelStats {
		return "stats"
	}
	return "drawer"
}

func (m *Model) modePrefix() string {
	return "  " + m.th.footerKey.Render(m.modeName()) + m.th.footerSep.Render(" · ")
}

// ── session home ────────────────────────────────────────────────────────────

// homeSessionsMsg delivers the async recent-sessions list for the cleared
// home dashboard (same source as the palette's resume rows).
type homeSessionsMsg struct {
	items []client.Session
	err   error
	gen   int // clear generation the fetch was armed under
}

// fetchHomeSessions arms the once-per-clear recent-sessions fetch that backs
// the home dashboard. Nil when this clear already armed one; the generation
// stamp lets a slow reply from a superseded clear be dropped on arrival.
func (m *Model) fetchHomeSessions() tea.Cmd {
	if m.homeSessDone {
		return nil
	}
	m.homeSessDone = true
	cl := m.cl
	gen := m.homeSessGen
	return func() tea.Msg {
		if cl == nil {
			return homeSessionsMsg{gen: gen}
		}
		items, err := cl.Sessions()
		return homeSessionsMsg{items: items, err: err, gen: gen}
	}
}

// handleHomeSessions stores the fetched recents. Failures stay silent —
// the dashboard is decorative orientation, never worth a strip note — and
// a reply from a superseded clear is dropped by generation.
func (m *Model) handleHomeSessions(msg homeSessionsMsg) {
	if msg.err != nil || msg.gen != m.homeSessGen {
		return
	}
	m.homeSess = msg.items
	m.refresh()
}

func (m *Model) home() string {
	if m.homePrompt != "" || (m.sessionID != "" && m.lastPrompt != "") {
		return m.sessionHome()
	}
	return welcome(m.th, m.cardSpan(), m.opts.CWD, m.resumeTitle)
}

func (m *Model) sessionHome() string {
	th := m.th
	w := m.cardSpan()
	if w < 20 {
		w = 20
	}
	prompt := m.homePrompt
	if prompt == "" {
		prompt = m.lastPrompt
	}
	var b strings.Builder
	b.WriteString(th.asstLabel.Render("⬡ session") + "\n")
	if prompt != "" {
		b.WriteString(th.userBar.Render("last: "+truncate(collapse(prompt), max(w-8, 12))) + "\n")
	}
	if m.homeReceipt != "" {
		b.WriteString(th.statsDim.Render(m.homeReceipt) + "\n")
	}
	if m.maxContext > 0 {
		b.WriteString(m.ctxGauge(true) + "\n")
	}
	// Recent sessions: orientation, not navigation — ⏎ types a new task
	// and the hub (^K) resumes. Wire-borne titles collapse before render.
	for i, s := range m.homeSess {
		if i >= 3 {
			break
		}
		task := collapse(s.Task)
		if task == "" {
			task = "(untitled)"
		}
		b.WriteString(th.statsDim.Render(fmt.Sprintf("%d ↩ %s · %d turns · %s",
			i+1, truncate(task, max(w-24, 12)), s.Turns, ago(s.UpdatedAt))) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(th.tipKey.Render("type a task") + "  " + th.tipText.Render("1–3 resume · ⏎ sends · ^K everything · /verbosity dials detail") + "\n")
	return lipgloss.NewStyle().Width(w).PaddingLeft(2).Render(strings.TrimRight(b.String(), "\n"))
}

// handleHomeResumeKey resumes a recent session from the cleared home card.
func (m *Model) handleHomeResumeKey(key string) tea.Cmd {
	if len(m.msgs) > 0 || m.busy || m.panel != panelNone {
		return nil
	}
	var slot int
	switch key {
	case "1":
		slot = 0
	case "2":
		slot = 1
	case "3":
		slot = 2
	default:
		return nil
	}
	if slot >= len(m.homeSess) || slot >= 3 {
		return nil
	}
	return m.resumeSession(m.homeSess[slot].ID)
}

func captureHome(m *Model) {
	if p := lastUserPrompt(m.msgs); p != "" {
		m.homePrompt = p
	} else if m.lastPrompt != "" {
		m.homePrompt = m.lastPrompt
	}
	for i := len(m.msgs) - 1; i >= 0; i-- {
		if m.msgs[i].role == roleAsst && !m.msgs[i].raw {
			if rec := formatReceipt(scanReceipt(m.msgs[i])); rec != "" {
				m.homeReceipt = rec
			}
			return
		}
	}
}

func lastUserPrompt(msgs []message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].role == roleUser && strings.TrimSpace(msgs[i].content) != "" {
			return msgs[i].content
		}
	}
	return ""
}

func clearHome(m *Model) {
	m.homePrompt = ""
	m.homeReceipt = ""
}

// ── card width contract ─────────────────────────────────────────────────────
//
// Every framed card (composer, approval, palette, @, drawer, stats, cockpit,
// help) uses the same lipgloss Width: two columns shy of the terminal so the
// rounded border paints flush to both edges. Unbordered transcript surfaces
// (answer cards, notes) use cardSpan so their painted edge matches.

func (m *Model) termWidth() int {
	if m.width > 0 {
		return m.width
	}
	return m.vp.Width
}

// boxWidth is the lipgloss Width of a framed card in a region of `term` columns.
func boxWidth(term int) int {
	if term < 8 {
		return 6
	}
	return term - 2
}

// boxInner is the text column inside a framed card (Width includes pad 2).
func boxInner(term int) int {
	if term < 12 {
		return 8
	}
	return term - 4
}

func (m *Model) cardWidth() int { return boxWidth(m.termWidth()) }
func (m *Model) cardInner() int { return boxInner(m.termWidth()) }

// cardSpan is the lipgloss Width of an unbordered transcript surface so its
// painted edge matches a framed card (border adds the two columns Width omits).
func (m *Model) cardSpan() int {
	w := m.termWidth()
	if w < 8 {
		return 8
	}
	return w
}
