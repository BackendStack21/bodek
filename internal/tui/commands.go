package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// command is a slash command typed in the input as "/name [args]".
type command struct {
	name string
	desc string
	run  func(m *Model, args string) tea.Cmd
}

// slashCommands is the registry. Keeping it a function keeps the closures
// simple and avoids package-init ordering concerns.
func slashCommands() []command {
	return []command{
		{"help", "show available commands", func(m *Model, _ string) tea.Cmd {
			m.showHelp()
			return nil
		}},
		{"clear", "clear the conversation", func(m *Model, _ string) tea.Cmd {
			// Same gate as ^L, and idle-only: a mid-turn wipe would drop the
			// view out from under the streaming turn.
			if m.busy {
				return m.transientNoteCmd("can't clear while a turn runs — esc cancels it first")
			}
			return m.armConfirm(confirmClear, "the conversation")
		}},
		{"new", "start a fresh session (the old one stays resumable)", func(m *Model, _ string) tea.Cmd {
			// Idle-only like /clear, plus the connection-scoped state a
			// forced redial would destroy: pending approvals die with the
			// socket, and queued prompts belong to THIS conversation.
			if m.busy {
				return m.transientNoteCmd("can't start a new session while a turn runs — esc cancels it first")
			}
			if len(m.approvals) > 0 {
				return m.transientNoteCmd("answer the pending approval first — it dies with the connection")
			}
			if len(m.queue) > 0 {
				return m.transientNoteCmd("drain the prompt queue first (/queue or ctrl+q)")
			}
			return m.startFreshSession()
		}},
		{"copy", "copy the last reply to the clipboard", func(m *Model, _ string) tea.Cmd {
			return m.copyLastReply()
		}},
		{"export", "save the session transcript — /export [md|json]", runExport},
		{"theme", "switch the color theme — /theme [name]", runTheme},
		{"retry", "re-send the last prompt (alt+r)", func(m *Model, _ string) tea.Cmd {
			return m.retryLast()
		}},
		{"queue", "manage queued prompts — priority, delete, send now", func(m *Model, _ string) tea.Cmd {
			return m.openQueue()
		}},
		{"stats", "session metrics & context gauge", func(m *Model, _ string) tea.Cmd {
			m.showStats()
			return nil
		}},
		{"sessions", "browse & resume saved sessions", func(m *Model, _ string) tea.Cmd {
			return m.openSessions()
		}},
		{"server", "cockpit — server, link, budget & session", func(m *Model, _ string) tea.Cmd {
			return m.openCockpit()
		}},
		{"runs", "headless runs & remote approvals", func(m *Model, _ string) tea.Cmd {
			return m.openRuns()
		}},
		{"run", "start a headless run — /run <prompt>", func(m *Model, args string) tea.Cmd {
			return m.startHeadlessRun(args)
		}},
		{"events", "runtime event feed", func(m *Model, _ string) tea.Cmd {
			return m.openEvents()
		}},
		{"jobs", "background jobs — status, output & stop", func(m *Model, _ string) tea.Cmd {
			return m.openJobs()
		}},
		{"plan", "structured task plan of this session", func(m *Model, _ string) tea.Cmd {
			return m.openPlan()
		}},
		{"memory", "facts, pending episodes, consolidate", func(m *Model, _ string) tea.Cmd {
			return m.openMemory()
		}},
		{"skills", "skill provenance & promote", func(m *Model, _ string) tea.Cmd {
			return m.openSkills()
		}},
		{"tools", "tool registry & MCP servers", func(m *Model, _ string) tea.Cmd {
			return m.openTools()
		}},
		{"config", "server config, usage & connections", func(m *Model, _ string) tea.Cmd {
			return m.openConfig()
		}},
		{"model", "switch model — /model [name]", func(m *Model, args string) tea.Cmd {
			if args != "" {
				m.pendModel = args
				m.model = args
				m.resolveMaxContext()
				m.refresh()
				return m.transientNoteCmd("model set to " + args + " (applies next turn)")
			}
			return m.openModels()
		}},
		{"thinking", "extended thinking — /thinking [on|off]", func(m *Model, args string) tea.Cmd {
			switch strings.ToLower(args) {
			case "on", "enabled", "true":
				m.thinkOn = true
			case "off", "disabled", "false":
				m.thinkOn = false
			default:
				m.thinkOn = !m.thinkOn
			}
			state := "off"
			if m.thinkOn {
				state = "on"
			}
			m.refresh()
			return m.transientNoteCmd("thinking " + state)
		}},
		{"cancel", "cancel the running turn", func(m *Model, _ string) tea.Cmd {
			return m.cancelRun()
		}},
		{"stop", "stop one sub-agent — /stop <SA#>", func(m *Model, args string) tea.Cmd {
			return m.stopByLabel(args)
		}},
		{"agents", "sub-agent registry — c stop · o jump (live poll)", func(m *Model, _ string) tea.Cmd {
			return m.openAgents()
		}},
		{"attach", "stage a file for the next prompt — /attach <path>", func(m *Model, args string) tea.Cmd {
			return m.attachFile(args)
		}},
		{"unattach", "drop staged files — /unattach [name]", func(m *Model, args string) tea.Cmd {
			return m.unattachFile(args)
		}},
		{"quit", "exit bodek", func(m *Model, _ string) tea.Cmd {
			m.quitting = true
			return tea.Quit
		}},
	}
}

