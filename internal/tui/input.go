package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// handleACKey navigates/accepts/dismisses the completion popup (@ files and
// sessions, or / commands) while it has keyboard capture.
func (m *Model) handleACKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "ctrl+p":
		if m.ac.sel > 0 {
			m.ac.sel--
			m.refresh()
		}
		return m, nil
	case "down", "ctrl+n":
		if m.ac.sel < len(m.ac.items)-1 {
			m.ac.sel++
			m.refresh()
		}
		return m, nil
	case "tab":
		m.acceptCompletion()
		return m, nil
	case "enter":
		// A fully-typed command executes; a reference is inserted.
		if m.ac.mode == acCmd {
			return m, m.runSelectedCommand()
		}
		m.acceptCompletion()
		return m, nil
	case "esc":
		m.closeAC()
		return m, nil
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}
	// Any other key is plain input: forward it to the textarea, then
	// re-evaluate the popup against the new value — typing narrows the
	// filter, backspace widens it, a space ends command completion.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, tea.Batch(cmd, m.syncAC())
}

// acMode selects what the completion popup is completing.
type acMode int

const (
	acRef acMode = iota // @-references (files/sessions), searched server-side
	acCmd               // slash commands, filtered locally
)

// autocomplete holds the completion popup state (shared by @ and / modes).
type autocomplete struct {
	open    bool
	loading bool
	mode    acMode
	query   string
	items   []client.Resource
	sel     int
	seq     int // request sequence, to drop stale responses
}

// rows is the number of list rows the popup renders.
func (a autocomplete) rows() int {
	if len(a.items) == 0 {
		return 1 // "searching…" / "no matches"
	}
	return len(a.items)
}

// height is the total rendered height of the popup (border + title + rows).
func (a autocomplete) height() int {
	return a.rows() + 3
}

// acResultMsg carries the result of an async resource search.
type acResultMsg struct {
	seq   int
	items []client.Resource
}

func (m *Model) submit() tea.Cmd {
	text := strings.TrimSpace(m.ta.Value())
	if text == "" {
		// Enter with an empty input is the manual retry while disconnected —
		// a character key can never carry this job without hijacking typing.
		if m.disconn && m.opts.Reconnect != nil {
			m.status = "reconnecting"
			note := m.transientNoteCmd("retrying connection…")
			m.refresh()
			return tea.Batch(m.scheduleReconnect(0), note)
		}
		return nil
	}
	// Slash commands run locally and are allowed even mid-turn (e.g. /cancel).
	if strings.HasPrefix(text, "/") {
		return m.runCommandLine(text)
	}
	if m.disconn {
		// Keep the draft — swallowing it silently reads as a lost message.
		// Alert tier: it dwells past a glance away but still autocloses,
		// and is re-posted (deduped) on every enter while disconnected.
		// Retry is ⏎ on an empty input — the hint spells that out.
		const warn = "disconnected — your draft is kept · clear the input, then ⏎ to retry"
		if n := len(m.notices); n == 0 || m.notices[n-1] != warn {
			m.addNote(warn)
		}
		m.refresh()
		return m.noticeSweep()
	}
	if m.busy {
		// Queue mid-turn prompts instead of dropping them; the queue drains
		// automatically when the running turn ends. Acknowledge the hold —
		// the input clearing silently reads as a lost message (same
		// rationale as the disconnected-draft warning below).
		m.queue = append(m.queue, text)
		m.ta.Reset()
		m.closeAC()
		m.refresh()
		m.vp.GotoBottom() // Enter means "show me the latest", even mid-turn
		return m.transientNoteCmd("queued — it sends when the turn ends")
	}
	m.ta.Reset()
	m.closeAC()
	return m.sendPrompt(text)
}

// sendPrompt appends the user/assistant pair to the transcript, records the
// prompt in the history ring, and dispatches it to the server.
func (m *Model) sendPrompt(text string) tea.Cmd {
	m.lastPrompt = text
	m.recordHistory(text)
	shown := text
	if n := len(m.attachments); n > 0 {
		shown = fmt.Sprintf("%s  📎×%d", shown, n)
	}
	m.msgs = append(m.msgs, message{role: roleUser, content: shown, sentAt: time.Now()})
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.ta.Reset()
	m.closeAC()
	m.busy = true
	m.cancelAck = false  // a fresh run's errors are real errors again
	m.skillSuggest = nil // the suggestion's window closed with the turn
	m.status = "thinking"
	m.runStart = time.Now()
	if m.sessionStart.IsZero() {
		m.sessionStart = m.runStart
	}
	m.relayout() // the busy status line claims a row above the input
	m.refresh()
	// Submitting is an explicit "show me the latest" signal — jump to the
	// bottom even when the reader was up in the scrollback (refresh alone
	// only sticks when already at the bottom).
	m.vp.GotoBottom()

	thinking := ""
	if m.thinkOn {
		thinking = "enabled"
	}
	opts := client.PromptOpts{
		Thinking:    thinking,
		Model:       m.pendModel,
		SessionID:   m.sessionID,
		AuthToken:   m.authToken,
		Attachments: m.attachments,
	}
	// Attachments are per-prompt by contract; the next send starts clean.
	m.attachments = nil
	m.pendModel = "" // applied
	cl := m.cl
	send := func() tea.Msg {
		if err := cl.SendPrompt(text, opts); err != nil {
			return errMsg{err}
		}
		return nil
	}
	if m.plain {
		// Linear mode: the prompt joins the scrollback log above the chrome.
		return tea.Batch(send, tea.Println(plainPromptLine(text)))
	}
	return send
}

