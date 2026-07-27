package tui

import (
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
		return nil
	}
	// Slash commands run locally and are allowed even mid-turn (e.g. /cancel).
	if strings.HasPrefix(text, "/") {
		return m.runCommandLine(text)
	}
	if m.busy || m.disconn {
		return nil
	}
	m.msgs = append(m.msgs, message{role: roleUser, content: text})
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.ta.Reset()
	m.closeAC()
	m.busy = true
	m.status = "thinking"
	m.runStart = time.Now()
	if m.sessionStart.IsZero() {
		m.sessionStart = m.runStart
	}
	m.refresh()

	thinking := ""
	if m.thinkOn {
		thinking = "enabled"
	}
	opts := client.PromptOpts{
		Thinking:  thinking,
		Model:     m.pendModel,
		SessionID: m.sessionID,
		AuthToken: m.authToken,
	}
	m.pendModel = "" // applied
	cl := m.cl
	return func() tea.Msg {
		if err := cl.SendPrompt(text, opts); err != nil {
			return errMsg{err}
		}
		return nil
	}
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
