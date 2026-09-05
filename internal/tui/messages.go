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

// eventBatchMsg is two or more pending events drained in one listen tick so
// a thinking_delta firehose does not run View between every fragment (that
// filled the client buffer and tripped odek's write-timeout disconnect).
type eventBatchMsg []client.Event

// maxEventBatch caps how many pending frames one listen tick consumes.
const maxEventBatch = 512

// errMsg reports a local (non-protocol) failure, e.g. a failed socket write.
type errMsg struct{ err error }

func errText(err error) string {
	if err != nil {
		return err.Error()
	}
	return "unknown error"
}

// approvalSendErrMsg is a failed approval_response write. Unlike errMsg it
// must not end the turn: the engine is still waiting on the request, so the
// popped head is restored and remaining queue items stay armed.
type approvalSendErrMsg struct {
	ev  client.Event
	dl  time.Time
	err error
}

// skillSendErrMsg is a failed skill_prompt_response write. Unlike errMsg it
// must not end the turn: the ack is local, the engine is still running, and
// the suggestion chip is restored so the operator can retry.
type skillSendErrMsg struct {
	ev  client.Event
	err error
}

// updateCheckMsg carries the startup latest-release lookup. The error is
// silent: a failed check must never nag.
type updateCheckMsg struct {
	latest string
	err    error
}

// listen blocks on the client's event channel and returns the next event as a
// tea.Msg. When more frames are already queued it drains them into one
// eventBatchMsg so Update can ingest the burst without a View per fragment.
func listen(ch <-chan client.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return eventMsg{Type: client.EventDisconnected}
		}
		batch := drainEvents(ch, ev)
		if len(batch) == 1 {
			return eventMsg(batch[0])
		}
		return eventBatchMsg(batch)
	}
}

// drainEvents appends pending events after first, stopping at maxEventBatch
// or when the channel would block. A closed channel or a disconnect frame
// is included and ends the drain.
func drainEvents(ch <-chan client.Event, first client.Event) []client.Event {
	batch := []client.Event{first}
	for len(batch) < maxEventBatch {
		select {
		case ev, ok := <-ch:
			if !ok {
				return append(batch, client.Event{Type: client.EventDisconnected})
			}
			batch = append(batch, ev)
			if ev.Type == client.EventDisconnected {
				return batch
			}
		default:
			return batch
		}
	}
	return batch
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
