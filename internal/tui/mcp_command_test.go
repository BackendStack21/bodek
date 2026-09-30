package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// runSlash executes a "/name args" line through the command registry and
// feeds the resulting message back into the model (the e2e command path).
func runSlash(t *testing.T, m *Model, line string) {
	t.Helper()
	name := strings.TrimPrefix(line, "/")
	args := ""
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		name, args = name[:i], strings.TrimSpace(name[i+1:])
	}
	var cmd tea.Cmd
	if c := findCommand(name); c != nil {
		cmd = c.run(m, args)
	} else {
		t.Fatalf("command %q not in registry", name)
	}
	if cmd != nil {
		m.Update(exec(cmd))
	}
}

func findCommand(name string) *command {
	for i := range slashCommands() {
		if slashCommands()[i].name == name {
			return &slashCommands()[i]
		}
	}
	return nil
}

// TestMCPCommandRegistry verifies the /mcp registry entry exists and is
// offered by the slash popup and the /help card.
func TestMCPCommandRegistry(t *testing.T) {
	c := findCommand("mcp")
	if c == nil {
		t.Fatal("/mcp not registered in slashCommands()")
	}
	if c.desc == "" {
		t.Fatal("/mcp has no description")
	}
	m := wired(t)
	if !commandOffered(m, *c) {
		t.Error("/mcp must be offered unconditionally (server-level, no session gate)")
	}
	m.ta.SetValue("/")
	m.Update(key("m"))
	m.Update(key("c"))
	m.Update(key("p"))
	found := false
	for _, it := range m.ac.items {
		if it.ID == "/mcp" {
			found = true
		}
	}
	if !found {
		t.Errorf("slash popup missing /mcp: %+v", m.ac.items)
	}
}

// TestMCPBareOpensToolsTabOnMCPSection verifies bare /mcp opens the drawer
// Tools tab with the first MCP server row focused.
func TestMCPBareOpensToolsTabOnMCPSection(t *testing.T) {
	m := wired(t)
	runSlash(t, m, "/mcp")
	if m.panel != panelTools {
		t.Fatalf("panel = %d, want tools", m.panel)
	}
	if len(m.toolRows) == 0 {
		t.Fatal("toolRows empty after /mcp")
	}
	sel := m.toolSelected()
	if sel == nil || sel.kind != "mcp" {
		t.Fatalf("selection = %+v, want first mcp row focused", sel)
	}
	if sel.text != "fs" {
		t.Fatalf("focused server = %q, want fs", sel.text)
	}
}

// TestMCPNamedJump verifies /mcp <name> focuses that server's row
// case-insensitively.
func TestMCPNamedJump(t *testing.T) {
	m := wired(t)
	runSlash(t, m, "/mcp FS")
	if m.panel != panelTools {
		t.Fatalf("panel = %d, want tools", m.panel)
	}
	sel := m.toolSelected()
	if sel == nil || sel.kind != "mcp" || sel.text != "fs" {
		t.Fatalf("selection = %+v, want mcp row fs", sel)
	}
}

// TestMCPUnknownName verifies an unknown server name reports in the drawer
// footer without moving the tab.
func TestMCPUnknownName(t *testing.T) {
	m := wired(t)
	runSlash(t, m, "/mcp nope")
	if m.panel != panelTools {
		t.Fatalf("panel = %d, want tools", m.panel)
	}
	if !strings.Contains(m.panelMsg, "no MCP server named") {
		t.Errorf("panelMsg = %q, want a not-found note", m.panelMsg)
	}
}

// TestMCPOneShotSemantics pins the one-shot focus contract: a /mcp jump
// consumed by one fetch never re-applies, and a tab switch before the fetch
// lands disarms it entirely.
func TestMCPOneShotSemantics(t *testing.T) {
	// /mcp fs, fetch lands → focus applied once. A later plain /tools
	// fetch must NOT re-apply the focus.
	m := wired(t)
	runSlash(t, m, "/mcp fs")
	if r := m.toolSelected(); r == nil || r.text != "fs" {
		t.Fatalf("focus not applied: %+v", r)
	}
	m.Update(exec(m.openTools())) // plain /tools re-fetch
	if r := m.toolSelected(); r != nil && r.kind == "mcp" {
		t.Fatalf("stale focus re-applied on a plain /tools fetch: %+v", r)
	}

	// /mcp fs then a tab switch before the fetch lands: the late tools
	// result is dropped by the cross-tab guard AND the jump is disarmed,
	// so a subsequent Tools load must not apply the stale focus.
	m2 := wired(t)
	runSlash(t, m2, "/mcp fs")
	m2.Update(exec(m2.switchDrawerTab(panelMemory))) // tab switch disarms
	m2.Update(exec(m2.openTools()))
	if r := m2.toolSelected(); r != nil && r.kind == "mcp" {
		t.Fatalf("disarmed jump re-applied after tab switch: %+v", r)
	}
}

