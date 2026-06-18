package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestUpdateMessageDispatch(t *testing.T) {
	m := wired(t)

	m.Update(sessionsMsg{items: []client.Session{{ID: "s1"}}})
	m.Update(modelsMsg{items: []client.ModelInfo{{ID: "m1"}}})
	m.Update(sessionDetailMsg{sess: client.Session{ID: "s1"}, token: "a1"})
	m.Update(sessionDeletedMsg{id: "s1"})
	m.Update(cancelDoneMsg{})
	m.Update(cancelDoneMsg{err: errTest{}})

	// Spinner tick while busy triggers a refresh path.
	m.busy = true
	m.runStart = time.Now()
	m.Update(spinner.TickMsg{})
	m.busy = false
	m.ac.loading = true
	m.Update(spinner.TickMsg{})
}

func TestCurOutOfRange(t *testing.T) {
	m := wired(t)
	m.curIdx = 99 // no messages
	if m.cur() != -1 {
		t.Error("cur should be -1 when index is out of range")
	}
	// token event with no current message must be a safe no-op.
	m.handleEvent(client.Event{Type: "token", Content: "x"})
}

func TestAutocompleteNavKeys(t *testing.T) {
	m := wired(t)
	m.ac.open = true
	m.ac.items = []client.Resource{{ID: "@a", Label: "a"}, {ID: "@b", Label: "b"}}
	m.Update(key("ctrl+n")) // down
	if m.ac.sel != 1 {
		t.Errorf("ctrl+n sel = %d", m.ac.sel)
	}
	m.Update(key("ctrl+p")) // up
	if m.ac.sel != 0 {
		t.Errorf("ctrl+p sel = %d", m.ac.sel)
	}
	m.Update(key("up")) // already at top — no move
	m.Update(key("tab"))
	if m.ac.open {
		t.Error("tab should accept and close")
	}
	// esc closes an open popup.
	m.ac.open = true
	m.ac.items = []client.Resource{{ID: "@a", Label: "a"}}
	m.Update(key("esc"))
	if m.ac.open {
		t.Error("esc should close popup")
	}
}

func TestPanelKeyBoundsAndQuit(t *testing.T) {
	m := wired(t)
	m.panel = panelSessions
	m.sessions = []client.Session{{ID: "s1"}}
	m.Update(key("up"))   // at top, no move
	m.Update(key("down")) // at bottom (only 1), no move
	if m.panelSel != 0 {
		t.Errorf("panelSel = %d", m.panelSel)
	}
	m.Update(key("ctrl+c"))
	if !m.quitting {
		t.Error("ctrl+c in panel should quit")
	}
}

func TestCancelRunGuards(t *testing.T) {
	m := wired(t)
	// Not busy → nil.
	if m.cancelRun() != nil {
		t.Error("cancelRun not busy should be nil")
	}
	// Busy but no session id → nil.
	m.busy = true
	if m.cancelRun() != nil {
		t.Error("cancelRun without session should be nil")
	}
}

func TestDeleteSelectedGuard(t *testing.T) {
	m := wired(t)
	m.panel = panelSessions
	m.sessions = nil
	m.panelSel = 5
	if m.deleteSelected() != nil {
		t.Error("deleteSelected out of range should be nil")
	}
}

func TestPanelSelectGuards(t *testing.T) {
	m := wired(t)
	// panelSelect with selection beyond range is a safe no-op.
	m.panel = panelSessions
	m.sessions = nil
	m.panelSel = 3
	if m.panelSelect() != nil {
		t.Error("panelSelect sessions out-of-range should be nil")
	}
	m.panel = panelModels
	m.models = nil
	if m.panelSelect() != nil {
		t.Error("panelSelect models out-of-range should be nil")
	}
	m.panel = panelNone
	if m.panelSelect() != nil {
		t.Error("panelSelect with no panel should be nil")
	}
}
