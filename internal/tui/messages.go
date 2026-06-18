package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// eventMsg wraps a decoded server event for the Bubble Tea update loop.
type eventMsg client.Event

// errMsg reports a local (non-protocol) failure, e.g. a failed socket write.
type errMsg struct{ err error }

// listen blocks on the client's event channel and returns the next event as a
// tea.Msg. It is re-armed after each event so the stream is continuous.
func listen(ch <-chan client.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return eventMsg{Type: client.EventDisconnected}
		}
		return eventMsg(ev)
	}
}
