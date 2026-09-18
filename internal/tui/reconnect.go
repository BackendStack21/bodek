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
	gen     int // scheduleReconnect chain that produced this result
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
// reconnGen counts reconnect chains. A manual ⏎ retry must not race a
// pending backoff tick into two concurrent hook dials: every schedule
// bumps the generation, and a reconnectMsg from a superseded chain is
// dropped (and its socket closed).
func (m *Model) scheduleReconnect(attempt int) tea.Cmd {
	hook := m.opts.Reconnect
	if hook == nil {
		return nil
	}
	m.reconnGen++
	gen := m.reconnGen
	m.reconnAttempt = attempt // the status line's backoff readout follows the chain
	return tea.Tick(reconnectBackoff(attempt), func(time.Time) tea.Msg {
		cl, err := hook()
		return reconnectMsg{attempt: attempt, gen: gen, cl: cl, err: err}
	})
}

// handleReconnect applies a redial outcome: success swaps the client and
// re-arms the event stream; failure retries with backoff until the attempt
// budget is spent, then keeps the terminal disconnected state.
func (m *Model) handleReconnect(msg reconnectMsg) (tea.Model, tea.Cmd) {
	if !m.disconn || msg.gen != m.reconnGen {
		// Stale result (superseded chain, or the user quit and restarted):
		// a successful dial nobody adopted would leak its socket — close it.
		if msg.cl != nil {
			_ = msg.cl.Close()
		}
		return m, nil
	}
	if msg.err == nil && msg.cl != nil {
		if old := m.cl; old != nil && old != msg.cl {
			// The dead client's readLoop exits on its own, but nothing closed
			// its socket — every drop leaked an fd and a live server slot.
			_ = old.Close()
		}
		m.cl = msg.cl
		m.events = msg.cl.Events
		m.disconn = false
		m.status = "ready"
		// Session continuity survives the drop: session_switch adopts the
		// session on the fresh connection (restoring the server-side memory
		// buffer) without waiting for a prompt, and every prompt still carries
		// session_id + auth_token as the belt-and-suspenders fallback.
		note := m.transientNoteCmd("reconnected to odek serve — the session resumes on your next prompt")
		if m.freshStart {
			// /new dropped the identity on purpose: nothing was resumed —
			// the first prompt mints a brand-new session server-side.
			m.freshStart = false
			note = m.transientNoteCmd("fresh session — your next prompt starts a new conversation")
		}
		m.refresh()
		return m, tea.Batch(listen(m.events), m.adoptSession(), m.sendQueued(), note)
	}
	if msg.attempt+1 < maxReconnectAttempts {
		return m, m.scheduleReconnect(msg.attempt + 1)
	}
	m.status = "disconnected"
	reason := "no client"
	if msg.err != nil {
		reason = msg.err.Error()
	}
	m.addNote("reconnect failed — " + reason + " · press ⏎ to retry")
	if m.opts.LogPath != "" {
		m.addNote("server log · " + m.opts.LogPath)
	}
	m.refresh()
	return m, nil
}
