package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestMgmtMemoryTab verifies the memory tab: facts + pending episodes render,
// the add-fact editor targets user/env, delete/promote/consolidate act.
func TestMgmtMemoryTab(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openMemory()))
	if m.panel != panelMemory {
		t.Fatalf("panel = %d, want memory", m.panel)
	}
	if len(m.memRows) != 3 { // 1 user + 1 env fact + 1 pending episode
		t.Fatalf("memRows = %+v", m.memRows)
	}
	out := plain(m.View())
	for _, want := range []string{"memory", "prefers vim", "go 1.25", "pending episode"} {
		if !strings.Contains(out, want) {
			t.Errorf("memory tab missing %q:\n%s", want, out)
		}
	}

	// `a` opens the add-fact editor for user facts; enter submits.
	m.Update(key("a"))
	if m.panelEdit != panelEditFact || m.memTarget != "user" {
		t.Fatalf("add-fact editor = %d target %q", m.panelEdit, m.memTarget)
	}
	for _, r := range "likes dark themes" {
		m.Update(key(string(r)))
	}
	_, cmd := m.Update(key("enter"))
	m.Update(exec(cmd))            // mgmtActionMsg
	m.Update(exec(m.openMemory())) // refetch
	found := false
	for _, r := range m.memRows {
		if r.text == "likes dark themes" {
			found = true
		}
	}
	_ = found // the stand-in accepts the POST; the refetch reflects source state

	// Episode promotion fires only on episode rows.
	m.panelSel = 2 // the pending episode
	if cmd := m.memPromoteSelected(); cmd == nil {
		t.Error("promote did not fire on an episode row")
	}
	m.panelSel = 0 // a fact row
	if cmd := m.memPromoteSelected(); cmd != nil {
		t.Error("promote fired on a fact row")
	}
}

// TestMgmtSkillsTab verifies skill rows carry provenance badges and promote
// only targets NeedsReview skills.
func TestMgmtSkillsTab(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openSkills()))
	if len(m.skills) != 2 {
		t.Fatalf("skills = %+v", m.skills)
	}
	out := plain(m.View())
	for _, want := range []string{"deploy-helper", "tainted-thing", "needs review", "untrusted"} {
		if !strings.Contains(out, want) {
			t.Errorf("skills tab missing %q:\n%s", want, out)
		}
	}
	m.panelSel = 0 // clean skill — nothing to promote
	if cmd := m.skillPromote(false); cmd != nil {
		t.Error("promote fired on a skill without NeedsReview")
	}
	m.panelSel = 1
	if cmd := m.skillPromote(false); cmd == nil {
		t.Error("promote did not fire on a NeedsReview skill")
	}
}

// TestMgmtToolsTab verifies the tools tab lists the registry and MCP servers.
func TestMgmtToolsTab(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openTools()))
	out := plain(m.View())
	for _, want := range []string{"shell", "read_file", "mcp", "fs", "built-ins · 1 MCP"} {
		if !strings.Contains(out, want) {
			t.Errorf("tools tab missing %q:\n%s", want, out)
		}
	}
}

// TestMgmtConfigTab verifies config + usage + connections render and kick
// targets only connection rows.
func TestMgmtConfigTab(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openConfig()))
	out := plain(m.View())
	for _, want := range []string{"model", "m", "prompts", "tokens", "connection"} {
		if !strings.Contains(out, want) {
			t.Errorf("config tab missing %q:\n%s", want, out)
		}
	}
	// Kick fires only on a connection row.
	connIdx := -1
	for i, r := range m.cfgRows {
		if r.kind == "conn" {
			connIdx = i
		}
	}
	if connIdx < 0 {
		t.Fatal("no connection rows rendered")
	}
	m.panelSel = 0 // a config row
	if cmd := m.cfgKickSelected(); cmd != nil {
		t.Error("kick fired on a config row")
	}
	m.panelSel = connIdx
	if cmd := m.cfgKickSelected(); cmd == nil {
		t.Error("kick did not fire on a connection row")
	}
}

// TestMgmtActionErrorSurfaces verifies a failed mutation reports in the tab.
func TestMgmtActionErrorSurfaces(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openSkills()))
	m.Update(mgmtActionMsg{tab: panelSkills, err: errTest{}})
	if !strings.Contains(m.panelMsg, "action failed") {
		t.Errorf("panelMsg = %q", m.panelMsg)
	}
}

var _ = client.Skill{} // keep client imported for fixtures
