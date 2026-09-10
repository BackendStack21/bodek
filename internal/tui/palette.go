package tui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── command palette ─────────────────────────────────────────────────────────
//
// The palette (^K) is the navigation spine: every command, view, session,
// model, and action is one fuzzy search away, and every entry shows its chord
// so users graduate from palette to keys naturally. It is the answer to
// "eleven integrations, no modality explosion": new surfaces add entries, not
// keybindings.

// palEntry is one searchable palette row.
type palEntry struct {
	title  string // the searchable label
	hint   string // the chord teaching ("" when none exists)
	kind   string // "command" · "view" · "action" · "session" · "model"
	detail string // optional readable description shown beside the kind
	run    func(m *Model) tea.Cmd
}

type palMode uint8

const (
	palModeCommands palMode = iota
	palModeThemes
)

// palState is the overlay state. Sessions arrive async after open.
type palState struct {
	open    bool
	mode    palMode
	query   string
	all     []palEntry // unfiltered source rows
	items   []palEntry // filtered, ranked
	sel     int
	loading bool
}

// palSessionsMsg delivers the async session list for the palette.
type palSessionsMsg struct {
	items []client.Session
	err   error
}

// maxPalRows bounds the visible list; scrolling windows around the selection.
const maxPalRows = 8

// togglePalette opens the palette (rebuilding entries) or closes it.
func (m *Model) togglePalette() tea.Cmd {
	if m.pal.open {
		m.pal.open = false
		m.relayout()
		m.refresh()
		return nil
	}
	m.pal = palState{open: true, mode: palModeCommands, loading: true}
	m.pal.all = m.basePaletteEntries()
	m.filterPalette()
	m.relayout()
	m.refresh()

	cl := m.cl
	if cl == nil {
		m.pal.loading = false
		m.filterPalette()
		return nil
	}
	return func() tea.Msg {
		items, err := cl.Sessions()
		return palSessionsMsg{items: items, err: err}
	}
}

// openThemePalette opens the palette's compact theme selector. It deliberately
// replaces the command palette state so a late session fetch cannot leak rows
// into this selector.
func (m *Model) openThemePalette() tea.Cmd {
	m.pal = palState{open: true, mode: palModeThemes, all: themePaletteEntries()}
	for i, e := range m.pal.all {
		if e.title == themeName() {
			m.pal.sel = i
			break
		}
	}
	m.filterPalette()
	m.relayout()
	m.refresh()
	return nil
}

func themePaletteEntries() []palEntry {
	return []palEntry{
		{title: "ember-dark", kind: "theme", detail: "dark terminal", run: func(m *Model) tea.Cmd { return m.switchTheme("ember-dark") }},
		{title: "ember-light", kind: "theme", detail: "light parchment", run: func(m *Model) tea.Cmd { return m.switchTheme("ember-light") }},
		{title: "high-contrast", kind: "theme", detail: "high contrast", run: func(m *Model) tea.Cmd { return m.switchTheme("high-contrast") }},
		{title: "classic", kind: "theme", detail: "classic violet", run: func(m *Model) tea.Cmd { return m.switchTheme("classic") }},
	}
}

// handlePalSessions appends the fetched sessions to the palette source.
func (m *Model) handlePalSessions(msg palSessionsMsg) tea.Cmd {
	if !m.pal.open || m.pal.mode != palModeCommands {
		return nil
	}
	m.pal.loading = false
	if msg.err == nil {
		for _, s := range msg.items {
			sess := s
			task := sess.Task
			if task == "" {
				task = "(untitled)"
			}
			m.pal.all = append(m.pal.all, palEntry{
				title: "resume · " + collapse(task),
				hint:  "^R",
				kind:  "session",
				run:   func(m *Model) tea.Cmd { return m.resumeSession(sess.ID) },
			})
		}
	}
	m.filterPalette()
	m.refresh()
	return nil
}

