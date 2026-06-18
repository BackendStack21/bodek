package tui

import (
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestApprovalUnmatchedAndNoTrust(t *testing.T) {
	m := wired(t)
	// AllowTrust=false: pressing "t" must NOT resolve the approval.
	m.approval = &client.Event{Type: "approval_request", AllowTrust: false}
	m.Update(key("t"))
	if m.approval == nil {
		t.Error("'t' without AllowTrust should not clear approval")
	}
	// An unrelated key falls through to a no-op.
	m.Update(key("z"))
	if m.approval == nil {
		t.Error("unrelated key should leave approval pending")
	}
}

func TestSyncACUnchangedQuery(t *testing.T) {
	m := wired(t)
	m.ta.SetValue("see @doc")
	m.ac.open = true
	m.ac.query = "doc"
	if cmd := m.syncAC(); cmd != nil {
		t.Error("syncAC with an unchanged query should return nil")
	}
	// No active ref while popup open → closeAC path.
	m.ta.SetValue("plain text")
	m.ac.open = true
	if cmd := m.syncAC(); cmd != nil {
		t.Error("syncAC with no ref should return nil and close")
	}
	if m.ac.open {
		t.Error("syncAC should have closed the popup")
	}
}

func TestArgPreviewURLKey(t *testing.T) {
	if got := argPreview(`{"url":"http://x"}`); got != "http://x" {
		t.Errorf("argPreview url = %q", got)
	}
}
