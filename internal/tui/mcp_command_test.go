package tui

import (
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