// basePaletteEntries builds the non-session rows. Navigation views and
// actions lead — the palette's default page is the spine — with slash
// commands and the model catalog behind them.
func (m *Model) basePaletteEntries() []palEntry {
	entries := []palEntry{
		{title: "sessions", hint: "^R", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openSessions() }},
		{title: "models", hint: "^O", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openModels() }},
		{title: "thinking", hint: "/thinking", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openThinking() }},
		{title: "cockpit — server & budget", hint: "/server", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openCockpit() }},
		{title: "runs — headless & approvals", hint: "/runs", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openRuns() }},
		{title: "events — runtime feed", hint: "/events", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openEvents() }},
		{title: "jobs — background commands", hint: "/jobs", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openJobs() }},
		{title: "memory — facts & episodes", hint: "/memory", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openMemory() }},
		{title: "skills — provenance & promote", hint: "/skills", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openSkills() }},
		{title: "tools — registry & MCP", hint: "/tools", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openTools() }},
		{title: "config — usage & connections", hint: "/config", kind: "view",
			run: func(m *Model) tea.Cmd { return m.openConfig() }},
		{title: "cancel the running turn", hint: "esc", kind: "action",
			run: func(m *Model) tea.Cmd { return m.cancelRun() }},
		{title: "run headless — sends the composer draft", hint: "/run", kind: "action",
			run: func(m *Model) tea.Cmd {
				draft := strings.TrimSpace(m.ta.Value())
				if draft == "" {
					return m.transientNoteCmd("type a prompt first — the palette runs your draft headlessly")
				}
				m.ta.Reset()
				return m.startHeadlessRun(draft)
			}},
		{title: "toggle details (reasoning + tool output)", hint: "^E", kind: "action",
			run: func(m *Model) tea.Cmd {
				m.expandAll = !m.expandAll
				m.invalidateAllMsgBlocks()
				for i := range m.msgs {
					for j := range m.msgs[i].steps {
						clearStepBlockCache(&m.msgs[i].steps[j])
					}
				}
				return nil
			}},
		{title: "cycle reasoning depth", hint: "^T", kind: "action",
			run: func(m *Model) tea.Cmd { return m.cycleThinkingLevel() }},
		{title: "find in transcript", hint: "alt+f", kind: "action",
			run: func(m *Model) tea.Cmd { m.openFind(); return nil }},
		{title: "fold the latest turn", hint: "^F", kind: "action",
			run: func(m *Model) tea.Cmd { m.toggleCollapseLast(); return nil }},
		{title: "verbosity: quiet — hide info notes", hint: "/verbosity", kind: "action",
			run: func(m *Model) tea.Cmd { return m.setVerbosity(verbosityQuiet) }},
		{title: "verbosity: normal — default transcript", hint: "/verbosity", kind: "action",
			run: func(m *Model) tea.Cmd { return m.setVerbosity(verbosityNormal) }},
		{title: "verbosity: detailed — expand every step", hint: "^E", kind: "action",
			run: func(m *Model) tea.Cmd { return m.setVerbosity(verbosityDetailed) }},
	}
	for _, c := range slashCommands() {
		cmd := c
		entries = append(entries, palEntry{
			title: "/" + cmd.name, kind: "command",
			run: func(m *Model) tea.Cmd { return m.runCommand(cmd.name, "") },
		})
	}
	for _, e := range m.modelEntries() {
		ent := e
		entries = append(entries, palEntry{
			title: "model · " + ent.label, kind: "model",
			run: func(m *Model) tea.Cmd {
				m.applyModelChoice(ent.id)
				return m.transientNoteCmd("model set to " + m.model + " (applies next turn)")
			},
		})
	}
	for _, e := range m.thinkingEntries() {
		ent := e
		entries = append(entries, palEntry{
			title: "thinking · " + ent.label, kind: "action",
			run: func(m *Model) tea.Cmd { return m.setThinking(ent.id) },
		})
	}
	return entries
}

// filterPalette recomputes the visible rows for the current query.
func (m *Model) filterPalette() {
	q := m.pal.query
	if q == "" {
		m.pal.items = m.pal.all
		if m.pal.sel >= len(m.pal.items) {
			m.pal.sel = 0
		}
		return
	}
	type scored struct {
		e    palEntry
		rank int
	}
	var hits []scored
	for _, e := range m.pal.all {
		if rank, ok := fuzzyScore(q, e.title); ok {
			hits = append(hits, scored{e, rank})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].rank > hits[j].rank })
	m.pal.items = make([]palEntry, 0, len(hits))
	for _, h := range hits {
		m.pal.items = append(m.pal.items, h.e)
	}
	if m.pal.sel >= len(m.pal.items) {
		m.pal.sel = 0
	}
}

// fuzzyScore reports whether query matches s as a case-insensitive
// subsequence, with a rank that rewards consecutive runs (compounding) and
// word starts — tight matches outrank scattered ones.
func fuzzyScore(query, s string) (int, bool) {
	q := strings.ToLower(query)
	t := strings.ToLower(s)
	if q == "" {
		return 0, true
	}
	rank, run, qi := 0, 0, 0
	for i := 0; i < len(t) && qi < len(q); i++ {
		if t[i] == q[qi] {
			run++
			rank += run * 2 // consecutive characters compound
			if i == 0 || t[i-1] == ' ' || t[i-1] == '/' || t[i-1] == '·' {
				rank += 3 // word-start bonus
			}
			qi++
		} else {
			run = 0
		}
	}
	return rank, qi == len(q)
}

