package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestShutdownTypedConfirmation drives the death-gate: S opens the typed
// confirmation, a mistyped word resets, the literal "shutdown" fires the
// request, and the socket drop that follows reads as an expected event —
// no reconnect spiral, with the ⏎ fresh-start affordance.
func TestShutdownTypedConfirmation(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openConfig()))
	if m.panel != panelConfig {
		t.Fatal("config tab did not open")
	}

	// S opens the gate; a mistyped word never fires.
	m.Update(key("s"))
	if m.panelEdit != panelEditShutdown {
		t.Fatal("S did not open the shutdown confirmation")
	}
	for _, r := range "shutdownx" {
		m.Update(key(string(r)))
	}
	m.Update(key("enter"))
	if m.shutdownReq {
		t.Fatal("mistyped word armed the shutdown")
	}

	// The literal word fires, and the disconnect lands in the expected state.
	for _, r := range "shutdown" {
		m.Update(key(string(r)))
	}
	_, cmd := m.Update(key("enter"))
	m.Update(exec(cmd)) // shutdownDoneMsg (nil err on the stand-in)
	if !m.shutdownReq {
		t.Fatal("typed confirmation did not arm the expected drop")
	}
	m.handleEvent(client.Event{Type: client.EventDisconnected})
	if m.status != "server shut down" {
		t.Errorf("status = %q, want the expected-drop state", m.status)
	}
	if note, exp := lastNoteMatching(m, "⏎ starts a fresh instance"); note == "" || exp.IsZero() {
		t.Errorf("fresh-start hint missing or not expiring: %q", note)
	}
	// No reconnect was scheduled: a retry tick would have flipped the status.
	if strings.HasPrefix(m.status, "reconnect") {
		t.Error("expected drop spiraled into reconnects")
	}
}

// TestShutdownWithoutRequestReconnects verifies an UNrequested drop still
// auto-reconnects — the expected-drop branch must not leak to normal
// disconnects.
func TestShutdownWithoutRequestReconnects(t *testing.T) {
	m := wired(t)
	m.opts.Reconnect = func() (*client.Client, error) { return nil, errTest{} }
	m.handleEvent(client.Event{Type: client.EventDisconnected})
	if m.status != "reconnecting…" {
		t.Errorf("normal drop did not reconnect: %q", m.status)
	}
}
