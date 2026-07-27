package tui

import (
	"context"
	"net/http"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/BackendStack21/bodek/internal/update"
)

// eventMsg wraps a decoded server event for the Bubble Tea update loop.
type eventMsg client.Event

// errMsg reports a local (non-protocol) failure, e.g. a failed socket write.
type errMsg struct{ err error }

// updateCheckMsg carries the startup latest-release lookup. The error is
// silent: a failed check must never nag.
type updateCheckMsg struct {
	latest string
	err    error
}

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

// shouldCheckUpdate reports whether a startup update check is worthwhile:
// dev builds and unstamped versions skip it entirely (no nagging while
// developing).
func shouldCheckUpdate(version string) bool {
	return version != "" && version != "dev"
}

// checkUpdate queries the latest bodek release once at startup; the result
// arrives as updateCheckMsg. Returns nil when the check is gated off.
func (m *Model) checkUpdate() tea.Cmd {
	if !shouldCheckUpdate(m.bodekVersion) {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client := &http.Client{Timeout: 5 * time.Second}
		latest, _, err := update.LatestRelease(ctx, client, update.LatestURL)
		if err != nil {
			return updateCheckMsg{err: err}
		}
		return updateCheckMsg{latest: latest}
	}
}
