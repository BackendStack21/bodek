package tui

import (
	"net/http"
	"strings"
	"testing"
)

// TestMgmtEpisodePromoteGate verifies promotion is a two-step action: p arms
// the gate (naming taint sources, summary length and short hash) without a
// request; any other key cancels without firing; y fires with the hash.
func TestMgmtEpisodePromoteGate(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openMemory()))
	m.panelSel = 2 // the pending episode

	_, cmd := m.Update(key("p"))
	if cmd != nil {
		m.Update(exec(cmd))
	}
	if standInSaw.promotes != 0 {
		t.Fatal("p alone fired the promote — gate missing")
	}
	if m.confirm != confirmEpisodePromote {
		t.Fatalf("p did not arm the promote gate: %d", m.confirm)
	}
	gate := plain(m.View())
	// The gate names the security-relevant review facts.
	for _, want := range []string{"promote", "browser", "chars", "deadbeef01"} {
		if !strings.Contains(gate, want) {
			t.Errorf("promote gate missing %q:\n%s", want, gate)
		}
	}

	// Any other key disarms without firing.
	m.Update(key("esc"))
	if m.confirm != confirmNone {
		t.Fatal("esc did not disarm the gate")
	}

	// y confirms.
	m.Update(key("p"))
	_, cmd = m.Update(key("y"))
	if cmd != nil {
		m.Update(exec(cmd))
	}
	if standInSaw.promotes != 1 {
		t.Errorf("y fired %d promotes, want 1", standInSaw.promotes)
	}
}

// TestMgmtEpisodeNoHashCannotPromote verifies a pending episode whose file
// could not be read (no summary_sha256) cannot promote: `p` is a no-op with
// the explanatory message.
func TestMgmtEpisodeNoHashCannotPromote(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openMemory()))
	// Replace the episode row with a hash-less one (server omitted it).
	m.memRows = append([]memRow(nil), m.memRows...)
	for i := range m.memRows {
		if m.memRows[i].kind == "episode" {
			m.memRows[i].summarySHA256 = ""
		}
	}
	m.panelSel = 2
	if cmd := m.memPromoteSelected(); cmd != nil {
		t.Fatal("promote fired on a hash-less episode")
	}
	if !strings.Contains(m.panelMsg, "cannot promote") {
		t.Errorf("panelMsg = %q", m.panelMsg)
	}
}

// TestMgmtEpisodePromote409 verifies a 409 conflict reloads the tab and says
// the summary changed — the user re-reviews the fresh text.
func TestMgmtEpisodePromote409(t *testing.T) {
	m := wired(t)
	standInSaw.promoteStatus = http.StatusConflict
	defer func() { standInSaw.promoteStatus = 0 }()
	m.Update(exec(m.openMemory()))
	m.panelSel = 2
	r := m.memSelected()
	if r == nil || r.summarySHA256 == "" {
		t.Fatalf("row = %+v", r)
	}
	m.Update(exec(m.memPromoteSelected()))
	if !strings.Contains(m.panelMsg, "changed since you reviewed") {
		t.Errorf("panelMsg = %q", m.panelMsg)
	}
	if !m.pendingMemReload {
		t.Error("409 did not schedule a memory refetch")
	}
}

// TestMgmtEpisodePromoteSuccess verifies the success note carries the
// promoted summary length and sources from the server's response.
func TestMgmtEpisodePromoteSuccess(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openMemory()))
	m.panelSel = 2
	m.Update(exec(m.memPromoteSelected()))
	if !strings.Contains(m.panelMsg, "episode promoted") || !strings.Contains(m.panelMsg, "browser") {
		t.Errorf("panelMsg = %q", m.panelMsg)
	}
}

// TestMgmtEpisodeDiscard verifies the discard flow: x arms a confirm gate on
// an episode row, y fires POST /api/memory/episodes/discard.
func TestMgmtEpisodeDiscard(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openMemory()))
	m.panelSel = 2
	_, cmd := m.Update(key("x"))
	if cmd != nil {
		m.Update(exec(cmd))
	}
	if standInSaw.discards != 0 {
		t.Fatal("x alone fired the discard — gate missing")
	}
	if m.confirm != confirmEpisodeDiscard {
		t.Fatalf("x did not arm the discard gate: %d", m.confirm)
	}
	_, cmd = m.Update(key("y"))
	if cmd != nil {
		m.Update(exec(cmd))
	}
	if standInSaw.discards != 1 {
		t.Errorf("y fired %d discards, want 1", standInSaw.discards)
	}
}

// TestMgmtEpisodeDetailRendering verifies the selected pending episode's
// detail view shows the full summary, its taint sources and short hash.
func TestMgmtEpisodeDetailRendering(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openMemory()))
	m.panelSel = 2
	m.panelDetail = true
	m.refresh()
	out := plain(m.View())
	for _, want := range []string{"fixed the login bug", "browser", "deadbeef01"} {
		if !strings.Contains(out, want) {
			t.Errorf("episode detail missing %q:\n%s", want, out)
		}
	}
}