// commandPrefix reports the command token when the input is a line-initial
// slash command still being typed (a leading "/" with no whitespace yet).
func commandPrefix(s string) (string, bool) {
	if !strings.HasPrefix(s, "/") {
		return "", false
	}
	body := s[1:]
	if strings.ContainsAny(body, " \t\n") {
		return "", false
	}
	return body, true
}

// runCommandLine parses and dispatches a full "/name args" line.
func (m *Model) runCommandLine(text string) tea.Cmd {
	body := strings.TrimPrefix(text, "/")
	name, args := body, ""
	if i := strings.IndexAny(body, " \t"); i >= 0 {
		name, args = body[:i], strings.TrimSpace(body[i+1:])
	}
	return m.runCommand(name, args)
}

// runCommand finds and executes a command by name, resetting the input.
func (m *Model) runCommand(name, args string) tea.Cmd {
	m.ta.Reset()
	m.syncComposer()
	m.closeAC()
	for _, c := range slashCommands() {
		if c.name == name {
			return c.run(m, args)
		}
	}
	m.refresh()
	return m.transientNoteCmd("unknown command: /" + name + " — try /help")
}

// runSelectedCommand executes the command highlighted in the popup. With
// no matches (a typo like /sesions), the typed line still dispatches —
// enter must always do something, and the unknown-command note teaches.
func (m *Model) runSelectedCommand() tea.Cmd {
	if len(m.ac.items) == 0 {
		if m.ac.mode == acCmd {
			if text := strings.TrimSpace(m.ta.Value()); strings.HasPrefix(text, "/") {
				return m.runCommandLine(text)
			}
		}
		m.closeAC()
		return nil
	}
	name := strings.TrimPrefix(m.ac.items[m.ac.sel].ID, "/")
	return m.runCommand(name, "")
}

// openCmdAC populates the popup with commands matching the typed prefix.
func (m *Model) openCmdAC(query string) {
	var items []client.Resource
	for _, c := range slashCommands() {
		if strings.HasPrefix(c.name, query) {
			items = append(items, client.Resource{
				ID: "/" + c.name, Type: "command", Label: "/" + c.name, Detail: c.desc,
			})
		}
	}
	m.ac.open = true
	m.ac.loading = false
	m.ac.mode = acCmd
	m.ac.query = query
	m.ac.items = items
	m.ac.seq++ // invalidate any in-flight @-search result
	if m.ac.sel >= len(items) {
		m.ac.sel = 0
	}
	m.relayout()
	m.refresh()
}

// themeOptions lists the palettes /theme accepts (aliases included in
// canonicalTheme: light, contrast, dark, default).
const themeOptions = "ember-dark · ember-light · high-contrast · classic"

// runTheme handles /theme: no argument reports the active palette and the
// options; a name switches at runtime and persists via OnThemeChange.
func runTheme(m *Model, args string) tea.Cmd {
	if args == "" {
		return m.transientNoteCmd("theme: " + themeName() + " — options: " + themeOptions)
	}
	return m.switchTheme(args)
}

