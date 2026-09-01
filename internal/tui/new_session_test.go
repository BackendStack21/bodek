package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// /new starts a genuinely fresh session: the transcript is wiped AND the
// connection drops its session identity, so the forced redial lands on a
// sessionless connection — odek binds sessions per connection, and the next
// prompt on a sessionless one mints a brand-new session server-side. The old
// session stays on disk, resumable via /sessions.

// runNew invokes the /new command through the registry.
func runNew(m *Model) tea.Cmd {
	for _, c := range slashCommands() {
		if c.name == "new" {
			return c.run(m, "")
		}
	}
	return nil
}

func TestNewCommandRegistered(t *testing.T) {
	for _, c := range slashCommands() {
		if c.name == "new" {
			if c.desc == "" {
				t.Fatal("/new registered with an empty description")
			}
			return
		}
	}
	t.Fatal("/new is not in the command registry")
}

func seedSessionState(m *Model) {
	m.sessionID = "s1"
	m.authToken = "a1"
	m.model = "glm-5.3-flash"
	seedConversation(m)
	m.planInit = true
	m.planVer = 2
	m.planAvail = planAvailable
}

func TestNewGuards(t *testing.T) {
	t.Run("busy", func(t *testing.T) {
		m := wired(t)
		seedSessionState(m)
		busyTurn(m)

		if cmd := runNew(m); cmd == nil {
			t.Fatal("busy /new returned no command (want a note cmd)")
		}
		if m.sessionID != "s1" || len(m.msgs) == 0 {
			t.Fatalf("busy /new must be a no-op: sid=%q msgs=%d", m.sessionID, len(m.msgs))
		}
	})
	t.Run("pending approval", func(t *testing.T) {
		m := wired(t)
		seedSessionState(m)
		m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1"})

		if cmd := runNew(m); cmd == nil {
			t.Fatal("/new with a pending approval returned no command (want a note cmd)")
		}
		if m.sessionID != "s1" || len(m.msgs) == 0 {
			t.Fatalf("/new must not run with a pending approval: sid=%q msgs=%d", m.sessionID, len(m.msgs))
		}
	})
	t.Run("queued prompts", func(t *testing.T) {
		m := wired(t)
		seedSessionState(m)
		m.queue = []string{"queued for THIS conversation"}

		if cmd := runNew(m); cmd == nil {
			t.Fatal("/new with a queue returned no command (want a note cmd)")
		}
		if m.sessionID != "s1" || len(m.queue) != 1 {
			t.Fatalf("/new must not run with queued prompts: sid=%q queue=%d", m.sessionID, len(m.queue))
		}
	})
}

func TestNewResetsAndDropsIdentity(t *testing.T) {
	m := wired(t)
	seedSessionState(m)

	cmd := runNew(m)
	if cmd == nil {
		t.Fatal("/new returned no command (want the socket close)")
	}

	if m.sessionID != "" || m.authToken != "" {
		t.Fatalf("session identity not dropped: sid=%q tok=%q", m.sessionID, m.authToken)
	}
	if len(m.msgs) != 0 || m.turnStats != nil || m.toolTotal != 0 {
		t.Fatalf("transcript not wiped: msgs=%d stats=%d tools=%d", len(m.msgs), len(m.turnStats), m.toolTotal)
	}
	if m.planInit || m.planAvail != planUnknown || m.planVer != 0 {
		t.Fatalf("plan state not reset: init=%v avail=%v ver=%d", m.planInit, m.planAvail, m.planVer)
	}
	if m.pendModel != "glm-5.3-flash" {
		t.Errorf("model not re-asserted on the next prompt: pendModel=%q", m.pendModel)
	}
	if !m.freshStart {
		t.Error("freshStart flag not armed — reconnect will show the resume note")
	}
	// The reconnect must have nothing to re-adopt: adoptSession no-ops on an
	// empty session id, so the fresh connection stays sessionless.
	if cmd := m.adoptSession(); cmd != nil {
		t.Error("adoptSession is not a no-op after /new — the old session would be re-adopted")
	}
}

func TestNewReconnectLandsFresh(t *testing.T) {
	m := wired(t)
	seedSessionState(m)
	runNew(m)
	m.disconn = true // the socket close lands as EventDisconnected

	m.handleReconnect(reconnectMsg{cl: m.cl})

	if m.disconn || m.status != "ready" {
		t.Fatalf("reconnect did not land: disconn=%v status=%q", m.disconn, m.status)
	}
	if m.freshStart {
		t.Error("freshStart flag not consumed by the reconnect")
	}
	if m.sessionID != "" {
		t.Fatalf("reconnect re-adopted a session: %q", m.sessionID)
	}
	found := false
	for _, n := range m.notices {
		if strings.Contains(n, "fresh session") {
			found = true
		}
	}
	if !found {
		t.Errorf("no fresh-session note after reconnect: %v", m.notices)
	}
}