// sendQueued pops the oldest queued prompt and sends it when the model is
// idle and connected; otherwise it is a no-op (nil cmd).
func (m *Model) sendQueued() tea.Cmd {
	if m.busy || m.disconn || len(m.queue) == 0 {
		return nil
	}
	text := m.queue[0]
	m.queue = m.queue[1:]
	m.qsel = clampSel(m.qsel, len(m.queue))
	if len(m.queue) == 0 {
		m.qfocus = false
	}
	return m.sendPrompt(text)
}

// retryLast re-sends the most recent prompt: immediately when idle, queued
// when a turn is running — the same path a mid-turn typed prompt takes.
func (m *Model) retryLast() tea.Cmd {
	if m.lastPrompt == "" {
		return m.transientNoteCmd("nothing to retry yet — send a prompt first")
	}
	if m.busy {
		m.queue = append(m.queue, m.lastPrompt)
		return m.transientNoteCmd("retry queued — it sends when the turn ends")
	}
	return m.sendPrompt(m.lastPrompt)
}

// maxHistory bounds the in-memory prompt history ring.
const maxHistory = 100

// recordHistory appends a submitted prompt to the history ring (deduping
// consecutive repeats) and resets any active history navigation.
func (m *Model) recordHistory(text string) {
	m.histNav = false
	m.histDraft = ""
	if n := len(m.history); n > 0 && m.history[n-1] == text {
		return
	}
	m.history = append(m.history, text)
	if len(m.history) > maxHistory {
		m.history = m.history[len(m.history)-maxHistory:]
	}
}

// historyPrev steps back through the prompt history, stashing the current
// input on the first step. Returns false when there is nothing to recall, so
// the caller can fall back to scrolling. At the oldest entry the key is
// consumed without moving.
func (m *Model) historyPrev() bool {
	if len(m.history) == 0 {
		return false
	}
	switch {
	case !m.histNav:
		m.histDraft = m.ta.Value()
		m.histIdx = len(m.history) - 1
		m.histNav = true
	case m.histIdx > 0:
		m.histIdx--
	}
	m.ta.SetValue(m.history[m.histIdx])
	m.ta.CursorEnd()
	return true
}

// historyNext steps forward through the history; past the newest entry it
// restores the stashed draft and leaves navigation mode.
func (m *Model) historyNext() {
	if !m.histNav {
		return
	}
	if m.histIdx < len(m.history)-1 {
		m.histIdx++
		m.ta.SetValue(m.history[m.histIdx])
	} else {
		m.histNav = false
		m.ta.SetValue(m.histDraft)
		m.histDraft = ""
	}
	m.ta.CursorEnd()
}

// ── @-reference autocomplete ────────────────────────────────────────────────

// refRe matches a trailing @-reference token at the end of the input.
var refRe = regexp.MustCompile(`(^|\s)@([^\s@]*)$`)

// activeRef returns the query of the trailing @-token, if the cursor is in one.
func activeRef(s string) (string, bool) {
	mm := refRe.FindStringSubmatch(s)
	if mm == nil {
		return "", false
	}
	return mm[2], true
}

// refStart returns the byte index of the '@' that begins the trailing token.
func refStart(s string) (int, bool) {
	loc := refRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return 0, false
	}
	return loc[4] - 1, true // group 2 start, minus the '@'
}

// syncAC re-evaluates the input and drives the completion popup — slash
// commands (filtered locally) or @-references (searched server-side).
func (m *Model) syncAC() tea.Cmd {
	val := m.ta.Value()

	// Line-initial slash command completion.
	if name, ok := commandPrefix(val); ok {
		if m.ac.open && m.ac.mode == acCmd && m.ac.query == name {
			return nil
		}
		m.openCmdAC(name)
		return nil
	}

	q, ok := activeRef(val)
	if !ok {
		if m.ac.open {
			m.closeAC()
		}
		return nil
	}
	if m.ac.open && m.ac.mode == acRef && q == m.ac.query {
		return nil // nothing changed
	}
	m.ac.open = true
	m.ac.loading = true
	m.ac.mode = acRef
	m.ac.query = q
	m.ac.sel = 0
	m.ac.seq++
	seq := m.ac.seq
	m.relayout()
	m.refresh()

	cl := m.cl
	return func() tea.Msg {
		// @ is for file attachments only; sessions are reached via /sessions
		// (or ^R). Over-fetch, then keep just files.
		items, err := cl.Resources(q, 12)
		if err != nil {
			return acResultMsg{seq: seq, items: nil}
		}
		files := make([]client.Resource, 0, len(items))
		for _, it := range items {
			if it.Type == "file" {
				files = append(files, it)
			}
		}
		if len(files) > 6 {
			files = files[:6]
		}
		return acResultMsg{seq: seq, items: files}
	}
}

// acceptCompletion inserts the highlighted item into the input.
func (m *Model) acceptCompletion() {
	if len(m.ac.items) == 0 {
		m.closeAC()
		return
	}
	item := m.ac.items[m.ac.sel]
	if m.ac.mode == acCmd {
		m.ta.SetValue(item.ID + " ")
		m.ta.CursorEnd()
		m.closeAC()
		return
	}
	val := m.ta.Value()
	if idx, ok := refStart(val); ok {
		m.ta.SetValue(val[:idx] + item.ID + " ")
		m.ta.CursorEnd()
	}
	m.closeAC()
}

// closeAC dismisses the completion popup and restores the layout.
func (m *Model) closeAC() {
	if !m.ac.open && m.ac.items == nil {
		return
	}
	m.ac = autocomplete{seq: m.ac.seq}
	m.relayout()
	m.refresh()
}