// switchTheme swaps the active palette mid-run: every style rebuilds from
// the new palette, the glamour renderer is recreated and finalized
// messages re-render through resize(), and the choice persists so the
// next launch starts there (flag > BODEK_THEME > settings file).
func (m *Model) switchTheme(name string) tea.Cmd {
	canonical, ok := canonicalTheme(name)
	if !ok {
		return m.transientNoteCmd("unknown theme: " + name + " — options: " + themeOptions)
	}
	if canonical == themeName() {
		return m.transientNoteCmd(canonical + " is already the active theme")
	}
	themeOverride = canonical
	m.th = themeFrom(paletteByName(canonical))
	m.ta.FocusedStyle.CursorLine = m.th.taCursorLine
	m.logoCache = "" // the banner gradient is palette-dependent
	m.resize(m.width, m.height)
	if m.opts.OnThemeChange != nil {
		if err := m.opts.OnThemeChange(canonical); err != nil {
			return m.transientNoteCmd("theme set to " + canonical + " — not saved: " + sanitize(err.Error()))
		}
	}
	return m.transientNoteCmd("theme set to " + canonical)
}

// showHelp appends a help card listing commands and key bindings. Like /stats
// it is pre-styled to the brand palette (raw), not glamour's stock dark style.
func (m *Model) showHelp() {
	th := m.th
	// Same box math as the /stats card: Width(boxW-2) renders boxW wide with a
	// boxW-4 content column the rules must match.
	boxW := max(min(m.vp.Width-2, 60), 28)
	innerW := boxW - 4
	rule := "\n" + th.rule.Render(strings.Repeat("─", innerW))

	var b strings.Builder
	b.WriteString(th.acTitle.Render("⬡ help"))
	b.WriteString(rule)
	b.WriteString("\n" + th.statsLabel.Render("commands"))
	const cmdW = 10 // longest name is "/thinking"
	for _, c := range slashCommands() {
		b.WriteString("\n" + th.tipKey.Render(padRight("/"+c.name, cmdW)) + " " + th.tipText.Render(c.desc))
	}
	b.WriteString(rule)
	b.WriteString("\n" + th.statsLabel.Render("keys"))
	const keyW = 8 // clears the widest chord in the table (alt+↑↓, --mouse)
	for _, k := range [][2]string{
		{"⏎", "send · queue mid-turn · run a /command"},
		{"^J", "newline in the input"},
		{"@", "attach files"},
		{"↑↓", "scroll the transcript"},
		{"alt+↑↓", "jump to the previous/next turn"},
		{"alt+y", "copy the focused turn's reply (falls back to the latest)"},
		{"^Y", "copy the latest reply"},
		{"alt+r", "re-send the last prompt (/retry)"},
		{"^F", "fold/unfold the latest turn card"},
		{"tab", "open/close the latest reasoning block"},
		{"Pg↑↓", "page the transcript"},
		{"^P^N", "recall prompts"},
		{"^G", "jump to the latest output"},
		{"^R", "browse & resume sessions"},
		{"^Q", "quick-manage the queue strip (full manager: /queue)"},
		{"^O", "switch model"},
		{"^K", "command palette"},
		{"^T", "toggle extended thinking"},
		{"^S", "stop the running sub-agent"},
		{"^L", "clear the conversation"},
		{"^E", "toggle tool details"},
		{"alt+f", "find in the transcript"},
		{"esc", "cancel the running turn (y confirms)"},
		{"/server", "cockpit — server, link, budget, session"},
		{"F1", "this help card"},
		{"^C", "quit"},
		{"--mouse", "wheel scroll · click tool rows & turn heads"},
	} {
		b.WriteString("\n" + th.tipKey.Render(padRight(k[0], keyW)) + " " + th.tipText.Render(k[1]))
	}

	card := th.acBox.Width(boxW - 2).Render(b.String())
	m.msgs = append(m.msgs, message{role: roleAsst, content: card, rendered: card, raw: true})
	m.refresh()
}