// TestMCPErrorConsumesFocus verifies a failed tools fetch consumes the
// one-shot focus instead of leaking it into the next successful load.
func TestMCPErrorConsumesFocus(t *testing.T) {
	m := wired(t)
	runSlash(t, m, "/mcp fs")
	m.handleMgmtMsg(mgmtMsg{tab: panelTools, err: errors.New("boom")})
	if m.mcpJump || m.mcpFocus != "" {
		t.Fatalf("error left the jump armed: jump=%v focus=%q", m.mcpJump, m.mcpFocus)
	}
	m.handleMgmtMsg(mgmtMsg{tab: panelTools, mcpN: 1, tls: []client.Tool{{Name: "shell", Enabled: true}}})
	if r := m.toolSelected(); r != nil && r.kind == "mcp" {
		t.Fatalf("stale focus applied after an error recovery load: %+v", r)
	}
}

// TestMCPUnknownNotClobbered verifies the not-found note survives the
// zero-MCP-server note on the same frame.
func TestMCPUnknownNotClobbered(t *testing.T) {
	m := wired(t)
	m.panel = panelTools
	m.mcpJump, m.mcpFocus = true, "gone"
	m.handleMgmtMsg(mgmtMsg{tab: panelTools, mcpN: 0, tls: []client.Tool{{Name: "shell", Enabled: true}}})
	if !strings.Contains(m.panelMsg, "no MCP server named") {
		t.Errorf("not-found note clobbered: %q", m.panelMsg)
	}
}

// TestMCPEnabledStateVisible verifies MCP rows and the detail view surface
// the enabled/disabled state odek sends on /api/mcp.
func TestMCPEnabledStateVisible(t *testing.T) {
	m := newTestModel()
	m.panel = panelTools
	m.handleMgmtMsg(mgmtMsg{tab: panelTools, mcpN: 2, tls: []client.Tool{{Name: "shell", Enabled: true}},
		mcp: []client.MCPServer{
			{Name: "fs", Command: "bun", Enabled: true},
			{Name: "vault", Command: "node", Enabled: false},
		}})
	if len(m.toolRows) != 3 {
		t.Fatalf("toolRows = %d, want 3", len(m.toolRows))
	}
	var fsRow, vaultRow *toolRow
	for i := range m.toolRows {
		switch r := &m.toolRows[i]; r.id {
		case "fs":
			fsRow = r
		case "vault":
			vaultRow = r
		}
	}
	if fsRow == nil || vaultRow == nil {
		t.Fatalf("missing mcp rows: %+v", m.toolRows)
	}
	if !strings.Contains(fsRow.dim, "enabled") || strings.Contains(fsRow.dim, "disabled") {
		t.Errorf("fs row dim = %q, want an \"enabled\" marker", fsRow.dim)
	}
	if !strings.Contains(vaultRow.dim, "disabled") {
		t.Errorf("vault row dim = %q, want \"disabled\" marker", vaultRow.dim)
	}
	// Detail view of the disabled server states the fact explicitly.
	for i := range m.toolRows {
		if m.toolRows[i].id == "vault" {
			m.panelSel = i
		}
	}
	m.panelDetail = true
	out := plain(m.View())
	if !strings.Contains(out, "disabled") {
		t.Errorf("mcp detail view missing \"disabled\":\n%s", out)
	}
}

// TestMCPZeroServers verifies the zero-server empty state: the Tools tab
// still opens (it has native tools) with a "no MCP servers configured" note.
func TestMCPZeroServers(t *testing.T) {
	m := wired(t)
	m.panel = panelTools
	m.handleMgmtMsg(mgmtMsg{tab: panelTools, mcpN: 0, tls: []client.Tool{
		{Name: "shell", Enabled: true}, {Name: "read_file", Enabled: true},
	}})
	if m.panel != panelTools {
		t.Fatalf("panel = %d, want tools", m.panel)
	}
	if !strings.Contains(m.panelMsg, "no MCP servers configured") {
		t.Errorf("panelMsg = %q, want the zero-servers note", m.panelMsg)
	}
	if len(m.toolRows) != 2 {
		t.Fatalf("toolRows = %d, want the 2 built-ins", len(m.toolRows))
	}
}
