package tui

import (
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// frictionModel builds a recorder model with a friction-gated approval as
// the queue head — the approval-fatigue state where Alt chords were the only
// path, which macOS Option-as-UTF-8 terminals cannot deliver.
func frictionModel(t *testing.T) (*Model, chan string) {
	t.Helper()
	m, actions, _ := approvalRecorder(t)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Friction: true, FrictionApprovals: 3})
	return m, actions
}

// TestFrictionPlainKeysWorkWithoutAlt verifies the friction gate is usable
// without Alt chords: a bare 'a' opens the confirmation editor (it must NOT
// approve — the friction gate exists to slow approving), and a bare 'd'
// denies. Both only fire on an empty composer draft.
func TestFrictionPlainKeysWorkWithoutAlt(t *testing.T) {
	t.Run("bare a opens editor without approving", func(t *testing.T) {
		m, actions := frictionModel(t)
		m.Update(key("a"))
		if !m.apprEditing {
			t.Fatal("bare 'a' did not open the friction confirmation editor")
		}
		if m.curApproval() == nil {
			t.Fatal("bare 'a' must not answer the approval")
		}
		select {
		case got := <-actions:
			t.Fatalf("bare 'a' sent action %q — friction must require the typed word", got)
		default:
		}
	})

	t.Run("bare d denies", func(t *testing.T) {
		m, actions := frictionModel(t)
		_, cmd := m.Update(key("d"))
		exec(cmd)
		if got := awaitAction(t, actions); got != "deny" {
			t.Fatalf("bare 'd' action = %q, want deny", got)
		}
	})

	t.Run("draft guards plain keys", func(t *testing.T) {
		m, _ := frictionModel(t)
		m.ta.SetValue("draft")
		m.Update(key("d"))
		if m.curApproval() == nil {
			t.Fatal("bare 'd' denied while the composer held a draft")
		}
		if m.ta.Value() != "draftd" {
			t.Fatalf("composer draft = %q, want the letter typed", m.ta.Value())
		}
	})

	t.Run("editor still requires the word", func(t *testing.T) {
		m, actions := frictionModel(t)
		m.Update(key("a"))
		m.Update(key("approve"))
		_, cmd := m.Update(key("enter"))
		exec(cmd)
		if got := awaitAction(t, actions); got != "approve" {
			t.Fatalf("typed word action = %q, want approve", got)
		}
	})
}