// runExport saves the current session transcript next to the user —
// /export [md|json], markdown by default. The server renders the document;
// the write is local, owner-only, and never overwrites.
func runExport(m *Model, arg string) tea.Cmd {
	format := strings.ToLower(strings.TrimSpace(arg))
	if format == "" {
		format = "md"
	}
	if format != "md" && format != "json" {
		return m.transientNoteCmd("unknown format '" + format + "' — usage: /export [md|json]")
	}
	if m.sessionID == "" || m.cl == nil {
		return m.transientNoteCmd("nothing to export yet — no session")
	}
	cl := m.cl
	id := m.sessionID
	token := m.tokens.Get(id)
	return func() tea.Msg {
		_, eff, err := cl.SessionDetail(id, token)
		if err != nil {
			return sessionExportedMsg{id: id, err: err}
		}
		data, err := cl.ExportSession(id, eff, format)
		if err != nil {
			return sessionExportedMsg{id: id, err: err}
		}
		path, err := writeExport(".", id, format, data)
		if err != nil {
			return sessionExportedMsg{id: id, err: err}
		}
		return sessionExportedMsg{id: id, path: path}
	}
}

// showStats appends a session dashboard card to the transcript.
func (m *Model) showStats() {
	card := m.statsCardBody()
	m.msgs = append(m.msgs, message{role: roleAsst, content: card, rendered: card, raw: true})
	m.refresh()
}

// statsCardBody builds the session dashboard: context-window usage, token and
// tool totals, latency, thinking ratio, session age, and model/sandbox. It is
// built as pre-styled lines (raw) so its colors and column alignment are exact
// and survive width changes untouched.
func (m *Model) statsCardBody() string {
	card := m.statsBody()
	// Same box math the card always used — changing it re-wraps the
	// pre-styled rows at narrow widths.
	return m.th.acBox.Width(max(min(m.vp.Width-2, 60), 28) - 2).Render(card)
}