// handlePaletteKey drives the overlay: typing filters, arrows move, enter
// runs, esc/^K close. The palette outranks every other mode except quit.
func (m *Model) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, m.armConfirm(confirmQuit, "bodek")
	case "esc", "ctrl+k":
		m.pal.open = false
		m.relayout()
		m.refresh()
		return m, nil
	case "up", "ctrl+p":
		if m.pal.sel > 0 {
			m.pal.sel--
			m.refresh()
		}
		return m, nil
	case "down", "ctrl+n":
		if m.pal.sel < len(m.pal.items)-1 {
			m.pal.sel++
			m.refresh()
		}
		return m, nil
	case "enter", "tab":
		if len(m.pal.items) == 0 {
			return m, nil
		}
		entry := m.pal.items[m.pal.sel]
		m.pal.open = false
		m.relayout()
		var cmd tea.Cmd
		if entry.run != nil {
			cmd = entry.run(m)
		}
		m.refresh()
		return m, cmd
	case "backspace":
		if n := len(m.pal.query); n > 0 {
			m.pal.query = m.pal.query[:n-1]
			m.filterPalette()
			m.refresh()
		}
		return m, nil
	case "space":
		m.pal.query += " "
		m.filterPalette()
		m.refresh()
		return m, nil
	default:
		if s := msg.String(); len([]rune(s)) == 1 {
			m.pal.query += s
			m.filterPalette()
			m.refresh()
		}
		return m, nil
	}
}

// palPopup renders the palette above the composer. Its height must match
// palHeight so the layout math stays exact.
func (m *Model) palPopup() string {
	th := m.th
	innerW := m.cardInner()

	titleText := "⌘ everything"
	if m.pal.mode == palModeThemes {
		titleText = "⌘ themes"
	}
	if m.pal.query != "" {
		budget := max(1, innerW-lipgloss.Width(titleText)-3)
		titleText += " · " + ansi.Truncate(m.pal.query, budget, "…")
	}
	title := th.acTitle.Render(titleText)
	hint := "  ↑↓ select · ⏎ run · esc close"
	if lipgloss.Width(title)+lipgloss.Width(hint) <= innerW {
		title += th.acDim.Render(hint)
	}
	var rows []string
	switch {
	case len(m.pal.items) == 0 && m.pal.query != "":
		rows = append(rows, ansi.Truncate(th.acDim.Render("no matches for “"+m.pal.query+"”"), max(1, innerW), ""))
	default:
		window, start := windowEntries(m.pal.items, m.pal.sel, m.palRowLimit())
		for i, e := range window {
			labelText := e.title
			if m.pal.mode == palModeThemes && e.title == themeName() {
				labelText += " ✓"
			}
			prefix, label := "  ", th.acItem.Render(labelText)
			detailText := e.kind
			if e.detail != "" {
				detailText = e.detail
			}
			detail := th.acDetail.Render(detailText)
			if e.hint != "" {
				detail += th.acDetail.Render("  ·  ") + th.footerKey.Render(e.hint)
			}
			if start+i == m.pal.sel {
				prefix, label = th.acSel.Render("› "), th.acSel.Render(labelText)
			}
			rows = append(rows, ansi.Truncate(prefix+label+"  "+detail, max(1, innerW), ""))
		}
		if m.pal.loading {
			rows = append(rows, th.acDim.Render("  … loading sessions"))
		}
	}
	return th.acBox.Width(m.cardWidth()).Render(title + "\n" + strings.Join(rows, "\n"))
}

// windowEntries windows entries around sel without changing indices.
// It returns the window and the absolute index of its first row, so
// callers can map window-relative positions back onto sel.
func windowEntries(entries []palEntry, sel, n int) ([]palEntry, int) {
	if len(entries) <= n {
		return entries, 0
	}
	start := sel - n/2
	if start < 0 {
		start = 0
	}
	if start+n > len(entries) {
		start = len(entries) - n
	}
	return entries[start : start+n], start
}

// palHeight is the palette's rendered height (border + title + rows).
func (m *Model) palHeight() int {
	n := len(m.pal.items)
	if n > m.palRowLimit() {
		n = m.palRowLimit()
	}
	if n == 0 {
		n = 1
	}
	if m.pal.loading {
		n++
	}
	return n + 3
}

// palRowLimit leaves one transcript row visible while reserving the current
// composer, status line, and other input chrome. The same budget drives both
// rendering and layout so a busy palette cannot push the screen past height.
func (m *Model) palRowLimit() int {
	rows := maxPalRows
	reserved := headerHeight + footerHeight + m.ta.Height() + 2 + 1
	if m.statusLineVisible() {
		reserved += 2
	}
	if m.curApproval() != nil && !m.pal.open {
		reserved += lineCount(m.approvalPanel())
	}
	if m.clarify != nil && !m.pal.open {
		reserved += lineCount(m.clarifyPanel())
	}
	reserved += m.shelfHeight() + m.queueStripHeight()
	if available := m.height - reserved - 3; available < rows {
		rows = available
	}
	return max(1, rows)
}
