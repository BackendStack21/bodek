package tui

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"github.com/BackendStack21/bodek/internal/client"
)

// role identifies who authored a conversation entry.
type role int

const (
	roleUser role = iota
	roleAsst
	roleNote
)

// step is a single tool invocation within an assistant turn.
type step struct {
	name   string
	arg    string
	result string
	done   bool
}

// message is one entry in the transcript.
type message struct {
	role      role
	content   string // raw text/markdown
	rendered  string // cached glamour render (assistant, finalized)
	steps     []step
	streaming bool
}

// Options carries startup display info into the model.
type Options struct {
	Model   string
	Sandbox bool
	CWD     string
}

// Model is the Bubble Tea model for bodek.
type Model struct {
	cl     *client.Client
	events <-chan client.Event
	opts   Options
	th     theme

	width, height int
	ready         bool

	vp   viewport.Model
	ta   textarea.Model
	sp   spinner.Model
	glam *glamour.TermRenderer

	msgs     []message
	curIdx   int // index of the streaming assistant message, -1 when idle
	busy     bool
	thinking strings.Builder

	approval *client.Event // pending approval, nil when none

	model     string
	sandbox   bool
	sessionID string
	thinkOn   bool

	sessCtxTok  int
	sessOutTok  int
	lastLatency float64

	status   string
	notices  []string
	disconn  bool
	quitting bool
}