// statsBody renders the session dashboard content without a frame — the
// cockpit embeds it directly; /stats wraps it in its card.
func (m *Model) statsBody() string {
	th := m.th
	// boxW is the total rendered width (incl. border). lipgloss .Width(w) makes
	// the text content w-2 wide (padding) and adds 2 for the border, so passing
	// boxW-2 yields a box exactly boxW wide with a boxW-4 content column — which
	// the divider and rows must match exactly to keep the right edge flush.
	boxW := max(min(m.vp.Width-2, 60), 28)
	innerW := boxW - 4

	// A labelled row: an accent-tinted glyph, a muted word, then the value.
	type row struct {
		glyph string
		style lipgloss.Style
		label string
		value string
	}
	var rows []row

	if len(m.turnStats) > 0 {
		var sumLat, peakLat float64
		thinkN := 0
		for _, t := range m.turnStats {
			sumLat += t.latency
			if t.latency > peakLat {
				peakLat = t.latency
			}
			if t.thought {
				thinkN++
			}
		}
		mean := sumLat / float64(len(m.turnStats))

		ctxVal := th.statsValue.Render(human(m.winCtxTok))
		if m.maxContext > 0 {
			ratio := float64(m.winCtxTok) / float64(m.maxContext)
			if ratio > 1 {
				ratio = 1
			}
			ctxVal = th.statsValue.Render(human(m.winCtxTok)+"/"+humanCtx(m.maxContext)) +
				"  " + m.gaugeColor(ratio).Render(gaugeGlyph(ratio)) +
				" " + th.statsDim.Render(fmt.Sprintf("%d%%", int(ratio*100+0.5)))
		}

		latVal := th.statsValue.Render(fmt.Sprintf("%.1fs", mean))
		if peakLat > mean+0.05 {
			latVal += th.statsDim.Render(fmt.Sprintf("  · slowest %.1fs", peakLat))
		}

		think := "off"
		if m.thinkOn {
			think = "on"
		}
		// Budget the (sanitized) model id so the " · think …" suffix and the
		// label gutter still fit the content column without wrapping.
		suffix := " · think " + think
		modelBudget := innerW - 11 - lipgloss.Width(suffix)
		if modelBudget < 8 {
			modelBudget = 8
		}
		modelID := truncate(collapse(orDash(m.model)), modelBudget)

		rows = []row{
			{"⌂", th.statCtx, "context", ctxVal},
			{"⎇", th.statCtx, "output", th.statsValue.Render(human(m.sessOutTok))},
			{"↻", th.statsLabel, "turns", th.statsValue.Render(fmt.Sprintf("%d", len(m.turnStats)))},
			{"⚒", th.statTool, "tools", th.statsValue.Render(fmt.Sprintf("%d", m.toolTotal))},
			{"⚡", th.statTime, "latency", latVal},
			{"✳", th.statThink, "thinking", th.statsValue.Render(fmt.Sprintf("%d of %d turns", thinkN, len(m.turnStats)))},
			{"◷", th.statsLabel, "active", th.statsValue.Render(formatDuration(time.Since(m.sessionStart)))},
			{"⬡", th.statThink, "model", th.statsValue.Render(modelID) + th.statsDim.Render(" · think "+think)},
		}

		// Session cost from cumulative session tokens — correct across
		// reconnect/resume where turnStats would be incomplete. The server's
		// effective_prices apply when the model matches its configuration;
		// otherwise the client-side twin resolves it. Hidden unless odek has
		// both token prices configured.
		if inPrice, outPrice := m.prices(); inPrice > 0 && outPrice > 0 {
			costVal := th.statsValue.Render(formatUSD(costUSD(m.sessCtxTok, m.sessOutTok, inPrice, outPrice) + m.subCostTotal()))
			if m.limits.MaxCostUSD > 0 {
				costVal += th.statsDim.Render("  · cap " + formatUSD(m.limits.MaxCostUSD))
			}
			rows = slices.Insert(rows, 2, row{"$", th.statCtx, "cost", costVal})
		}

		// Provider cache activity across turns — only when any was reported.
		var cacheW, cacheR, cacheC int
		for _, t := range m.turnStats {
			cacheW += t.cacheWrite
			cacheR += t.cacheRead
			cacheC += t.cachedTok
		}
		if cacheW > 0 || cacheR > 0 || cacheC > 0 {
			cacheVal := th.statsValue.Render(fmt.Sprintf("%s w · %s r", human(cacheW), human(cacheR)))
			if cacheC > 0 {
				cacheVal += th.statsDim.Render(fmt.Sprintf(" · %s prefix", human(cacheC)))
			}
			rows = append(rows, row{"⛁", th.statCtx, "cache", cacheVal})
		}

		// Server link from the latest heartbeat snapshot — RTT, uptime, and
		// the live connection count. Hidden when no snapshot has arrived yet.
		if m.rtt > 0 || m.srvConns > 0 {
			link := th.statsValue.Render(fmt.Sprintf("%s rtt", formatStepDur(m.rtt)))
			if m.srvUptime > 0 {
				link += th.statsDim.Render(fmt.Sprintf(" · up %s", formatDuration(m.srvUptime)))
			}
			if m.srvConns > 0 {
				link += th.statsDim.Render(fmt.Sprintf(" · %d ws", m.srvConns))
			}
			if m.serverStream {
				link += th.statsDim.Render(" · ⚡ stream")
			}
			rows = append(rows, row{"⇄", th.statsLabel, "link", link})
		}
	}

	// Align values into a column just past the widest label.
	gutter := 0
	for _, r := range rows {
		if w := lipgloss.Width(r.glyph + " " + r.label); w > gutter {
			gutter = w
		}
	}
	gutter++ // one space before the value column

	var b strings.Builder
	title := th.acTitle.Render("⬡ session")
	if id := shortID(m.sessionID); id != "" {
		title += " " + th.statsDim.Render(id)
	}
	b.WriteString(title)
	b.WriteString("\n" + th.rule.Render(strings.Repeat("─", innerW)))

	if len(rows) == 0 {
		b.WriteString("\n" + th.statsDim.Render("no turns yet — ask odek something"))
	} else {
		for _, r := range rows {
			styled := r.style.Render(r.glyph) + " " + th.statsLabel.Render(r.label)
			pad := gutter - lipgloss.Width(r.glyph+" "+r.label)
			if pad < 1 {
				pad = 1
			}
			b.WriteString("\n" + styled + strings.Repeat(" ", pad) + r.value)
		}
		idline := m.sandboxBadge()
		if !m.sessionStart.IsZero() {
			idline += th.statsDim.Render(" · started " + ago(m.sessionStart))
		}
		b.WriteString("\n" + th.rule.Render(strings.Repeat("─", innerW)))
		b.WriteString("\n" + idline)
	}

	_ = boxW
	return b.String()
}
