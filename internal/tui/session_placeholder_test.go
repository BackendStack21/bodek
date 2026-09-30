package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/BackendStack21/bodek/internal/workspace"
)

// Regression tests for the connect-time placeholder session id: between the
// first prompt and the session frame that confirms the minted id, m.sessionID
// may still hold the placeholder the connect-time session event stamped —
// /copy-session-id must never hand it to the clipboard, and /new must not let
// the fresh connection's first session frame re-persist it.

// BUG 1: a prompt arms sessionLive while the id is still the connect-time
// placeholder; /copy-session-id must wait for the post-prompt session frame.
func TestCopySessionIDPlaceholderNotCopiedBetweenPromptAndConfirm(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "session", SessionID: "sess-placeholder"})
	_ = m.sendPrompt("hi") // arms sessionLive; the confirm frame has not arrived

	runCopySessionID(m)
	if m.copyFlashing() {
		t.Error("placeholder id copied before the post-prompt session frame confirmed it")
	}
	if n := len(m.notices); n == 0 || !strings.Contains(m.notices[n-1], "no session") {
		t.Errorf("placeholder window must show the no-session note, got %v", m.notices)
	}

	m.handleEvent(client.Event{Type: "session", SessionID: "sess-real"})
	runCopySessionID(m)
	if !m.copyFlashing() {
		t.Error("confirmed session id must copy after the post-prompt session frame")
	}
}

// BUG 3: the old wall-clock gate compared sessionIDAt against
// lastPromptStart, which sendPrompt re-stamps on every dispatch. odek does
// not re-emit a session frame per turn, so after the SECOND prompt the
// confirmed stamp was older than the prompt stamp and /copy-session-id
// permanently reported "no session yet". The ordering-based gate
// (awaitSessionConfirm, armed per prompt, cleared by any session frame)
// must keep the confirmed id copyable across later prompts.
func TestCopySessionIDStillCopiesAfterSecondPrompt(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "session", SessionID: "sess-placeholder"})
	_ = m.sendPrompt("first")
	m.handleEvent(client.Event{Type: "session", SessionID: "sess-real"}) // confirms the minted id
	_ = m.sendPrompt("second")                                           // no further session frame arrives

	runCopySessionID(m)
	if !m.copyFlashing() {
		t.Error("the confirmed session id must still copy after a second prompt")
	}
}

// Resume path is unaffected: an adopted session is live and copyable without
// any prompt.
func TestCopySessionIDResumedSessionCopiesWithoutPrompt(t *testing.T) {
	m := wired(t)
	m.handleSessionDetail(sessionDetailMsg{sess: client.Session{ID: "sess-resumed"}})
	runCopySessionID(m)
	if !m.copyFlashing() {
		t.Error("an adopted (resumed) session id must copy without a prompt")
	}
}

// BUG 2: the reconnect consumes freshStart before the fresh connection's
// session frame is ingested, so rememberSession re-persists the placeholder.
func TestNewReconnectDoesNotRepersistPlaceholder(t *testing.T) {
	t.Setenv("BODEK_WORKSPACE", filepath.Join(t.TempDir(), "workspaces.json"))
	m := wired(t)
	m.ws = workspace.Open()
	m.opts.CWD = "/tmp/bodek-test-fresh-remember"
	_ = m.ws.Save(m.opts.CWD, workspace.State{SessionID: "s1"})
	seedSessionState(m)

	runNew(m)
	if got := m.ws.Load(m.opts.CWD).SessionID; got != "" {
		t.Fatalf("/new must clear the mapping, got %q", got)
	}

	m.disconn = true
	m.handleReconnect(reconnectMsg{cl: m.cl})
	m.handleEvent(client.Event{Type: "session", SessionID: "sess-placeholder"})

	if got := m.ws.Load(m.opts.CWD).SessionID; got != "" {
		t.Errorf("fresh connection's placeholder frame re-persisted session %q", got)
	}

	// The suppression is one-shot: the real id from a later frame persists.
	m.handleEvent(client.Event{Type: "session", SessionID: "sess-real"})
	if got := m.ws.Load(m.opts.CWD).SessionID; got != "sess-real" {
		t.Errorf("real session id must persist after the fresh session starts, got %q", got)
	}
}