// New builds the initial model.
func New(cl *client.Client, opts Options) *Model {
	th := newTheme()

	ta := textarea.New()
	ta.Placeholder = "Ask odek to build, fix, explore… (⏎ send · ^J newline)"
	ta.Prompt = th.asstLabel.Render("┃ ")
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.FocusedStyle.CursorLine = th.taCursorLine
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = th.spinner

	return &Model{
		cl:      cl,
		events:  cl.Events,
		opts:    opts,
		th:      th,
		ta:      ta,
		sp:      sp,
		curIdx:  -1,
		model:   opts.Model,
		sandbox: opts.Sandbox,
		thinkOn: false,
		status:  "ready",
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.sp.Tick, listen(m.events))
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, m.resize(msg.Width, msg.Height)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		if m.busy {
			m.refresh()
		}
		return m, cmd

	case errMsg:
		m.busy = false
		m.status = "error"
		m.addNote("error: " + msg.err.Error())
		m.refresh()
		return m, nil

	case eventMsg:
		return m.handleEvent(client.Event(msg))

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	// Forward anything else to the focused input.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Approval mode captures the keyboard until answered.
	if m.approval != nil {
		switch msg.String() {
		case "a", "y":
			return m, m.answer("approve")
		case "d", "n":
			return m, m.answer("deny")
		case "t":
			if m.approval.AllowTrust {
				return m, m.answer("trust")
			}
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "enter":
		return m, m.submit()
	case "ctrl+j":
		// Insert a newline into the textarea.
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return m, cmd
	case "ctrl+t":
		m.thinkOn = !m.thinkOn
		return m, nil
	case "ctrl+l":
		if !m.busy {
			m.msgs = nil
			m.refresh()
		}
		return m, nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m *Model) handleEvent(ev client.Event) (tea.Model, tea.Cmd) {
	switch ev.Type {
	case "session":
		m.sessionID = ev.SessionID
		if ev.Model != "" {
			m.model = ev.Model
		}
		m.sandbox = ev.Sandbox

	case "thinking":
		m.thinking.WriteString(ev.Content)
		m.status = "thinking"

	case "token":
		if i := m.cur(); i >= 0 {
			m.msgs[i].content += ev.Content
			m.msgs[i].streaming = true
		}
		m.status = "responding"

	case "tool_call":
		if i := m.cur(); i >= 0 {
			m.msgs[i].steps = append(m.msgs[i].steps, step{name: ev.Name, arg: argPreview(ev.Data)})
		}
		m.status = "running " + ev.Name

	case "tool_result":
		if i := m.cur(); i >= 0 {
			steps := m.msgs[i].steps
			for j := len(steps) - 1; j >= 0; j-- {
				if steps[j].name == ev.Name && !steps[j].done {
					steps[j].done = true
					steps[j].result = linePreview(ev.Data)
					break
				}
			}
		}

	case "done":
		m.finalize()
		m.busy = false
		m.status = "ready"
		m.sessCtxTok = ev.SessionContextTokens
		m.sessOutTok = ev.SessionOutputTokens
		m.lastLatency = ev.Latency

	case "error":
		if i := m.cur(); i >= 0 && m.msgs[i].content == "" {
			m.msgs[i].content = "**Error:** " + ev.Message
		} else {
			m.addNote("error: " + ev.Message)
		}
		m.finalize()
		m.busy = false
		m.status = "error"

	case "approval_request":
		e := ev
		m.approval = &e
		m.status = "approval required"

	case "skill_event":
		m.addNote("skill · " + strings.TrimSpace(ev.SubType+" "+ev.SkillName))
	case "memory_event":
		m.addNote("memory · " + strings.TrimSpace(ev.SubType+" "+ev.Target))
	case "agent_signal":
		m.addNote("signal · " + strings.TrimSpace(ev.SubType+" "+ev.Detail))
	case "subagent_log":
		m.addNote("subagent · " + strings.TrimSpace(ev.SubType+" "+ev.Name))

	case client.EventDisconnected:
		m.disconn = true
		m.busy = false
		m.status = "disconnected"
		m.addNote("disconnected from odek serve")
		m.refresh()
		return m, nil
	}

	m.refresh()
	return m, listen(m.events)
}

// ── actions ──────────────────────────────────────────────────────────────

func (m *Model) submit() tea.Cmd {
	if m.busy || m.disconn {
		return nil
	}
	text := strings.TrimSpace(m.ta.Value())
	if text == "" {
		return nil
	}
	m.msgs = append(m.msgs, message{role: roleUser, content: text})
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.ta.Reset()
	m.busy = true
	m.status = "thinking"
	m.thinking.Reset()
	m.refresh()

	thinking := ""
	if m.thinkOn {
		thinking = "enabled"
	}
	cl := m.cl
	return func() tea.Msg {
		if err := cl.SendPrompt(text, thinking, ""); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m *Model) answer(action string) tea.Cmd {
	id := m.approval.ID
	m.approval = nil
	m.status = "thinking"
	m.refresh()
	cl := m.cl
	return func() tea.Msg {
		if err := cl.SendApproval(id, action); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

// cur returns the index of the active streaming assistant message, or -1.
func (m *Model) cur() int {
	if m.curIdx >= 0 && m.curIdx < len(m.msgs) {
		return m.curIdx
	}
	return -1
}

// finalize closes out the streaming assistant message, rendering its markdown.
func (m *Model) finalize() {
	if i := m.cur(); i >= 0 {
		m.msgs[i].streaming = false
		m.msgs[i].rendered = m.render(m.msgs[i].content)
	}
	m.curIdx = -1
	m.thinking.Reset()
}

func (m *Model) addNote(s string) {
	m.notices = append(m.notices, s)
	if len(m.notices) > 6 {
		m.notices = m.notices[len(m.notices)-6:]
	}
}

// render runs content through glamour; falls back to raw text on error.
func (m *Model) render(content string) string {
	if m.glam == nil || strings.TrimSpace(content) == "" {
		return content
	}
	out, err := m.glam.Render(content)
	if err != nil {
		return content
	}
	return strings.TrimRight(out, "\n")
}

func (m *Model) resize(w, h int) tea.Cmd {
	m.width, m.height = w, h

	vpHeight := h - headerHeight - inputHeight - footerHeight
	if vpHeight < 3 {
		vpHeight = 3
	}
	if !m.ready {
		m.vp = viewport.New(w, vpHeight)
		m.ready = true
	} else {
		m.vp.Width = w
		m.vp.Height = vpHeight
	}
	m.ta.SetWidth(w - 4)

	wrap := w - 6
	if wrap < 20 {
		wrap = 20
	}
	if r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(wrap),
	); err == nil {
		m.glam = r
		// Re-render finalized assistant messages at the new width.
		for i := range m.msgs {
			if m.msgs[i].role == roleAsst && !m.msgs[i].streaming {
				m.msgs[i].rendered = m.render(m.msgs[i].content)
			}
		}
	}
	m.refresh()
	return nil
}

// argPreview extracts a short, human-friendly summary from a tool's JSON args.
func argPreview(data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return truncate(collapse(data), 72)
	}
	for _, key := range []string{"command", "cmd", "path", "file", "pattern", "query", "url"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return truncate(collapse(s), 72)
			}
		}
	}
	parts := make([]string, 0, len(m))
	for _, v := range m {
		if s, ok := v.(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return truncate(collapse(strings.Join(parts, " ")), 72)
}

// linePreview returns the first meaningful line of tool output, truncated.
func linePreview(data string) string {
	return truncate(collapse(data), 72)
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
