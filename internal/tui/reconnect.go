package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// maxReconnectAttempts bounds how many redials follow a socket drop before
// the TUI settles into its terminal disconnected state.
const maxReconnectAttempts = 5

// reconnectMsg carries the outcome of one redial attempt.
type reconnectMsg struct {
	attempt int
	cl      *client.Client
	err     error
}

// reconnectBackoff delays attempt n by 500ms·2ⁿ, capped at 8s.
func reconnectBackoff(attempt int) time.Duration {
	d := 500 * time.Millisecond << uint(attempt)
	if d > 8*time.Second {
		return 8 * time.Second
	}
	return d
}

// scheduleReconnect runs one redial (via the Reconnect hook main wires in)
// after the attempt's backoff tick. Nil hook means reconnects are disabled.
func (m *Model) scheduleReconnect(attempt int) tea.Cmd {
	hook := m.opts.Reconnect
	if hook == nil {
		return nil
	}
	return tea.Tick(reconnectBackoff(attempt), func(time.Time) tea.Msg {
		cl, err := hook()
		return reconnectMsg{attempt: attempt, cl: cl, err: err}
	})
}

// handleReconnect applies a redial outcome: success swaps the client and
// re-arms the event stream; failure retries with backoff until the attempt
// budget is spent, then keeps the terminal disconnected state.
func (m *Model) handleReconnect(msg reconnectMsg) (tea.Model, tea.Cmd) {
	if !m.disconn {
		return m, nil // stale result (e.g. the user quit and restarted)
	}
	if msg.err == nil && msg.cl != nil {
		m.cl = msg.cl
		m.events = msg.cl.Events
		m.disconn = false
		m.status = "ready"
		// Session continuity survives the drop: session_switch adopts the
		// session on the fresh connection (restoring the server-side memory
		// buffer) without waiting for a prompt, and every prompt still carries
		// session_id + auth_token as the belt-and-suspenders fallback.
		m.addNote("reconnected to odek serve — the session resumes on your next prompt")
		m.refresh()
		return m, tea.Batch(listen(m.events), m.adoptSession(), m.sendQueued())
	}
	if msg.attempt+1 < maxReconnectAttempts {
		return m, m.scheduleReconnect(msg.attempt + 1)
	}
	m.status = "disconnected"
	m.addNote("reconnect failed — " + msg.err.Error() + " · press r to retry")
	if m.opts.LogPath != "" {
		m.addNote("server log · " + m.opts.LogPath)
	}
	m.refresh()
	return m, nil
}
