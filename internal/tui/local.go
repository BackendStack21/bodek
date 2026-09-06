package tui

import (
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/workspace"
)

// persistDebounce is how long an idle composer waits before the draft hits
// disk. Crash survival, not a keystroke journal.
const persistDebounce = 400 * time.Millisecond

type persistDraftMsg struct{ seq int }

// restoreWorkspace applies the cwd snapshot: history, draft, queue, staged
// paths, and (unless --new) a pending last-session resume. The queue is
// restored, never sent.
func (m *Model) restoreWorkspace() {
	if m.ws == nil || m.opts.CWD == "" {
		return
	}
	st := m.ws.Load(m.opts.CWD)
	if n := len(st.History); n > 0 {
		m.history = append([]string(nil), st.History...)
		if n > maxHistory {
			m.history = m.history[n-maxHistory:]
		}
	}
	if st.Draft != "" {
		m.ta.SetValue(st.Draft)
		m.syncComposer()
	}
	if len(st.Queue) > 0 {
		m.queue = append([]string(nil), st.Queue...)
	}
	for _, p := range st.Attachments {
		if p == "" {
			continue
		}
		_ = m.attachFile(p)
	}
	m.resumeTitle = st.SessionTitle
	if !m.opts.Fresh && st.SessionID != "" {
		m.pendingResume = st.SessionID
	}
}

// persistLocal writes the current cwd snapshot. No tokens, no wire content.
func (m *Model) persistLocal() {
	if m.ws == nil || m.opts.CWD == "" {
		return
	}
	m.ws.Patch(m.opts.CWD, func(st *workspace.State) {
		st.History = append([]string(nil), m.history...)
		st.Draft = m.ta.Value()
		st.Queue = append([]string(nil), m.queue...)
		st.Attachments = append([]string(nil), m.attachPaths...)
		if m.sessionID != "" && !m.freshStart {
			st.SessionID = m.sessionID
		}
	})
}

// rememberSession records the cwd → session mapping (id + sanitized title).
func (m *Model) rememberSession(title string) {
	if m.ws == nil || m.opts.CWD == "" || m.sessionID == "" || m.freshStart {
		return
	}
	title = sanitize(title)
	m.ws.Patch(m.opts.CWD, func(st *workspace.State) {
		st.SessionID = m.sessionID
		if title != "" {
			st.SessionTitle = title
		}
	})
}

// schedulePersist arms a generation-guarded draft flush.
func (m *Model) schedulePersist() tea.Cmd {
	if m.ws == nil {
		return nil
	}
	m.persistSeq++
	seq := m.persistSeq
	return tea.Tick(persistDebounce, func(time.Time) tea.Msg {
		return persistDraftMsg{seq: seq}
	})
}

func (m *Model) handlePersistDraft(msg persistDraftMsg) {
	if msg.seq != m.persistSeq {
		return
	}
	m.persistLocal()
}

// resumeLast kicks SessionDetail for the cwd's last session. Failures stay
// a note — the operator can /new or ^R. Live approvals come from the
// engine after adopt; bodek does not restore a local approval form.
func (m *Model) resumeLast() tea.Cmd {
	id := m.pendingResume
	if id == "" || m.cl == nil {
		return nil
	}
	m.pendingResume = ""
	cl := m.cl
	token := m.tokens.Get(id)
	return func() tea.Msg {
		sess, eff, err := cl.SessionDetail(id, token)
		return sessionDetailMsg{sess: sess, token: eff, err: err}
	}
}

// restoreComposerPrompt puts lastPrompt back in an empty composer so a
// cancelled or failed turn can be edited. /retry still sends the same bytes.
func (m *Model) restoreComposerPrompt() {
	if m.ta.Value() != "" || m.lastPrompt == "" {
		return
	}
	m.ta.SetValue(m.lastPrompt)
	m.ta.CursorEnd()
	m.syncComposer()
}

// MissingProvider reports whether no known provider env key is set. Bodek
// never reads ~/.odek/config.json — a file-only key is invisible here.
func MissingProvider() bool {
	for _, k := range []string{
		"ODEK_API_KEY", "DEEPSEEK_API_KEY", "OPENAI_API_KEY", "ZAI_API_KEY",
		"ANTHROPIC_API_KEY", "GEMINI_API_KEY", "OPENROUTER_API_KEY",
	} {
		if os.Getenv(k) != "" {
			return false
		}
	}
	return true
}
